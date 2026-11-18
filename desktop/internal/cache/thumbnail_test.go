package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func solid(w, h int, c color.Color) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestLoadGeneratesOnceThenServesFromDisk(t *testing.T) {
	c := NewThumbnails(filepath.Join(t.TempDir(), "thumbs"), 1<<20)
	mod := time.Unix(1700000000, 0)
	calls := 0
	gen := func() (image.Image, error) { calls++; return solid(40, 30, color.RGBA{200, 0, 0, 255}), nil }

	img, cached, err := c.Load("/p/a.jpg", 10, mod, gen)
	if err != nil || cached || calls != 1 || img.Bounds().Dx() != 40 {
		t.Fatal(cached, calls, err)
	}
	img, cached, err = c.Load("/p/a.jpg", 10, mod, gen)
	if err != nil || !cached || calls != 1 {
		t.Fatal(cached, calls, err)
	}
	r, _, _, _ := img.At(5, 5).RGBA()
	if r>>8 < 150 {
		t.Fatal("colour lost")
	}
	// a modified file (size or mtime) gets a new entry
	if _, cached, _ := c.Load("/p/a.jpg", 11, mod, gen); cached {
		t.Fatal("size change served stale thumbnail")
	}
	if _, cached, _ := c.Load("/p/a.jpg", 10, mod.Add(time.Second), gen); cached {
		t.Fatal("mtime change served stale thumbnail")
	}
	if calls != 3 {
		t.Fatal(calls)
	}
}

func TestGenerationErrorAndCorruptEntry(t *testing.T) {
	c := NewThumbnails(t.TempDir(), 1<<20)
	mod := time.Unix(1, 0)
	if _, _, err := c.Load("/x", 1, mod, func() (image.Image, error) { return nil, errors.New("boom") }); err == nil {
		t.Fatal("expected error")
	}
	if _, ok := c.Get("/x", 1, mod); ok {
		t.Fatal("failed generation must not be cached")
	}
	// corrupt the stored file: it is discarded and regenerated
	c.Put("/y", 1, mod, solid(8, 8, color.White))
	os.WriteFile(c.file("/y", 1, mod), []byte("garbage"), 0o644)
	if _, ok := c.Get("/y", 1, mod); ok {
		t.Fatal("corrupt entry served")
	}
	if _, err := os.Stat(c.file("/y", 1, mod)); err == nil {
		t.Fatal("corrupt entry not removed")
	}
	// unwritable cache location: Load still returns the generated image
	bad := NewThumbnails(filepath.Join(t.TempDir(), "f", "sub"), 1<<20)
	os.WriteFile(filepath.Join(filepath.Dir(bad.dir), "..", "f"), nil, 0o644)
	img, _, err := bad.Load("/z", 1, mod, func() (image.Image, error) { return solid(4, 4, color.White), nil })
	if err != nil || img == nil {
		t.Fatal(err)
	}
}

func TestTransparencyIsFlattened(t *testing.T) {
	c := NewThumbnails(t.TempDir(), 1<<20)
	mod := time.Unix(1, 0)
	c.Put("/t", 1, mod, solid(8, 8, color.NRGBA{0, 0, 0, 0})) // fully transparent
	img, ok := c.Get("/t", 1, mod)
	if !ok {
		t.Fatal("miss")
	}
	if r, g, b, _ := img.At(1, 1).RGBA(); r>>8 < 240 || g>>8 < 240 || b>>8 < 240 {
		t.Fatalf("transparent area not white: %v %v %v", r, g, b)
	}
}

func TestPruneRemovesLeastRecentlyUsed(t *testing.T) {
	c := NewThumbnails(t.TempDir(), 1) // absurdly small: everything is over the limit
	base := time.Unix(1700000000, 0)
	for i := 0; i < 5; i++ {
		c.Put("/p", int64(i), base, solid(64, 64, color.RGBA{uint8(i * 40), 9, 9, 255}))
	}
	// make entry 4 the most recently used
	os.Chtimes(c.file("/p", 4, base), time.Now().Add(time.Hour), time.Now().Add(time.Hour))
	n, err := c.Prune()
	if err != nil || n == 0 {
		t.Fatal(n, err)
	}
	c2 := NewThumbnails(c.dir, 1<<30)
	if n, _ := c2.Prune(); n != 0 {
		t.Fatal("prune below the limit must do nothing")
	}
	if err := c2.Clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.dir); err == nil {
		t.Fatal("Clear left the directory")
	}
}

