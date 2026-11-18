package player

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tone(rate, ch int, secs float64) []byte {
	n := int(float64(rate) * secs)
	var b bytes.Buffer
	for i := 0; i < n; i++ {
		v := int16(10000 * math.Sin(2*math.Pi*440*float64(i)/float64(rate)))
		for c := 0; c < ch; c++ {
			binary.Write(&b, binary.LittleEndian, v)
		}
	}
	return b.Bytes()
}

func TestConverterResamplesAndMixes(t *testing.T) {
	cases := []struct{ rate, ch int }{{44100, 2}, {22050, 1}, {8000, 2}, {48000, 2}, {96000, 6}}
	for _, c := range cases {
		in := tone(c.rate, c.ch, 0.5)
		out, err := io.ReadAll(newConverter(bytes.NewReader(in), c.rate, c.ch))
		if err != nil {
			t.Fatal(err)
		}
		wantFrames := 0.5 * outRate
		gotFrames := float64(len(out) / 4)
		if math.Abs(gotFrames-wantFrames) > 50 {
			t.Errorf("rate %d ch %d: got %v frames want ~%v", c.rate, c.ch, gotFrames, wantFrames)
		}
		if len(out)%4 != 0 {
			t.Errorf("output not frame aligned")
		}
	}
	if out, err := io.ReadAll(newConverter(bytes.NewReader(nil), 44100, 2)); err != nil || len(out) != 0 {
		t.Fatal(len(out), err)
	}
}

func wavFile(t *testing.T, rate, ch int, secs float64, bits int) string {
	data := tone(rate, ch, secs)
	var b bytes.Buffer
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, uint32(36+len(data)))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(ch), uint32(rate), uint32(rate * ch * 2), uint16(ch * 2), uint16(bits)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	p := filepath.Join(t.TempDir(), "t.wav")
	os.WriteFile(p, b.Bytes(), 0o644)
	return p
}

func TestOpenWAV(t *testing.T) {
	s, err := open(wavFile(t, 22050, 1, 1, 16))
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if s.rate != 22050 || s.channels != 1 || s.dur != time.Second {
		t.Fatalf("%+v", s)
	}
	if _, err := open(wavFile(t, 22050, 1, 1, 12)); err == nil {
		t.Fatal("12-bit WAV should be rejected")
	}
	s8, err := open(wavFile(t, 22050, 1, 1, 8))
	if err != nil {
		t.Fatalf("8-bit WAV is converted, not rejected: %v", err)
	}
	s8.close()
}

func TestPCMReaderConversions(t *testing.T) {
	read := func(in []byte, bits int, float bool) []int16 {
		out, err := io.ReadAll(&pcmReader{r: bytes.NewReader(in), bits: bits, float: float})
		if err != nil || len(out)%2 != 0 {
			t.Fatalf("%v len %d", err, len(out))
		}
		var s []int16
		for i := 0; i < len(out); i += 2 {
			s = append(s, int16(binary.LittleEndian.Uint16(out[i:])))
		}
		return s
	}
	eq := func(got []int16, want ...int16) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("got %v want %v", got, want)
			}
		}
	}
	eq(read([]byte{128, 255, 0}, 8, false), 0, 127<<8, -128<<8) // unsigned 8-bit
	eq(read([]byte{0x00, 0x34, 0x12, 0x00, 0x00, 0x80}, 24, false), 0x1234, -32768)
	eq(read([]byte{0, 0, 0x34, 0x12, 0, 0, 0, 0x80}, 32, false), 0x1234, -32768)
	f := func(v float32) []byte { return binary.LittleEndian.AppendUint32(nil, math.Float32bits(v)) }
	eq(read(append(append(f(0), f(1)...), f(-2)...), 32, true), 0, 32767, -32768)
	eq(read([]byte{0x00, 0x34, 0x12, 0x55}, 24, false), 0x1234) // trailing partial sample is dropped
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{"x.mp3": "not mp3", "x.ogg": "not ogg", "x.flac": "not flac", "x.m4a": "aac"} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(content), 0o644)
		if _, err := open(p); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := open(filepath.Join(dir, "missing.wav")); err == nil {
		t.Error("missing file should error")
	}
	if !Supported(".MP3") || Supported(".m4a") {
		t.Error("Supported() wrong")
	}
}

// Without a sound device Play must fail gracefully, not crash.
func TestPlayWithoutDeviceIsGraceful(t *testing.T) {
	p := New()
	err := p.Play(wavFile(t, 8000, 1, 1, 16))
	t.Logf("Play result (device-dependent): %v", err)
	p.Stop()
	if p.Status().State != Stopped {
		t.Fatal("expected stopped")
	}
}

