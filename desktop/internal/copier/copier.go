// Package copier copies classified files into their destinations. It is
// independent of the GUI. Source files are only ever opened read-only; the
// destination is written through temporary files and an atomic no-clobber
// rename, so an existing file is never overwritten silently.
package copier

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"sortie/internal/destination"
	"sortie/internal/tags"
)

// ConflictPolicy decides what happens when a different file already exists.
type ConflictPolicy int

const (
	PolicySkip    ConflictPolicy = iota // default
	PolicyRename                        // write "name (1).ext"
	PolicyReplace                       // overwrite; requires Options.ConfirmReplace
)

func (p ConflictPolicy) String() string {
	return [...]string{"skip", "rename", "replace"}[p]
}

// ResultStatus is the outcome of one job. Values double as history statuses.
type ResultStatus string

const (
	StatusCopied        ResultStatus = "COPIED"
	StatusReplaced      ResultStatus = "REPLACED"
	StatusRenamed       ResultStatus = "RENAMED"
	StatusAlreadyCopied ResultStatus = "ALREADY_COPIED"
	StatusConflictSkip  ResultStatus = "CONFLICT_SKIPPED"
	StatusInvalid       ResultStatus = "SKIPPED_INVALID" // variable or path unusable
	StatusFailed        ResultStatus = "FAILED"
	StatusCancelled     ResultStatus = "CANCELLED"
)

// Job is one (file, tag) copy.
type Job struct {
	FileID     int64
	SourcePath string
	Name       string
	Size       int64             // size recorded by the scan
	ModTime    time.Time         // mtime recorded by the scan; zero disables the changed-since-scan check
	Vars       map[string]string // resolved variable values for the destination template
	Tag        tags.Tag
}

// Result is the outcome of one job.
type Result struct {
	Job      Job
	Status   ResultStatus
	DestPath string
	Detail   string
	SrcHash  string
	DstHash  string
	Bytes    int64
}

// OK reports whether the destination holds the file afterwards.
func (r Result) OK() bool {
	switch r.Status {
	case StatusCopied, StatusReplaced, StatusRenamed, StatusAlreadyCopied:
		return true
	}
	return false
}

// EventKind classifies progress events.
type EventKind int

const (
	EventStart EventKind = iota // a job started
	EventBytes                  // bytes copied within a large file
	EventDone                   // a job finished
)

// Event reports progress. Calls to Options.OnEvent are serialised.
type Event struct {
	Kind       EventKind
	Index      int // job index
	Total      int // number of jobs
	Done       int // jobs finished so far
	Job        Job
	Result     Result // EventDone only
	Bytes      int64  // EventBytes only
	BytesTotal int64
}

// Options configures a run.
type Options struct {
	Policy         ConflictPolicy
	ConfirmReplace bool // must be true to use PolicyReplace
	Workers        int  // concurrent copies, default 2
	OnEvent        func(Event)
}

// Summary aggregates a run.
type Summary struct {
	Results                                  []Result // same order as the jobs
	Copied, Replaced, Renamed, AlreadyCopied int
	ConflictsSkipped, Invalid, Failed        int
	Cancelled                                int
	Bytes                                    int64
}

// ErrReplaceNotConfirmed is returned when PolicyReplace is used without
// explicit confirmation.
var ErrReplaceNotConfirmed = errors.New("replacing existing files requires explicit confirmation")

// Engine runs copy jobs.
type Engine struct {
	checker *destination.Checker
	hist    History
}

// New creates an engine; hist may be nil.
func New(checker *destination.Checker, hist History) *Engine {
	return &Engine{checker: checker, hist: hist}
}

func preflight(r destination.Resolver) error { return r.CheckRoot() }

