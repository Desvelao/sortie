// Package cache stores generated thumbnails as ordinary files in the user's
// cache directory. Entries are keyed by path, size, modification time and a format version, so
// a changed file gets a new thumbnail; everything can be regenerated, so the
// cache may be deleted at any time.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"
)

// DefaultDir returns $XDG_CACHE_HOME/sortie/thumbnails
// (~/.cache/sortie/thumbnails).
func DefaultDir() (string, error) {
	d := os.Getenv("XDG_CACHE_HOME")
	if d == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		d = filepath.Join(h, ".cache")
	}
	return filepath.Join(d, "sortie", "thumbnails"), nil
}

// Thumbnails is a size-limited on-disk thumbnail cache.
type Thumbnails struct {
	dir      string
	maxBytes atomic.Int64 // read by Prune, written by SetLimit from the UI goroutine
	puts     atomic.Int64
	pruning  atomic.Bool
}

// pruneEvery is how many Put calls pass between background prunes, so the
// cache cannot grow far beyond its limit during a long session.
const pruneEvery = 100

// NewThumbnails creates a cache in dir limited to roughly maxBytes (see Prune).
func NewThumbnails(dir string, maxBytes int64) *Thumbnails {
	t := &Thumbnails{dir: dir}
	t.maxBytes.Store(maxBytes)
	return t
}

// formatVersion is part of every cache key. Bump it whenever the way thumbnails
// are generated changes (dimensions, orientation handling, JPEG quality), so
// stale entries are no longer served; they age out through Prune.
const formatVersion = 1

func (t *Thumbnails) file(path string, size int64, mod time.Time) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("v%d|%s|%d|%d", formatVersion, path, size, mod.UnixNano())))
	h := hex.EncodeToString(sum[:])
	return filepath.Join(t.dir, h[:2], h[2:]+".jpg")
}

// Get returns a cached thumbnail. A corrupt entry is removed and reported as a miss.
func (t *Thumbnails) Get(path string, size int64, mod time.Time) (image.Image, bool) {
	p := t.file(path, size, mod)
	f, err := os.Open(p)
	if err != nil {
		return nil, false
	}
	img, err := jpeg.Decode(f)
	f.Close()
	if err != nil {
		os.Remove(p)
		return nil, false
	}
	now := time.Now()
	os.Chtimes(p, now, now) // recently used entries survive pruning
	return img, true
}

// Put stores a thumbnail (JPEG, transparency flattened onto white). Writes
// are atomic, so readers never see partial files.
func (t *Thumbnails) Put(path string, size int64, mod time.Time, img image.Image) error {
	p := t.file(path, size, mod)
	// Thumbnails are previews of private images: keep the cache private.
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	_ = os.Chmod(t.dir, 0o700) // keep the directory private
	b := img.Bounds()
	flat := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), img, b.Min, draw.Over)
	tmp, err := os.CreateTemp(filepath.Dir(p), ".thumb-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := jpeg.Encode(tmp, flat, &jpeg.Options{Quality: 80}); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return err
	}
	if t.puts.Add(1)%pruneEvery == 0 && t.pruning.CompareAndSwap(false, true) {
		go func() {
			defer t.pruning.Store(false)
			t.Prune()
		}()
	}
	return nil
}

// Load returns the cached thumbnail or generates, stores and returns it.
// fromCache tells which happened. A failing cache write is not an error.
func (t *Thumbnails) Load(path string, size int64, mod time.Time, generate func() (image.Image, error)) (img image.Image, fromCache bool, err error) {
	if img, ok := t.Get(path, size, mod); ok {
		return img, true, nil
	}
	img, err = generate()
	if err != nil {
		return nil, false, err
	}
	_ = t.Put(path, size, mod, img)
	return img, false, nil
}

// Prune deletes the least recently used entries until the cache is at most
// 80% of its size limit; it returns how many files were removed.
func (t *Thumbnails) Prune() (int, error) {
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var all []entry
	var total int64
	err := filepath.WalkDir(t.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			all = append(all, entry{p, info.Size(), info.ModTime()})
			total += info.Size()
		}
		return nil
	})
	limit := t.maxBytes.Load()
	if err != nil || total <= limit {
		return 0, err
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.Before(all[j].mod) })
	target := limit * 8 / 10
	removed := 0
	for _, e := range all {
		if total <= target {
			break
		}
		if os.Remove(e.path) == nil {
			total -= e.size
			removed++
		}
	}
	return removed, nil
}

// Clear removes every cached thumbnail.
func (t *Thumbnails) Clear() error { return os.RemoveAll(t.dir) }

// SetLimit changes the size limit used by Prune. It is safe to call while a
// Prune runs; call Prune afterwards to enforce a lowered limit right away.
func (t *Thumbnails) SetLimit(maxBytes int64) { t.maxBytes.Store(maxBytes) }