func TestFlacDepthSupport(t *testing.T) {
	for bps, want := range map[int]bool{8: true, 16: true, 24: true, 12: false, 20: false, 32: false, 0: false} {
		if flacDepthSupported(bps) != want {
			t.Errorf("flacDepthSupported(%d) != %v", bps, want)
		}
	}
}

func TestSkipDecoded(t *testing.T) {
	data := make([]byte, bytesPerSec) // one second of decoded audio
	for i := range data {
		data[i] = byte(i)
	}
	r := bytes.NewReader(data)
	if err := skipDecoded(r, 250*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	want := bytesPerSec / 4
	if r.Len() != len(data)-want {
		t.Fatalf("skipped %d bytes, want %d", len(data)-r.Len(), want)
	}
	// a seek past the end is not an error, it just leaves nothing to play
	if err := skipDecoded(bytes.NewReader(data), 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := skipDecoded(bytes.NewReader(data), 0); err != nil {
		t.Fatal(err)
	}
}

func TestSeekWithoutTrackFailsAndCallbackIsStored(t *testing.T) {
	p := New()
	if err := p.Seek(time.Second); err == nil {
		t.Fatal("Seek with nothing loaded must fail")
	}
	called := make(chan string, 1)
	p.SetOnFinished(func(path string) { called <- path })
	// no sound device in tests: Play fails and must not fire the callback
	if err := p.Play(wavFile(t, 8000, 1, 1, 16)); err == nil {
		p.Stop()
	}
	select {
	case path := <-called:
		t.Fatalf("callback fired for %s although nothing finished", path)
	case <-time.After(400 * time.Millisecond):
	}
}

// A failing Seek (file vanished) must leave the current track loaded instead of
// stopping it. The track is faked, as tests have no sound device.
func TestSeekFailureKeepsCurrentTrack(t *testing.T) {
	p := New()
	closed := false
	missing := filepath.Join(t.TempDir(), "gone.wav")
	p.state, p.path = Playing, missing
	p.src = &source{dur: time.Minute, close: func() error { closed = true; return nil }}

	if err := p.Seek(5 * time.Second); err == nil {
		t.Fatal("Seek on a vanished file must fail")
	}
	if p.state != Playing || p.path != missing || p.src == nil {
		t.Errorf("track lost after failed Seek: state=%v path=%q src=%v", p.state, p.path, p.src)
	}
	if closed {
		t.Error("the current source must not be closed when Seek fails")
	}
}

func TestDownmixKeepsCentreAndSurroundsDropsLFE(t *testing.T) {
	// 5.1: FL FR FC LFE BL BR
	l, r := downmix([]int16{0, 0, 10000, 0, 0, 0})
	if l < 7000 || r < 7000 || l != r {
		t.Errorf("centre must reach both channels: %d %d", l, r)
	}
	if l, r := downmix([]int16{0, 0, 0, 20000, 0, 0}); l != 0 || r != 0 {
		t.Errorf("LFE must be dropped: %d %d", l, r)
	}
	if l, r := downmix([]int16{0, 0, 0, 0, 10000, 0}); l == 0 || r != 0 {
		t.Errorf("rear left must feed only the left channel: %d %d", l, r)
	}
	if l, r := downmix([]int16{30000, 0, 30000, 0, 30000, 0}); l != 32767 || r < 0 {
		t.Errorf("sum must clip, not wrap: %d %d", l, r)
	}
	if l, r := downmix([]int16{123}); l != 123 || r != 123 {
		t.Errorf("mono must be duplicated: %d %d", l, r)
	}
}

// A tone above the output Nyquist frequency (here 30 kHz at 96 kHz) used to
// alias into a loud audible tone; the box filter must attenuate it strongly.
func TestDownsamplingAttenuatesUltrasound(t *testing.T) {
	const rate = 96000
	var in []byte
	for i := 0; i < rate; i++ {
		v := int16(20000 * math.Sin(2*math.Pi*30000*float64(i)/rate))
		in = append(in, byte(v), byte(uint16(v)>>8), byte(v), byte(uint16(v)>>8))
	}
	out, err := io.ReadAll(newConverter(bytes.NewReader(in), rate, 2))
	if err != nil {
		t.Fatal(err)
	}
	var peak int16
	for i := 0; i+1 < len(out); i += 4 {
		v := int16(uint16(out[i]) | uint16(out[i+1])<<8)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
	}
	if peak > 12000 {
		t.Errorf("ultrasound leaked through at amplitude %d (input 20000)", peak)
	}
}