// Plan evaluates every job without writing anything.
func (e *Engine) Plan(ctx context.Context, r destination.Resolver, jobs []Job, workers int, onDone func(done, total int)) []destination.Check {
	if workers < 1 {
		workers = 2
	}
	out := make([]destination.Check, len(jobs))
	defer func() { noteBatchDuplicates(out) }()
	idx := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	done := 0
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				j := jobs[i]
				out[i] = e.checker.Check(ctx, r, destination.Source{Path: j.SourcePath, Name: j.Name, Vars: j.Vars}, []tags.Tag{j.Tag})[0]
				if onDone != nil {
					mu.Lock()
					done++
					onDone(done, len(jobs))
					mu.Unlock()
				}
			}
		}()
	}
	for i := range jobs {
		if ctx.Err() != nil {
			break
		}
		idx <- i
	}
	close(idx)
	wg.Wait()
	return out
}

// noteBatchDuplicates flags READY jobs whose destination is also the
// destination of an earlier job of the same batch: such jobs look fine on
// their own, but only one of the files can end up there.
func noteBatchDuplicates(checks []destination.Check) {
	seen := map[string]bool{}
	seenFold := map[string]bool{} // destinations that differ only in letter case
	for i, c := range checks {
		if c.Path == "" {
			continue
		}
		fold := strings.ToLower(c.Path)
		if c.Status == destination.StatusReady && (seen[c.Path] || seenFold[fold]) {
			note := "another file in this batch has the same destination"
			if !seen[c.Path] {
				note = "another file in this batch has a destination that differs only in letter case (the same file on case-insensitive filesystems such as FAT/exFAT/NTFS)"
			}
			if c.Detail != "" {
				note = c.Detail + "; " + note
			}
			checks[i].Detail = note
		}
		seen[c.Path] = true
		seenFold[fold] = true
	}
}

// batchPaths records the destinations written during one Run, so that
// PolicyReplace never overwrites a file this same batch just wrote.
type batchPaths struct {
	mu    sync.Mutex
	paths map[string]bool
}

// wrote reports whether this batch already wrote dest, directly or under a
// name that differs only in letter case and is the same file on disk (a
// case-insensitive destination filesystem). The caller holds bp.mu.
func (bp *batchPaths) wrote(dest string) bool {
	if bp.paths[dest] {
		return true
	}
	var destInfo os.FileInfo
	for p := range bp.paths {
		if !strings.EqualFold(p, dest) {
			continue
		}
		if destInfo == nil {
			var err error
			if destInfo, err = os.Stat(dest); err != nil {
				return false
			}
		}
		if pi, err := os.Stat(p); err == nil && os.SameFile(pi, destInfo) {
			return true
		}
	}
	return false
}

// Run executes jobs with a bounded worker pool. Errors of single jobs never
// stop the batch. Cancelling ctx aborts running copies (their temporary
// files are removed) and marks unstarted jobs CANCELLED. Running the same
// jobs again resumes: finished destinations are recognised by content.
func (e *Engine) Run(ctx context.Context, r destination.Resolver, jobs []Job, opts Options) (Summary, error) {
	if opts.Policy == PolicyReplace && !opts.ConfirmReplace {
		return Summary{}, ErrReplaceNotConfirmed
	}
	if err := preflight(r); err != nil {
		return Summary{}, err
	}
	workers := opts.Workers
	if workers < 1 {
		workers = 2
	}
	sum := Summary{Results: make([]Result, len(jobs))}
	bp := &batchPaths{paths: map[string]bool{}}
	var emu sync.Mutex
	done := 0
	emit := func(ev Event) {
		if opts.OnEvent == nil {
			return
		}
		emu.Lock()
		defer emu.Unlock()
		ev.Total, ev.Done = len(jobs), done
		opts.OnEvent(ev)
	}

	idx := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				job := jobs[i]
				emit(Event{Kind: EventStart, Index: i, Job: job})
				res := e.runJob(ctx, r, bp, job, opts, func(done, total int64) {
					emit(Event{Kind: EventBytes, Index: i, Job: job, Bytes: done, BytesTotal: total})
				})
				sum.Results[i] = res
				emu.Lock()
				done++
				emu.Unlock()
				emit(Event{Kind: EventDone, Index: i, Job: job, Result: res})
			}
		}()
	}
	for i := range jobs {
		if ctx.Err() != nil {
			sum.Results[i] = Result{Job: jobs[i], Status: StatusCancelled, Detail: "not started"}
			continue
		}
		idx <- i
	}
	close(idx)
	wg.Wait()

	for _, r := range sum.Results {
		switch r.Status {
		case StatusCopied:
			sum.Copied++
		case StatusReplaced:
			sum.Replaced++
		case StatusRenamed:
			sum.Renamed++
		case StatusAlreadyCopied:
			sum.AlreadyCopied++
		case StatusConflictSkip:
			sum.ConflictsSkipped++
		case StatusInvalid:
			sum.Invalid++
		case StatusFailed:
			sum.Failed++
		case StatusCancelled:
			sum.Cancelled++
		}
		sum.Bytes += r.Bytes
	}
	return sum, nil
}