func TestPutHandlesImagesWithNonZeroOrigin(t *testing.T) {
	c := NewThumbnails(t.TempDir(), 1<<30)
	img := image.NewRGBA(image.Rect(10, 20, 74, 84)) // 64x64 but not at (0,0)
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{200, 10, 10, 255}), image.Point{}, draw.Src)
	mod := time.Unix(1700000000, 0)
	if err := c.Put("/p", 1, mod, img); err != nil {
		t.Fatal(err)
	}
	got, ok := c.Get("/p", 1, mod)
	if !ok || got.Bounds().Dx() != 64 || got.Bounds().Dy() != 64 {
		t.Fatalf("ok=%v bounds=%v", ok, got)
	}
	if r, _, _, _ := got.At(32, 32).RGBA(); r>>8 < 150 {
		t.Fatalf("content lost: red=%d", r>>8)
	}
}

func TestPutAfterClearRecreatesTheDirectory(t *testing.T) {
	c := NewThumbnails(filepath.Join(t.TempDir(), "thumbs"), 1<<30)
	mod := time.Unix(1700000000, 0)
	c.Put("/p", 1, mod, solid(8, 8, color.White))
	if err := c.Clear(); err != nil {
		t.Fatal(err)
	}
	if err := c.Put("/p", 2, mod, solid(8, 8, color.White)); err != nil {
		t.Fatalf("Put after Clear: %v", err)
	}
	if _, ok := c.Get("/p", 2, mod); !ok {
		t.Fatal("entry missing after Clear + Put")
	}
}

func TestPruneAlsoRemovesLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	c := NewThumbnails(dir, 100)
	stray := filepath.Join(dir, "ab", ".thumb-123.tmp") // left by a crash during Put
	os.MkdirAll(filepath.Dir(stray), 0o755)
	os.WriteFile(stray, make([]byte, 1000), 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(stray, old, old)
	if n, err := c.Prune(); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := os.Stat(stray); err == nil {
		t.Fatal("stray temp file survived Prune")
	}
}

func countFiles(dir string) int {
	n := 0
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return nil
	})
	return n
}

// Put prunes in the background, so a long session cannot outgrow the limit.
func TestPutPrunesPeriodically(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "thumbs")
	c := NewThumbnails(dir, 1) // far below one thumbnail
	for i := 0; i < pruneEvery; i++ {
		if err := c.Put("/p/a.jpg", int64(i), time.Unix(1700000000, 0), solid(8, 8, color.Black)); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for countFiles(dir) >= pruneEvery && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := countFiles(dir); n >= pruneEvery {
		t.Errorf("cache was not pruned: %d files", n)
	}
}

// Run with -race: SetLimit, Prune and Put/Get may overlap.
func TestConcurrentSetLimitPruneAndPut(t *testing.T) {
	c := NewThumbnails(filepath.Join(t.TempDir(), "thumbs"), 1<<20)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				c.SetLimit(int64(1+i%3) << 10)
				c.Put("/p/x.jpg", int64(g*100+i), time.Unix(1700000000, 0), solid(8, 8, color.Black))
				c.Get("/p/x.jpg", int64(g*100+i), time.Unix(1700000000, 0))
				c.Prune()
			}
		}(g)
	}
	wg.Wait()
}

func TestCacheDirsArePrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "thumbs")
	c := NewThumbnails(dir, 1<<20)
	if err := c.Put("/p/a.jpg", 1, time.Unix(1700000000, 0), solid(8, 8, color.Black)); err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			if st, _ := os.Stat(p); st.Mode().Perm()&0o077 != 0 {
				t.Errorf("%s is accessible to others: %v", p, st.Mode().Perm())
			}
		}
		return nil
	})
}

func TestKeyIncludesFormatVersion(t *testing.T) {
	c := NewThumbnails(t.TempDir(), 1<<20)
	mod := time.Unix(1700000000, 0)
	want := c.file("/p/a.jpg", 5, mod)
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", "/p/a.jpg", 5, mod.UnixNano())))
	h := hex.EncodeToString(sum[:])
	if old := filepath.Join(c.dir, h[:2], h[2:]+".jpg"); old == want {
		t.Error("the key must differ from the unversioned key so old thumbnails are not reused")
	}
}
