package media

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestFieldsUnsupportedOrBrokenFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"a.txt": "hi", "b.jpg": "not a jpeg", "c.pdf": "%PDF-broken"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if f := Fields(p); f == nil || len(f) != 0 {
			t.Errorf("%s: want empty map, got %v", name, f)
		}
	}
	if f := Fields(filepath.Join(dir, "missing.mp3")); len(f) != 0 {
		t.Errorf("missing file: %v", f)
	}
}

// Every container ReadAudio/ReadVideo understand must also feed Fields.
func TestFieldsCoversAllSupportedExtensions(t *testing.T) {
	dir := t.TempDir()
	put := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if f := Fields(put("a.wav", wavBytes(8000, 2, 3))); f["duration"] != "3" || f["format"] != "WAV" {
		t.Errorf("wav: %v", f)
	}

	avih := make([]byte, 56)
	copy(avih[0:], le32(40000))
	copy(avih[16:], le32(250))
	copy(avih[32:], le32(640))
	copy(avih[36:], le32(480))
	avi := cat([]byte("RIFF"), le32(0), []byte("AVI LIST"), le32(0), []byte("hdrlavih"), le32(56), avih)
	if f := Fields(put("v.avi", avi)); f["duration"] != "10" || f["width"] != "640" || f["height"] != "480" {
		t.Errorf("avi: %v", f)
	}

	f64 := make([]byte, 8)
	binary.BigEndian.PutUint64(f64, math.Float64bits(90000))
	info := ebml([]byte{0x15, 0x49, 0xA9, 0x66}, ebml([]byte{0x44, 0x89}, f64))
	seg := cat([]byte{0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, info)
	mka := cat(ebml([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte{0x42, 0x82, 0x88, 'm', 'a', 't', 'r', 'o', 's', 'k', 'a'}), seg)
	if f := Fields(put("s.mka", mka)); f["duration"] != "90" {
		t.Errorf("mka: %v", f)
	}
}
