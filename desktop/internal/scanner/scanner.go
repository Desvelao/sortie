// Package scanner discovers regular files in a directory. It is strictly
// read-only and never modifies or deletes source files.
package scanner

import (
	"context"
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"sortie/internal/fileinfo"
)

// Options configures a scan.
type Options struct {
	Recursive bool // default false
	// SkipHidden leaves out files whose name starts with "." and, when
	// scanning recursively, everything below hidden directories.
	SkipHidden bool
	// Exclude lists path.Match patterns tested against the file name (e.g.
	// "*.part", "*.crdownload"); matching files are not listed.
	Exclude []string
}

// The copier names its in-progress files ".sortie-*.tmp"; leftovers of an
// interrupted copy must never be offered for sorting (kept in sync with
// copier.IsTempFile; the scanner must not depend on the copier).
func isCopierTemp(name string) bool {
	return strings.HasPrefix(name, ".sortie-") && strings.HasSuffix(name, ".tmp")
}

func excluded(name string, patterns []string) bool {
	for _, pat := range patterns {
		if ok, err := path.Match(pat, name); err == nil && ok {
			return true
		}
	}
	return false
}

// Result holds scan output.
type Result struct {
	Files   []fileinfo.FileInfo
	Skipped []string // paths that could not be accessed
}

// Scan lists regular files under root. Symlinks and non-regular files are
// skipped; inaccessible entries are recorded in Result.Skipped, not fatal.
func Scan(ctx context.Context, root string, opts Options) (Result, error) {
	var res Result
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return res, err
	}
	if _, err := fs.Stat(osFS{}, absRoot); err != nil {
		return res, err
	}

	// WalkDir lstat's its root, so a root that is a symlink to a directory
	// would be skipped as "not a regular file". A trailing separator makes the
	// OS follow it, while file paths keep the root as the user chose it.
	walkRoot := absRoot
	if !strings.HasSuffix(walkRoot, string(filepath.Separator)) {
		walkRoot += string(filepath.Separator)
	}
	err = filepath.WalkDir(walkRoot, func(path string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if path == walkRoot {
				return err
			}
			res.Skipped = append(res.Skipped, path)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if path != walkRoot && (!opts.Recursive || (opts.SkipHidden && strings.HasPrefix(d.Name(), "."))) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() { // symlinks, devices, sockets...
			return nil
		}
		if n := d.Name(); isCopierTemp(n) || (opts.SkipHidden && strings.HasPrefix(n, ".")) || excluded(n, opts.Exclude) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			res.Skipped = append(res.Skipped, path)
			return nil
		}
		name := d.Name()
		res.Files = append(res.Files, fileinfo.FileInfo{
			Path:    path,
			Name:    name,
			Ext:     fileinfo.Extension(name),
			Size:    info.Size(),
			ModTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		return res, err
	}
	if err := detectMIMEs(ctx, res.Files); err != nil {
		return Result{}, err // workers may still touch res.Files: do not hand it out
	}
	sort.SliceStable(res.Files, func(i, j int) bool {
		a, b := res.Files[i].Path, res.Files[j].Path
		la, lb := strings.ToLower(a), strings.ToLower(b)
		if la != lb {
			return la < lb
		}
		return a < b
	})
	return res, nil
}

// mimeWorkers bounds the concurrent content sniffs; reading the first bytes of
// many files is latency-bound (network shares, spinning disks), not CPU-bound.
const mimeWorkers = 8

// detectMIMEs fills MIMEType of every file using a bounded worker pool. It
// returns ctx's error as soon as ctx is cancelled, even if a sniff is stuck
// on a stalled mount (that worker is abandoned and ends when the call returns).
func detectMIMEs(ctx context.Context, files []fileinfo.FileInfo) error {
	idx := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < mimeWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range idx {
				if ctx.Err() != nil {
					continue // drain
				}
				files[i].MIMEType = fileinfo.DetectMIME(files[i].Path)
			}
		}()
	}
	go func() {
		defer close(idx)
		for i := range files {
			select {
			case idx <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}
