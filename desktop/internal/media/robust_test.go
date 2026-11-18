package media

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
	"time"
)

// Seed files for the container parsers (valid minimal examples).

func seedMP4() []byte {
	created := uint32(time.Date(2018, 6, 1, 0, 0, 0, 0, time.UTC).Sub(mp4Epoch) / time.Second)
	mvhd := box("mvhd", []byte{0, 0, 0, 0}, be32(created), be32(created), be32(1000), be32(125000), make([]byte, 80))
	tk := cat([]byte{0, 0, 0, 0}, be32(0), be32(0), be32(1), be32(0), be32(125000), make([]byte, 8+2+2+2+2+36), be32(1920<<16), be32(1080<<16))
	return cat(box("ftyp", []byte("isom\x00\x00\x02\x00")), box("moov", mvhd, box("trak", box("tkhd", tk))), box("mdat", make([]byte, 100)))
}

func seedMKV(dur32 bool) []byte {
	var d []byte
	if dur32 {
		d = make([]byte, 4)
		binary.BigEndian.PutUint32(d, math.Float32bits(90000))
	} else {
		d = make([]byte, 8)
		binary.BigEndian.PutUint64(d, math.Float64bits(90000))
	}
	info := ebml([]byte{0x15, 0x49, 0xA9, 0x66}, ebml([]byte{0x44, 0x89}, d))
	video := ebml([]byte{0xE0}, ebml([]byte{0xB0}, []byte{0x05, 0x00}), ebml([]byte{0xBA}, []byte{0x02, 0xD0}))
	tracks := ebml([]byte{0x16, 0x54, 0xAE, 0x6B}, ebml([]byte{0xAE}, video))
	seg := cat([]byte{0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, info, tracks)
	return cat(ebml([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte{0x42, 0x82, 0x84, 'w', 'e', 'b', 'm'}), seg)
}

// parseAll feeds b to every container parser; none may panic or hang (a panic
// in the unrecovered internal readers would crash a preview).
func parseAll(b []byte) {
	r := bytes.NewReader(b)
	readISOBMFF(r, int64(len(b)))
	readEBML(r, int64(len(b)))
	readAVI(r)
	ReadWAV(bytes.NewReader(b))
}

func TestParsersSurviveEveryTruncationOfValidFiles(t *testing.T) {
	seeds := map[string][]byte{
		"mp4": seedMP4(), "mkv64": seedMKV(false), "mkv32": seedMKV(true), "wav": wavBytes(8000, 1, 1)[:200],
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, s := range seeds {
			for n := 0; n <= len(s); n++ {
				parseAll(s[:n])
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("a parser hangs on a truncated file")
	}
}

func TestEBMLFloatDurationSizes(t *testing.T) {
	for name, file := range map[string][]byte{"4-byte": seedMKV(true), "8-byte": seedMKV(false)} {
		b := file
		v, err := readEBML(bytes.NewReader(b), int64(len(b)))
		if err != nil || v.Duration != 90*time.Second {
			t.Errorf("%s float duration: %+v %v", name, v, err)
		}
	}
}

func TestISOBMFFOddBoxes(t *testing.T) {
	good := seedMP4()
	// a box that claims to be far larger than the file must not be followed
	huge := cat(be32(0xFFFFFF00), []byte("moov"), make([]byte, 16))
	if _, err := readISOBMFF(bytes.NewReader(huge), int64(len(huge))); err == nil {
		t.Error("oversized box accepted")
	}
	// size 0 means "to the end of the file"; size 1 means a 64-bit size follows
	zero := cat(be32(0), []byte("free"), make([]byte, 8))
	one := cat(be32(1), []byte("free"), binary.BigEndian.AppendUint64(nil, 16+4), []byte{0, 0, 0, 0})
	for name, prefix := range map[string][]byte{"size0": zero, "size1": one} {
		b := cat(prefix, good)
		if name == "size0" { // a size-0 box runs to EOF: nothing can follow it
			b = prefix
		}
		readISOBMFF(bytes.NewReader(b), int64(len(b))) // must simply return
	}
	b := cat(one, good)
	if v, err := readISOBMFF(bytes.NewReader(b), int64(len(b))); err != nil || v.Duration != 125*time.Second {
		t.Errorf("a 64-bit-size box before moov broke parsing: %+v %v", v, err)
	}
}

func FuzzReadISOBMFF(f *testing.F) {
	f.Add(seedMP4())
	f.Fuzz(func(t *testing.T, b []byte) { readISOBMFF(bytes.NewReader(b), int64(len(b))) })
}

func FuzzReadEBML(f *testing.F) {
	f.Add(seedMKV(false))
	f.Add(seedMKV(true))
	f.Fuzz(func(t *testing.T, b []byte) { readEBML(bytes.NewReader(b), int64(len(b))) })
}

func FuzzReadWAV(f *testing.F) {
	f.Add(wavBytes(8000, 1, 1)[:100])
	f.Fuzz(func(t *testing.T, b []byte) { ReadWAV(bytes.NewReader(b)) })
}

func FuzzReadAVI(f *testing.F) {
	f.Add(cat([]byte("RIFF"), le32(0), []byte("AVI LIST"), le32(0), []byte("hdrlavih"), le32(56), make([]byte, 56)))
	f.Fuzz(func(t *testing.T, b []byte) { readAVI(bytes.NewReader(b)) })
}

func be64(v uint64) []byte { b := make([]byte, 8); binary.BigEndian.PutUint64(b, v); return b }

// A v1 mvhd with a huge creation time must not wrap around time.Duration into a
// bogus date, while a normal v1 value still yields the right year.
func TestISOBMFFCreationTimeOverflow(t *testing.T) {
	mp4 := func(created uint64) []byte {
		mvhd := box("mvhd", []byte{1, 0, 0, 0}, be64(created), be64(created), be32(1000), be64(5000), make([]byte, 80))
		return cat(box("ftyp", []byte("isom\x00\x00\x02\x00")), box("moov", mvhd), box("mdat", make([]byte, 16)))
	}
	ok := uint64(time.Date(2018, 6, 1, 0, 0, 0, 0, time.UTC).Sub(mp4Epoch) / time.Second)
	if v, err := ReadVideo(write(t, "ok.mp4", mp4(ok))); err != nil || v.Created.Year() != 2018 {
		t.Fatalf("%+v %v", v, err)
	}
	for _, huge := range []uint64{1 << 39, 1<<40 - 1} {
		v, err := ReadVideo(write(t, "huge.mp4", mp4(huge)))
		if err != nil || !v.Created.IsZero() {
			t.Errorf("created=%d: want zero date, got %v (%v)", huge, v.Created, err)
		}
	}
}

func tkhdBox(w, h uint32, matrix []byte) []byte {
	return box("tkhd", cat([]byte{0, 0, 0, 0}, be32(0), be32(0), be32(1), be32(0), be32(0),
		make([]byte, 8+2+2+2+2), matrix, be32(w<<16), be32(h<<16)))
}

func matrixBytes(a, b, c, d int32) []byte {
	return cat(be32(uint32(a)), be32(uint32(b)), be32(0), be32(uint32(c)), be32(uint32(d)), be32(0), be32(0), be32(0), be32(1<<30))
}

// Portrait phone video: stored 1920x1080 with a 90 degree rotation matrix is
// shown as 1080x1920.
func TestISOBMFFRotationSwapsDimensions(t *testing.T) {
	mvhd := box("mvhd", []byte{0, 0, 0, 0}, be32(0), be32(0), be32(1000), be32(5000), make([]byte, 80))
	for name, tc := range map[string]struct {
		m    []byte
		w, h int
	}{
		"identity": {matrixBytes(0x10000, 0, 0, 0x10000), 1920, 1080},
		"rot90":    {matrixBytes(0, 0x10000, -0x10000, 0), 1080, 1920},
		"rot270":   {matrixBytes(0, -0x10000, 0x10000, 0), 1080, 1920},
		"rot180":   {matrixBytes(-0x10000, 0, 0, -0x10000), 1920, 1080},
	} {
		moov := box("moov", mvhd, box("trak", tkhdBox(1920, 1080, tc.m)))
		v, err := ReadVideo(write(t, name+".mp4", cat(box("ftyp", []byte("isom\x00\x00\x02\x00")), moov)))
		if err != nil || v.Width != tc.w || v.Height != tc.h {
			t.Errorf("%s: %dx%d (%v), want %dx%d", name, v.Width, v.Height, err, tc.w, tc.h)
		}
	}
}

// Fragmented MP4: mvhd duration is 0, the length is in mvex/mehd.
func TestISOBMFFFragmentedDurationFromMehd(t *testing.T) {
	mvhd := box("mvhd", []byte{0, 0, 0, 0}, be32(0), be32(0), be32(1000), be32(0), make([]byte, 80))
	mehd0 := box("mehd", []byte{0, 0, 0, 0}, be32(42000))
	mehd1 := box("mehd", []byte{1, 0, 0, 0}, be64(61000))
	for name, mehd := range map[string][]byte{"v0": mehd0, "v1": mehd1} {
		moov := box("moov", mvhd, box("mvex", mehd))
		v, err := ReadVideo(write(t, "frag"+name+".mp4", cat(box("ftyp", []byte("isom\x00\x00\x02\x00")), moov)))
		want := map[string]time.Duration{"v0": 42 * time.Second, "v1": 61 * time.Second}[name]
		if err != nil || v.Duration != want {
			t.Errorf("%s: duration %v (%v), want %v", name, v.Duration, err, want)
		}
	}
}