func (e *Engine) runJob(ctx context.Context, r destination.Resolver, bp *batchPaths, job Job, opts Options, progress func(done, total int64)) Result {
	res := Result{Job: job}
	if ctx.Err() != nil {
		res.Status, res.Detail = StatusCancelled, "cancelled"
		return res
	}
	chk := e.checker.Check(ctx, r, destination.Source{Path: job.SourcePath, Name: job.Name, Vars: job.Vars}, []tags.Tag{job.Tag})[0]
	res.DestPath, res.SrcHash, res.DstHash, res.Detail = chk.Path, chk.SrcHash, chk.DstHash, chk.Detail

	switch chk.Status {
	case destination.StatusAlreadyCopied:
		res.Status = StatusAlreadyCopied
		return res
	case destination.StatusUnresolvedVar, destination.StatusInvalidPath:
		res.Status = StatusInvalid
		return res
	case destination.StatusError:
		res.Status = StatusFailed
		if ctx.Err() != nil {
			res.Status = StatusCancelled
		}
		return res
	case destination.StatusConflict:
		if opts.Policy == PolicySkip {
			res.Status = StatusConflictSkip
			return res
		}
	}
	conflict := chk.Status == destination.StatusConflict
	return e.write(ctx, r, bp, job, res, conflict, opts.Policy, progress)
}

