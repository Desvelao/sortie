package preview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
)

func write(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		img.Set(x, 0, color.RGBA{255, 0, 0, 255})
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

func TestImagePreviewDownscales(t *testing.T) {
	p := write(t, "big.png", pngBytes(4000, 1000))
	pv := Default().Create(p, "image/png", ".png")
	if pv.Kind != KindImage || pv.Image == nil {
		t.Fatalf("%+v", pv)
	}
	if b := pv.Image.Bounds(); b.Dx() != 1600 || b.Dy() != 400 {
		t.Fatalf("size %v", b)
	}
	if pv.Fields[1].Value != "4000 × 1000 px" {
		t.Fatalf("%v", pv.Fields)
	}
}

func TestOrient(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	out := orient(img, 6) // rotate 90° CW: top-left ends up top-right, size swaps
	if b := out.Bounds(); b.Dx() != 2 || b.Dy() != 4 {
		t.Fatal(b)
	}
	if r, _, _, _ := out.At(1, 0).RGBA(); r == 0 {
		t.Fatal("pixel not moved to top-right")
	}
	if orient(img, 1) != image.Image(img) {
		t.Fatal("orientation 1 must be a no-op")
	}
}

func TestCorruptAndMisleadingImagesDegrade(t *testing.T) {
	r := Default()
	pv := r.Create(write(t, "bad.png", []byte("not a png")), "image/png", ".png")
	if pv.Kind != KindGeneric || len(pv.Notes) == 0 || !strings.Contains(pv.Notes[0], "Preview unavailable") {
		t.Fatalf("%+v", pv)
	}
	// text pretending to be .jpg: sniffed as text/plain → shown as text
	pv = r.Create(write(t, "fake.jpg", []byte("hello world")), "text/plain", ".jpg")
	if pv.Kind != KindText || pv.Text != "hello world" {
		t.Fatalf("%+v", pv)
	}
}

func TestSVGIsPassedThrough(t *testing.T) {
	p := write(t, "a.svg", []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"))
	if pv := Default().Create(p, "image/svg+xml", ".svg"); pv.Kind != KindSVG || pv.Path != p {
		t.Fatalf("%+v", pv)
	}
}

func TestTextPreview(t *testing.T) {
	r := Default()
	big := strings.Repeat("héllo wörld\n", 20000) // > 64 KiB, multi-byte
	pv := r.Create(write(t, "big.txt", []byte(big)), "text/plain", ".txt")
	if pv.Kind != KindText || !pv.Truncated || len(pv.Text) > maxTextBytes {
		t.Fatalf("kind=%s trunc=%v len=%d", pv.Kind, pv.Truncated, len(pv.Text))
	}
	for _, c := range pv.Text {
		if c == '�' {
			t.Fatal("split multi-byte rune")
		}
	}
	pv = r.Create(write(t, "bin.txt", []byte("abc\x00\x01\x02")), "text/plain", ".txt")
	if pv.Kind != KindGeneric {
		t.Fatalf("binary data shown as text: %+v", pv)
	}
	if pv := r.Create(write(t, "empty.md", nil), "", ".md"); pv.Kind != KindText {
		t.Fatalf("%+v", pv)
	}
}

func TestUnknownTypeIsGenericWithoutNotes(t *testing.T) {
	pv := Default().Create(write(t, "data.xyz", []byte{1, 2, 3}), "application/octet-stream", ".xyz")
	if pv.Kind != KindGeneric || len(pv.Notes) != 0 {
		t.Fatalf("%+v", pv)
	}
}

func TestAudioPreviewWAV(t *testing.T) {
	// 1 s mono 8 kHz WAV
	data := make([]byte, 16000)
	hdr := []byte("RIFF\x00\x00\x00\x00WAVEfmt \x10\x00\x00\x00\x01\x00\x01\x00\x40\x1f\x00\x00\x80\x3e\x00\x00\x02\x00\x10\x00data\x80\x3e\x00\x00")
	pv := Default().Create(write(t, "s.wav", append(hdr, data...)), "audio/wav", ".wav")
	if pv.Kind != KindAudio || !pv.Playable {
		t.Fatalf("%+v", pv)
	}
	found := false
	for _, f := range pv.Fields {
		if f.Label == "Duration" && f.Value == "0:01" {
			found = true
		}
	}
	if !found {
		t.Fatalf("%v", pv.Fields)
	}
	pv = Default().Create(write(t, "x.m4a", []byte("junk")), "audio/mp4", ".m4a")
	if pv.Kind == KindAudio && pv.Playable {
		t.Fatal("m4a must not claim playback")
	}
}

func TestVideoWithBrokenContainerStillPreviews(t *testing.T) {
	pv := Default().Create(write(t, "v.mkv", []byte("garbage")), "video/x-matroska", ".mkv")
	if pv.Kind != KindVideo || len(pv.Notes) == 0 {
		t.Fatalf("%+v", pv)
	}
}

func TestUnreadableAudioStillPreviews(t *testing.T) {
	// raw ADTS .aac is not an MP4 container and carries no tags
	adts := []byte{0xFF, 0xF1, 0x50, 0x80, 0x02, 0x1F, 0xFC, 0x00, 0x00, 0x00, 0x00, 0x00}
	pv := Default().Create(write(t, "raw.aac", adts), "audio/aac", ".aac")
	if pv.Kind != KindAudio {
		t.Fatalf("%+v", pv)
	}
	if len(pv.Notes) == 0 {
		t.Fatalf("expected a degraded but valid preview: %+v", pv)
	}
	var format string
	for _, f := range pv.Fields {
		if f.Label == "Format" {
			format = f.Value
		}
	}
	if format != "AAC" {
		t.Fatalf("format %q from %+v", format, pv.Fields)
	}
}

type panicky struct{}

func (panicky) CanHandle(string, string) bool         { return true }
func (panicky) CreatePreview(string) (Preview, error) { panic("boom") }

type slow struct{}

func (slow) CanHandle(string, string) bool { return true }
func (slow) CreatePreview(string) (Preview, error) {
	time.Sleep(time.Second)
	return Preview{Kind: KindText}, nil
}

type failing struct{}

func (failing) CanHandle(string, string) bool         { return true }
func (failing) CreatePreview(string) (Preview, error) { return Preview{}, errors.New("nope") }

func TestRegistryIsolatesHandlerFailures(t *testing.T) {
	for name, h := range map[string]PreviewHandler{"panic": panicky{}, "error": failing{}} {
		pv := NewRegistry(h).Create("/x", "", "")
		if pv.Kind != KindGeneric || len(pv.Notes) == 0 {
			t.Errorf("%s: %+v", name, pv)
		}
	}
	r := NewRegistry(slow{})
	r.Timeout = 20 * time.Millisecond
	if pv := r.Create("/x", "", ""); pv.Kind != KindGeneric || !strings.Contains(pv.Notes[0], "timed out") {
		t.Fatalf("%+v", pv)
	}
}

func TestMissingFile(t *testing.T) {
	pv := Default().Create("/nonexistent/file.png", "image/png", ".png")
	if pv.Kind != KindGeneric {
		t.Fatalf("%+v", pv)
	}
}

func TestCacheLRU(t *testing.T) {
	c := NewCache(2)
	c.Put("a", Preview{Text: "a"})
	c.Put("b", Preview{Text: "b"})
	c.Get("a") // a is now most recent
	c.Put("c", Preview{Text: "c"})
	if _, ok := c.Get("b"); ok {
		t.Fatal("b should have been evicted")
	}
	if p, ok := c.Get("a"); !ok || p.Text != "a" {
		t.Fatal("a lost")
	}
	if Key("/x", 1, time.Unix(1, 0)) == Key("/x", 2, time.Unix(1, 0)) {
		t.Fatal("key ignores size")
	}
}

func TestNonRegularFilesAreNotPreviewed(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip("mkfifo unavailable:", err)
	}
	done := make(chan Preview, 1)
	go func() { done <- Default().Create(fifo, "text/plain", ".txt") }()
	select {
	case pv := <-done:
		if pv.Kind != KindGeneric || len(pv.Notes) == 0 {
			t.Fatalf("%+v", pv)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preview of a FIFO blocked")
	}
}

func TestTooManyStuckHandlersAreRefused(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	r := NewRegistry(stuck{block})
	r.Timeout = 5 * time.Millisecond
	for i := 0; i < maxInflight; i++ {
		r.Create("/x", "", "") // each times out and keeps its slot
	}
	pv := r.Create("/x", "", "")
	if len(pv.Notes) == 0 || !strings.Contains(pv.Notes[0], "too many") {
		t.Fatalf("%+v", pv)
	}
}

type stuck struct{ release chan struct{} }

func (stuck) CanHandle(string, string) bool { return true }
func (s stuck) CreatePreview(string) (Preview, error) {
	<-s.release
	return Preview{}, nil
}

// orientSlow is the obvious per-pixel reference implementation.
func orientSlow(img image.Image, o int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	nw, nh := w, h
	if o >= 5 {
		nw, nh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx, dy := x, y
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

func TestOrientMatchesReferenceForAllOrientationsAndImageTypes(t *testing.T) {
	nrgba := image.NewNRGBA(image.Rect(3, 5, 10, 9)) // 7x4, non-zero origin
	ycc := image.NewYCbCr(image.Rect(0, 0, 8, 6), image.YCbCrSubsampleRatio420)
	for i := range nrgba.Pix {
		nrgba.Pix[i] = uint8(i * 7)
	}
	for i := range ycc.Y {
		ycc.Y[i] = uint8(i * 5)
	}
	for i := range ycc.Cb {
		ycc.Cb[i], ycc.Cr[i] = uint8(i*11), uint8(255-i*3)
	}
	for name, img := range map[string]image.Image{"nrgba": nrgba, "ycbcr": ycc} {
		for o := 2; o <= 8; o++ {
			got, want := orient(img, o), orientSlow(img, o)
			if got.Bounds() != want.Bounds() {
				t.Fatalf("%s o=%d: bounds %v vs %v", name, o, got.Bounds(), want.Bounds())
			}
			if !bytes.Equal(got.(*image.RGBA).Pix, want.(*image.RGBA).Pix) {
				t.Fatalf("%s o=%d: pixels differ from the reference", name, o)
			}
		}
	}
}

func BenchmarkOrient(b *testing.B) {
	img := image.NewYCbCr(image.Rect(0, 0, 1600, 1200), image.YCbCrSubsampleRatio420)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		orient(img, 6)
	}
}

func TestTextPreviewUTF16BOMAndLineCount(t *testing.T) {
	r := Default()
	u16 := func(order binary.AppendByteOrder, bom []byte, s string) []byte {
		b := append([]byte(nil), bom...)
		for _, u := range utf16.Encode([]rune(s)) {
			b = order.AppendUint16(b, u)
		}
		return b
	}
	for name, data := range map[string][]byte{
		"le.txt":  u16(binary.LittleEndian, []byte{0xFF, 0xFE}, "héllo\nwörld 日本\n"),
		"be.txt":  u16(binary.BigEndian, []byte{0xFE, 0xFF}, "héllo\nwörld 日本\n"),
		"odd.txt": append(u16(binary.LittleEndian, []byte{0xFF, 0xFE}, "héllo\nwörld 日本\n"), 0x41), // cut-off byte
	} {
		pv := r.Create(write(t, name, data), "text/plain", ".txt")
		if pv.Kind != KindText || pv.Text != "héllo\nwörld 日本\n" {
			t.Fatalf("%s: %+v", name, pv)
		}
		if pv.Fields[0].Value != "2" {
			t.Fatalf("%s: lines %q", name, pv.Fields[0].Value)
		}
	}
	// a UTF-8 BOM is dropped
	pv := r.Create(write(t, "bom.txt", []byte("\xEF\xBB\xBFhi")), "text/plain", ".txt")
	if pv.Text != "hi" {
		t.Fatalf("%q", pv.Text)
	}
	for in, want := range map[string]int{"": 0, "a": 1, "a\n": 1, "a\nb": 2, "a\nb\n": 2, "\n": 1} {
		if got := lineCount([]byte(in)); got != want {
			t.Errorf("lineCount(%q) = %d, want %d", in, got, want)
		}
	}
}
