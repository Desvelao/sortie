package destination

import (
	"context"
	"errors"
	"os"
	"sync"
	"syscall"

	"sortie/internal/tags"
)

// Status of one (file, tag) destination.
type Status string

const (
	StatusReady         Status = "READY"          // file can be copied
	StatusAlreadyCopied Status = "ALREADY_COPIED" // identical content exists
	StatusConflict      Status = "CONFLICT"       // different content exists
	StatusUnresolvedVar Status = "UNRESOLVED_VAR" // a variable of the template cannot be determined
	StatusInvalidPath   Status = "INVALID_PATH"   // destination path is invalid
	StatusError         Status = "ERROR"          // filesystem error
)

// Check is the result for one tag of one file.
type Check struct {
	TagID   int
	TagName string
	Path    string // destination path ("" if it could not be resolved)
	Status  Status
	Detail  string
	SrcHash string // set when a content comparison was needed
	DstHash string
}

// CanCopy is true only for READY: unresolved variables, invalid paths, conflicts and
// errors must never be copied automatically.
func (c Check) CanCopy() bool { return c.Status == StatusReady }

// AllBlocked reports whether there are checks and none can ever be copied: every
// one is StatusInvalidPath (for example the destination root is missing) or
// StatusUnresolvedVar. The UI then disables its copy button. Other statuses never
// count: a conflict can still be resolved by the conflict policy, and an error
// may be temporary.
func AllBlocked(cs []Check) bool {
	if len(cs) == 0 {
		return false
	}
	for _, c := range cs {
		if c.Status != StatusInvalidPath && c.Status != StatusUnresolvedVar {
			return false
		}
	}
	return true
}

// Source describes the file being checked.
type Source struct {
	Path string
	Name string
	Vars map[string]string // resolved variable values; unresolved ones are absent
}

// Checker evaluates destinations. It is safe for concurrent use; hashing runs
// in a bounded worker pool and is cancellable.
type Checker struct{ h *hasher }

// NewChecker allows at most hashWorkers concurrent hash computations.
func NewChecker(hashWorkers int) *Checker {
	if hashWorkers < 1 {
		hashWorkers = 1
	}
	return &Checker{h: newHasher(hashWorkers)}
}

// Check evaluates every tag independently (in parallel) and returns the
// results in the order of ts. It never writes to the filesystem.
func (c *Checker) Check(ctx context.Context, r Resolver, src Source, ts []tags.Tag) []Check {
	out := make([]Check, len(ts))
	var wg sync.WaitGroup
	for i, t := range ts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = c.checkOne(ctx, r, src, t)
		}()
	}
	wg.Wait()
	return out
}

func (c *Checker) checkOne(ctx context.Context, r Resolver, src Source, t tags.Tag) Check {
	chk := Check{TagID: t.ID, TagName: t.Name}
	// The copy refuses a root that is missing or not a directory, so the check must not call it ready.
	if err := r.CheckRoot(); err != nil {
		chk.Status, chk.Detail = StatusInvalidPath, err.Error()
		return chk
	}
	path, err := r.Resolve(src.Name, src.Vars, t)
	if err != nil {
		chk.Status, chk.Detail = StatusInvalidPath, err.Error()
		if errors.Is(err, ErrUnresolved) {
			chk.Status = StatusUnresolvedVar
		}
		return chk
	}
	chk.Path = path
	c.compare(ctx, src.Path, &chk)
	return chk
}

// compare fills chk.Status from the state of the destination file.
func (c *Checker) compare(ctx context.Context, srcPath string, chk *Check) {
	fail := func(s Status, d string) { chk.Status, chk.Detail = s, d }
	sst, err := os.Stat(srcPath)
	if err != nil {
		fail(StatusError, "source: "+err.Error())
		return
	}
	dst, err := os.Stat(chk.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		chk.Status = StatusReady
		return
	case errors.Is(err, syscall.ENOTDIR):
		fail(StatusInvalidPath, "a parent of the destination is a file, not a directory")
		return
	case err != nil:
		fail(StatusError, err.Error())
		return
	}
	switch {
	case os.SameFile(sst, dst):
		fail(StatusInvalidPath, "destination is the source file itself")
		return
	case dst.IsDir():
		fail(StatusConflict, "a directory exists at the destination")
		return
	case !dst.Mode().IsRegular():
		fail(StatusConflict, "a non-regular file exists at the destination")
		return
	case dst.Size() != sst.Size():
		fail(StatusConflict, "a file with different size exists")
		return
	}
	sh, err := c.h.Sum(ctx, srcPath)
	if err != nil {
		fail(StatusError, "hashing source: "+err.Error())
		return
	}
	dh, err := c.h.Sum(ctx, chk.Path)
	if err != nil {
		fail(StatusError, "hashing destination: "+err.Error())
		return
	}
	chk.SrcHash, chk.DstHash = sh, dh
	if sh == dh {
		chk.Status = StatusAlreadyCopied
	} else {
		fail(StatusConflict, "a file with the same size but different content exists")
	}
}

// Compare checks an explicit destination path against the source (used by the
// copy engine for alternative file names and race re-evaluation).
func (c *Checker) Compare(ctx context.Context, srcPath, destPath string) Check {
	chk := Check{Path: destPath}
	c.compare(ctx, srcPath, &chk)
	return chk
}
