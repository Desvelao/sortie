package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, name string, b []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func be32(v uint32) []byte       { b := make([]byte, 4); binary.BigEndian.PutUint32(b, v); return b }
func le32(v uint32) []byte       { b := make([]byte, 4); binary.LittleEndian.PutUint32(b, v); return b }
func le16(v uint16) []byte       { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, v); return b }
func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func box(typ string, body ...[]byte) []byte {
	b := cat(body...)
	return cat(be32(uint32(8+len(b))), []byte(typ), b)
}

func wavBytes(rate, ch, seconds int) []byte {
	data := make([]byte, rate*ch*2*seconds)
	return cat([]byte("RIFF"), le32(uint32(36+len(data))), []byte("WAVEfmt "), le32(16),
		le16(1), le16(uint16(ch)), le32(uint32(rate)), le32(uint32(rate*ch*2)), le16(uint16(ch*2)), le16(16),
		[]byte("data"), le32(uint32(len(data))), data)
}

func TestParsePDFDate(t *testing.T) {
	if d := ParsePDFDate("D:20190412103000+02'00'"); d.Year() != 2019 || d.Month() != 4 || d.Hour() != 10 {
		t.Fatal(d)
	}
	if d := ParsePDFDate("D:2020"); d.Year() != 2020 {
		t.Fatal(d)
	}
	for _, bad := range []string{"", "garbage", "D:20201399"} {
		if !ParsePDFDate(bad).IsZero() {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestWAV(t *testing.T) {
	p := write(t, "a.wav", wavBytes(8000, 2, 3))
	a, err := ReadAudio(p)
	if err != nil || a.Duration != 3*time.Second || a.SampleRate != 8000 || a.Channels != 2 || a.Format != "WAV" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := ReadAudio(write(t, "bad.wav", []byte("not a wav at all"))); err == nil {
		t.Fatal("expected error")
	}
}

func TestID3Tags(t *testing.T) {
	frame := func(id, text string) []byte {
		body := cat([]byte{0}, []byte(text))
		return cat([]byte(id), be32(uint32(len(body))), []byte{0, 0}, body)
	}
	frames := cat(frame("TIT2", "Song"), frame("TPE1", "Artist"), frame("TALB", "Album"), frame("TYER", "2019"))
	n := len(frames)
	syncsafe := []byte{byte(n >> 21 & 0x7f), byte(n >> 14 & 0x7f), byte(n >> 7 & 0x7f), byte(n & 0x7f)}
	tagBytes := cat([]byte("ID3\x03\x00\x00"), syncsafe, frames, make([]byte, 64))
	a, err := ReadAudio(write(t, "x.mp3", tagBytes)) // no audio frames: duration unknown, tags still shown
	if err != nil || a.Title != "Song" || a.Artist != "Artist" || a.Album != "Album" || a.Year != 2019 {
		t.Fatalf("%+v %v", a, err)
	}
	if y := Fields(write(t, "y.mp3", tagBytes))["year"]; y != "2019" {
		t.Fatalf("year field = %q", y)
	}
}

func TestMP4(t *testing.T) {
	created := uint32(time.Date(2018, 6, 1, 0, 0, 0, 0, time.UTC).Sub(mp4Epoch) / time.Second)
	mvhd := box("mvhd", []byte{0, 0, 0, 0}, be32(created), be32(created), be32(1000), be32(125000), make([]byte, 80))
	tk := cat([]byte{0, 0, 0, 0}, be32(0), be32(0), be32(1), be32(0), be32(125000), make([]byte, 8+2+2+2+2+36), be32(1920<<16), be32(1080<<16))
	moov := box("moov", mvhd, box("trak", box("tkhd", tk)))
	p := write(t, "v.mp4", cat(box("ftyp", []byte("isom\x00\x00\x02\x00")), moov, box("mdat", make([]byte, 100))))
	v, err := ReadVideo(p)
	if err != nil || v.Duration != 125*time.Second || v.Width != 1920 || v.Height != 1080 || v.Created.Year() != 2018 {
		t.Fatalf("%+v %v", v, err)
	}
	if y := Fields(p)["year"]; y != "2018" {
		t.Fatalf("year field = %q", y)
	}
	if _, err := ReadVideo(write(t, "broken.mp4", []byte("garbage garbage garbage"))); err == nil {
		t.Fatal("expected error")
	}
}

func ebml(id []byte, body ...[]byte) []byte {
	b := cat(body...)
	return cat(id, []byte{0x80 | byte(len(b))}, b) // sizes < 127
}

func TestMatroska(t *testing.T) {
	f64 := make([]byte, 8)
	binary.BigEndian.PutUint64(f64, math.Float64bits(90000)) // ms with default 1e6 scale → 90 s
	info := ebml([]byte{0x15, 0x49, 0xA9, 0x66}, ebml([]byte{0x44, 0x89}, f64))
	video := ebml([]byte{0xE0}, ebml([]byte{0xB0}, []byte{0x05, 0x00}), ebml([]byte{0xBA}, []byte{0x02, 0xD0}))
	tracks := ebml([]byte{0x16, 0x54, 0xAE, 0x6B}, ebml([]byte{0xAE}, video))
	seg := cat([]byte{0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, info, tracks)
	file := cat(ebml([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte{0x42, 0x82, 0x84, 'w', 'e', 'b', 'm'}), seg)
	v, err := ReadVideo(write(t, "v.webm", file))
	if err != nil || v.Duration != 90*time.Second || v.Width != 1280 || v.Height != 720 {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestAVI(t *testing.T) {
	avih := make([]byte, 56)
	copy(avih[0:], le32(40000)) // 25 fps
	copy(avih[16:], le32(250))  // frames → 10 s
	copy(avih[32:], le32(640))
	copy(avih[36:], le32(480))
	b := cat([]byte("RIFF"), le32(0), []byte("AVI LIST"), le32(0), []byte("hdrlavih"), le32(56), avih)
	v, err := ReadVideo(write(t, "v.avi", b))
	if err != nil || v.Duration != 10*time.Second || v.Width != 640 || v.Height != 480 {
		t.Fatalf("%+v %v", v, err)
	}
}

func exifJPEG(t *testing.T, orientation int) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 4)), nil); err != nil {
		t.Fatal(err)
	}
	date := append([]byte("2019:07:04 12:00:00"), 0)
	ifd0 := cat(le16(2),
		le16(0x0112), le16(3), le32(1), le16(uint16(orientation)), le16(0),
		le16(0x8769), le16(4), le32(1), le32(38),
		le32(0))
	exifIFD := cat(le16(1), le16(0x9003), le16(2), le32(uint32(len(date))), le32(56), le32(0))
	tiff := cat([]byte("II"), le16(42), le32(8), ifd0, exifIFD, date)
	app1 := cat([]byte("Exif\x00\x00"), tiff)
	seg := cat([]byte{0xFF, 0xE1}, []byte{byte((len(app1) + 2) >> 8), byte(len(app1) + 2)}, app1)
	j := buf.Bytes()
	return cat(j[:2], seg, j[2:])
}

func TestEXIF(t *testing.T) {
	p := write(t, "p.jpg", exifJPEG(t, 6))
	e, err := ReadEXIF(p)
	if err != nil || e.Taken.Year() != 2019 || e.Orientation != 6 {
		t.Fatalf("%+v %v", e, err)
	}
	if y := Fields(p)["year"]; y != "2019" {
		t.Fatalf("year field = %q", y)
	}
	if _, err := ReadEXIF(write(t, "noexif.jpg", []byte{0xFF, 0xD8, 0xFF, 0xD9})); err == nil {
		t.Fatal("expected error")
	}
}

func pdfBytes(title string) []byte {
	var b bytes.Buffer
	var offs []int
	obj := func(s string) { offs = append(offs, b.Len()); fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", len(offs), s) }
	b.WriteString("%PDF-1.4\n")
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	obj("<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	obj("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] /Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	content := "BT /F1 12 Tf 10 100 Td (Hello Invoice) Tj ET"
	obj(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	obj(fmt.Sprintf("<< /Title (%s) /Author (Ann) /CreationDate (D:20190412103000Z) >>", title))
	x := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R /Info 6 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, x)
	return b.Bytes()
}

func TestPDF(t *testing.T) {
	p := write(t, "d.pdf", pdfBytes("My Doc"))
	info, err := ReadPDF(p, true)
	if err != nil || info.Pages != 1 || info.Title != "My Doc" || info.Author != "Ann" || info.Created.Year() != 2019 {
		t.Fatalf("%+v %v", info, err)
	}
	if info.FirstPageText == "" {
		t.Log("first-page text not extracted (non-fatal)")
	}
	if y := Fields(p)["year"]; y != "2019" {
		t.Fatalf("year field = %q", y)
	}
	if _, err := ReadPDF(write(t, "bad.pdf", []byte("%PDF-1.4 truncated")), true); err == nil {
		t.Fatal("expected error for corrupt pdf")
	}
}

func TestWAVExtensibleAndStreamedSizes(t *testing.T) {
	data := make([]byte, 8000*2*2) // 2 s mono 16-bit
	ext := func(riffSize, dataSize uint32, truncate int) []byte {
		sub := cat(le16(1), []byte{0x00, 0x00, 0x00, 0x00, 0x10, 0x00, 0x80, 0x00, 0x00, 0xAA, 0x00, 0x38, 0x9B, 0x71})
		fmtBody := cat(le16(0xFFFE), le16(1), le32(8000), le32(16000), le16(2), le16(16), le16(22), le16(16), le32(4), sub)
		b := cat([]byte("RIFF"), le32(riffSize), []byte("WAVEfmt "), le32(uint32(len(fmtBody))), fmtBody,
			[]byte("data"), le32(dataSize), data)
		return b[:len(b)-truncate]
	}
	// WAVE_FORMAT_EXTENSIBLE resolves to its PCM sub-format
	w, err := ReadWAV(bytes.NewReader(ext(100, uint32(len(data)), 0)))
	if err != nil || w.Format != 1 || w.BitsPerSample != 16 || w.DataSize != int64(len(data)) {
		t.Fatalf("%+v %v", w, err)
	}
	// streamed: sizes 0xFFFFFFFF or 0 -> the real remaining bytes
	for _, c := range []struct{ riff, data uint32 }{{0xFFFFFFFF, 0xFFFFFFFF}, {0, 0}} {
		w, err = ReadWAV(bytes.NewReader(ext(c.riff, c.data, 0)))
		if err != nil || w.DataSize != int64(len(data)) || w.Duration() != 2*time.Second {
			t.Fatalf("%+v %v", w, err)
		}
	}
	// truncated file: declared size larger than what exists is clamped
	w, err = ReadWAV(bytes.NewReader(ext(100, uint32(len(data)), 4000)))
	if err != nil || w.DataSize != int64(len(data)-4000) {
		t.Fatalf("%+v %v", w, err)
	}
	// an explicit small data chunk is respected
	w, _ = ReadWAV(bytes.NewReader(ext(100, 1000, 0)))
	if w.DataSize != 1000 {
		t.Fatalf("%+v", w)
	}
}

func TestSafeTextSurvivesPanicsAndErrors(t *testing.T) {
	if got := safeText(func() (string, error) { return "  hello \n", nil }); got != "hello" {
		t.Fatalf("%q", got)
	}
	if got := safeText(func() (string, error) { panic("malformed content stream") }); got != "" {
		t.Fatalf("%q", got)
	}
	if got := safeText(func() (string, error) { return "partial", errors.New("bad") }); got != "" {
		t.Fatalf("%q", got)
	}
}
