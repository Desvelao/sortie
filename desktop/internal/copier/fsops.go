package copier

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const copyChunk = 1 << 20

// tempPrefix marks in-progress files; they are never cleaned up automatically
// after a crash (the history entry names the leftover file). IsTempFile and
// RemoveTempFile let the application find and, when the user agrees, remove them.
const tempPrefix = ".sortie-"

// IsTempFile reports whether path is named like one of our temporary files.
func IsTempFile(path string) bool {
	b := filepath.Base(path)
	return strings.HasPrefix(b, tempPrefix) && strings.HasSuffix(b, ".tmp")
}

// RemoveTempFile deletes a leftover temporary file. It refuses anything that
// is not a regular file named like our temporary files, and reports whether a
// file was removed (false, nil if it no longer exists).
func RemoveTempFile(path string) (bool, error) {
	if !IsTempFile(path) {
		return false, fmt.Errorf("%s is not a sortie temporary file", path)
	}
	st, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() {
		return false, fmt.Errorf("%s is not a regular file", path)
	}
	return true, os.Remove(path)
}

// ctxReader aborts reads once ctx is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func hashFile(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.CopyBuffer(h, ctxReader{ctx, f}, make([]byte, copyChunk)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type copied struct {
	tmp  string // path of the finished temporary file
	hash string // SHA-256 of the source as read
	n    int64
}

// copyToTemp copies src into a new temporary file inside dir, verifies it by
// reading it back, and applies the source's permissions and modification
// time. The source is opened read-only. On any error the temporary file is
// removed. progress (optional) receives bytes copied so far.
func copyToTemp(ctx context.Context, src, dir string, progress func(done, total int64), note func(tmp string)) (res copied, err error) {
	in, err := os.Open(src)
	if err != nil {
		return res, err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return res, err
	}
	if !st.Mode().IsRegular() {
		return res, errors.New("source is not a regular file")
	}
	out, err := os.CreateTemp(dir, tempPrefix+"*.tmp")
	if err != nil {
		return res, err
	}
	tmp := out.Name()
	if note != nil {
		note(tmp)
	}
	defer func() {
		if err != nil {
			out.Close()
			os.Remove(tmp)
		}
	}()

	h := sha256.New()
	buf := make([]byte, copyChunk)
	var n int64
	last := time.Now()
	for {
		if err = ctx.Err(); err != nil {
			return res, err
		}
		k, rerr := in.Read(buf)
		if k > 0 {
			if _, err = out.Write(buf[:k]); err != nil {
				return res, err
			}
			h.Write(buf[:k])
			n += int64(k)
			if progress != nil && time.Since(last) > 150*time.Millisecond {
				progress(n, st.Size())
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return res, rerr
		}
	}
	if n != st.Size() {
		return res, fmt.Errorf("source changed while copying (read %d of %d bytes)", n, st.Size())
	}
	if err = out.Sync(); err != nil {
		return res, err
	}
	// Drop the (now clean) cached pages so the read-back below comes from the
	// device rather than from the page cache. Best effort: it is only a hint.
	_ = unix.Fadvise(int(out.Fd()), 0, 0, unix.FADV_DONTNEED)
	if err = out.Close(); err != nil {
		return res, err
	}
	// the source must not have changed during the copy
	after, serr := os.Stat(src)
	if serr != nil || after.Size() != st.Size() || !after.ModTime().Equal(st.ModTime()) {
		err = errors.New("source changed while copying")
		return res, err
	}
	// read the copy back to verify what actually reached the disk (the page
	// cache was dropped above where the kernel honours it)
	sum := hex.EncodeToString(h.Sum(nil))
	back, herr := hashFile(ctx, tmp)
	if herr != nil {
		err = herr
		return res, err
	}
	if back != sum {
		err = errors.New("verification failed: the copy differs from the source")
		return res, err
	}
	// Permissions and times are best effort: FAT/exFAT/NTFS mounts commonly
	// refuse chmod/utimes (EPERM/ENOTSUP) although the data was copied fine.
	if err = tolerateUnsupported(chmodFn(tmp, st.Mode().Perm()|0o600)); err != nil {
		return res, err
	}
	if err = tolerateUnsupported(chtimesFn(tmp, st.ModTime(), st.ModTime())); err != nil {
		return res, err
	}
	return copied{tmp: tmp, hash: sum, n: n}, nil
}

// chmodFn and chtimesFn are variables so tests can simulate filesystems that
// refuse them.
var (
	chmodFn   = os.Chmod
	chtimesFn = os.Chtimes
)

// tolerateUnsupported turns "this filesystem does not support that" errors
// into nil and passes every other error through.
func tolerateUnsupported(err error) error {
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
		return nil
	}
	return err
}

// renameNoReplace atomically moves src to dst and fails with fs.ErrExist
// (leaving both untouched) if dst already exists, so a destination file can
// never be overwritten silently — not even by a concurrent writer.
func renameNoReplace(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		return fs.ErrExist
	case !errors.Is(err, unix.EINVAL) && !errors.Is(err, unix.ENOSYS) && !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EOPNOTSUPP):
		return err
	}
	// filesystem without RENAME_NOREPLACE: hard link fails if dst exists
	if lerr := os.Link(src, dst); lerr == nil {
		return os.Remove(src)
	} else if errors.Is(lerr, fs.ErrExist) {
		return fs.ErrExist
	}
	// last resort (e.g. FAT): check, then rename (tiny race window)
	if _, serr := os.Lstat(dst); serr == nil {
		return fs.ErrExist
	}
	return os.Rename(src, dst)
}

// alternativeName returns "name (n).ext" for n >= start.
func alternativeName(path string, n int) string {
	dir, base := filepath.Split(path)
	ext := filepath.Ext(base)
	if ext == base { // dotfile such as ".bashrc": no extension
		ext = ""
	}
	stem := strings.TrimSuffix(base, ext)
	if strings.HasSuffix(stem, ".tar") && len(stem) > len(".tar") { // keep .tar.gz, .tar.xz, ... together
		ext = ".tar" + ext
		stem = strings.TrimSuffix(stem, ".tar")
	}
	return filepath.Join(dir, fmt.Sprintf("%s (%d)%s", stem, n, ext))
}

// syncDir flushes directory metadata (best effort).
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
}