// write performs the actual copy (with history) for a job whose destination
// is READY, or in conflict with a policy that allows writing.
func (e *Engine) write(ctx context.Context, r destination.Resolver, bp *batchPaths, job Job, res Result, conflict bool, policy ConflictPolicy, progress func(done, total int64)) Result {
	var histID int64
	haveHist := false
	if e.hist != nil {
		if id, err := e.hist.Begin(job, res.DestPath); err == nil {
			histID, haveHist = id, true
		} else {
			res.Detail = "history unavailable: " + err.Error()
		}
	}
	finish := func(r Result) Result {
		if haveHist {
			if err := e.hist.Finish(histID, r); err != nil && r.Detail == "" {
				r.Detail = "history not updated: " + err.Error()
			}
		}
		return r
	}
	fail := func(err error) Result {
		res.Status, res.Detail = StatusFailed, err.Error()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			res.Status, res.Detail = StatusCancelled, "cancelled; temporary file removed"
		}
		return finish(res)
	}

	// 1. the source must still be what the scan saw
	st, err := os.Stat(job.SourcePath)
	switch {
	case err != nil:
		return fail(fmt.Errorf("source: %w", err))
	case !st.Mode().IsRegular():
		return fail(errors.New("source is no longer a regular file"))
	case !job.ModTime.IsZero() && (st.Size() != job.Size || !st.ModTime().Equal(job.ModTime)):
		return fail(errors.New("source changed since it was scanned; refresh the directory"))
	}

	dest := res.DestPath
	overwrite := conflict && policy == PolicyReplace
	rename := conflict && policy == PolicyRename
	if rename { // reuse an identical alternative if a previous run created one
		alt, identical, err := e.pickAlternative(ctx, job.SourcePath, dest)
		if err != nil {
			return fail(err)
		}
		if identical {
			res.Status, res.DestPath, res.Detail = StatusAlreadyCopied, alt, "identical copy exists under an alternative name"
			return finish(res)
		}
		dest = alt
	}

	// 2. directories below the (existing) root
	dir := filepath.Dir(dest)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fail(err)
	}

	// 3. copy to a temporary file next to the destination, verified
	var note func(string)
	if haveHist {
		note = func(tmp string) { _ = e.hist.Note(histID, tmp) }
	}
	c, err := copyToTemp(ctx, job.SourcePath, dir, progress, note)
	if err != nil {
		return fail(err)
	}
	res.SrcHash, res.DstHash, res.Bytes = c.hash, c.hash, c.n

	// 4. publish atomically; never overwrite unless explicitly allowed
	for tries := 0; ; tries++ {
		// Publish and record under one lock, so a concurrent job that finds
		// the file just written cannot overwrite it before it is recorded.
		bp.mu.Lock()
		if overwrite && bp.wrote(dest) {
			bp.mu.Unlock()
			os.Remove(c.tmp)
			res.Status, res.Bytes, res.Detail = StatusConflictSkip, 0, "another file in this batch was already written to this destination; not replaced"
			return finish(res)
		}
		if overwrite {
			err = os.Rename(c.tmp, dest)
		} else {
			err = renameNoReplace(c.tmp, dest)
		}
		if err == nil {
			bp.paths[dest] = true
		}
		bp.mu.Unlock()
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) || tries > 20 {
			os.Remove(c.tmp)
			return fail(err)
		}
		// someone created the destination while we were copying
		if rename {
			var identical bool
			dest, identical, err = e.pickAlternative(ctx, job.SourcePath, res.DestPath)
			if err != nil {
				os.Remove(c.tmp)
				return fail(err)
			}
			if identical {
				os.Remove(c.tmp)
				res.Status, res.DestPath, res.Bytes = StatusAlreadyCopied, dest, 0
				return finish(res)
			}
			continue
		}
		cmp := e.checker.Compare(ctx, job.SourcePath, dest)
		if cmp.Status == destination.StatusAlreadyCopied {
			os.Remove(c.tmp)
			res.Status, res.Bytes, res.Detail = StatusAlreadyCopied, 0, "appeared during copy"
			return finish(res)
		}
		switch policy {
		case PolicyRename:
			rename, conflict = true, true
			if dest, _, err = e.pickAlternative(ctx, job.SourcePath, res.DestPath); err != nil {
				os.Remove(c.tmp)
				return fail(err)
			}
		case PolicyReplace:
			overwrite, conflict = true, true
		default:
			os.Remove(c.tmp)
			res.Status, res.Bytes, res.Detail = StatusConflictSkip, 0, "a different file appeared during copy"
			return finish(res)
		}
	}
	syncDir(dir)

	res.DestPath = dest
	switch {
	case overwrite && conflict:
		res.Status = StatusReplaced
	case rename && conflict:
		res.Status = StatusRenamed
	default:
		res.Status = StatusCopied
	}
	if !strings.HasPrefix(res.Detail, "history unavailable") {
		res.Detail = "" // success: drop the pre-check remarks
	}
	return finish(res)
}

// pickAlternative returns the first free "name (n).ext", or the existing
// alternative whose content already equals the source.
func (e *Engine) pickAlternative(ctx context.Context, src, dest string) (path string, identical bool, err error) {
	for n := 1; n < 10000; n++ {
		cand := alternativeName(dest, n)
		if _, serr := os.Lstat(cand); errors.Is(serr, fs.ErrNotExist) {
			return cand, false, nil
		} else if serr != nil {
			return "", false, serr
		}
		switch c := e.checker.Compare(ctx, src, cand); c.Status {
		case destination.StatusAlreadyCopied:
			return cand, true, nil
		case destination.StatusError:
			return "", false, errors.New(c.Detail)
		}
	}
	return "", false, errors.New("no free alternative file name")
}
