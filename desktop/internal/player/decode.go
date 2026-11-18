// Package player decodes and plays audio. It is optional: sorting never
// depends on it, and playback failures (no sound device, unsupported codec)
// are reported as errors only.
package player

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/eaburns/flac"
	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"

	"sortie/internal/media"
)

// source is decoded audio: interleaved signed 16-bit little-endian PCM.
type source struct {
	r        io.Reader
	rate     int
	channels int
	dur      time.Duration
	close    func() error
}

// Supported reports whether files with this extension can be played.
func Supported(ext string) bool {
	switch strings.ToLower(ext) {
	case ".mp3", ".wav", ".ogg", ".oga", ".flac":
		return true
	}
	return false
}

func open(path string) (*source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	s, err := decode(f, path)
	if err != nil {
		f.Close()
		return nil, err
	}
	s.close = f.Close
	return s, nil
}

func decode(f *os.File, path string) (s *source, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("decoder failed: %v", r)
		}
	}()
	ext := strings.ToLower(path[strings.LastIndexByte(path, '.')+1:])
	switch ext {
	case "mp3":
		d, err := mp3.NewDecoder(f)
		if err != nil {
			return nil, err
		}
		s := &source{r: d, rate: d.SampleRate(), channels: 2}
		if n := d.Length(); n > 0 {
			s.dur = time.Duration(n / 4 * int64(time.Second) / int64(s.rate))
		}
		return s, nil
	case "wav":
		w, err := media.ReadWAV(f)
		if err != nil {
			return nil, err
		}
		if w.Channels < 1 || !wavSupported(w.Format, w.BitsPerSample) {
			return nil, fmt.Errorf("unsupported WAV encoding (format %d, %d-bit); playable: 8/16/24/32-bit PCM and 32-bit float", w.Format, w.BitsPerSample)
		}
		if _, err := f.Seek(w.DataOffset, io.SeekStart); err != nil {
			return nil, err
		}
		var r io.Reader = io.LimitReader(f, w.DataSize)
		if !(w.Format == 1 && w.BitsPerSample == 16) {
			r = &pcmReader{r: r, bits: w.BitsPerSample, float: w.Format == 3}
		}
		return &source{r: r, rate: w.SampleRate, channels: w.Channels, dur: w.Duration()}, nil
	case "ogg", "oga":
		r, err := oggvorbis.NewReader(f)
		if err != nil {
			return nil, err
		}
		return &source{r: &floatReader{r: r}, rate: r.SampleRate(), channels: r.Channels(),
			dur: time.Duration(r.Length() * int64(time.Second) / int64(r.SampleRate()))}, nil
	case "flac":
		d, err := flac.NewDecoder(f)
		if err != nil {
			return nil, err
		}
		if !flacDepthSupported(d.BitsPerSample) {
			return nil, fmt.Errorf("unsupported FLAC bit depth (%d); only 8, 16 and 24-bit can be played", d.BitsPerSample)
		}
		return &source{r: &flacReader{d: d, bps: d.BitsPerSample}, rate: d.SampleRate, channels: d.NChannels,
			dur: time.Duration(d.TotalSamples * int64(time.Second) / int64(d.SampleRate))}, nil
	}
	return nil, fmt.Errorf("playback of .%s is not supported", ext)
}

// wavSupported reports whether a WAV sample format can be converted to s16le.
func wavSupported(format, bits int) bool {
	switch format {
	case 1:
		return bits == 8 || bits == 16 || bits == 24 || bits == 32
	case 3:
		return bits == 32
	}
	return false
}

// pcmReader converts 8-bit (unsigned), 24/32-bit (signed) PCM and 32-bit
// float samples to signed 16-bit little-endian bytes.
type pcmReader struct {
	r     io.Reader
	bits  int
	float bool
	buf   []byte
	out   []byte
}

func (pr *pcmReader) Read(p []byte) (int, error) {
	bytesPer := pr.bits / 8
	for len(pr.out) == 0 {
		if pr.buf == nil {
			pr.buf = make([]byte, 4096*bytesPer)
		}
		n, err := io.ReadFull(pr.r, pr.buf)
		n -= n % bytesPer // drop a trailing partial sample
		for i := 0; i+bytesPer <= n; i += bytesPer {
			pr.out = binary.LittleEndian.AppendUint16(pr.out, uint16(pr.sample16(pr.buf[i:i+bytesPer])))
		}
		if len(pr.out) == 0 {
			if err == nil || err == io.ErrUnexpectedEOF {
				err = io.EOF
			}
			return 0, err
		}
	}
	n := copy(p, pr.out)
	pr.out = pr.out[n:]
	return n, nil
}

func (pr *pcmReader) sample16(b []byte) int16 {
	switch {
	case pr.float:
		x := int(math.Round(float64(math.Float32frombits(binary.LittleEndian.Uint32(b))) * 32767))
		if x > 32767 {
			x = 32767
		} else if x < -32768 {
			x = -32768
		}
		return int16(x)
	case pr.bits == 8:
		return int16(int(b[0])-128) << 8
	case pr.bits == 24:
		return int16(binary.LittleEndian.Uint16(b[1:3]))
	default: // 32-bit integer
		return int16(binary.LittleEndian.Uint16(b[2:4]))
	}
}

// floatReader converts oggvorbis float32 samples to s16le bytes.
type floatReader struct {
	r   *oggvorbis.Reader
	buf []float32
	out []byte
}

func (fr *floatReader) Read(p []byte) (int, error) {
	if len(fr.out) == 0 {
		if fr.buf == nil {
			fr.buf = make([]float32, 4096)
		}
		n, err := fr.r.Read(fr.buf)
		if n == 0 {
			return 0, err
		}
		fr.out = fr.out[:0]
		for _, v := range fr.buf[:n] {
			x := int(math.Round(float64(v) * 32767))
			if x > 32767 {
				x = 32767
			} else if x < -32768 {
				x = -32768
			}
			fr.out = binary.LittleEndian.AppendUint16(fr.out, uint16(int16(x)))
		}
	}
	n := copy(p, fr.out)
	fr.out = fr.out[n:]
	return n, nil
}

// flacDepthSupported reports whether flacReader can convert samples of this
// depth; other depths would otherwise decode to silence.
func flacDepthSupported(bps int) bool { return bps == 8 || bps == 16 || bps == 24 }

// flacReader converts decoded FLAC frames (8/16/24-bit) to s16le bytes.
type flacReader struct {
	d   *flac.Decoder
	bps int
	out []byte
}

func (fr *flacReader) Read(p []byte) (int, error) {
	for len(fr.out) == 0 {
		raw, err := fr.d.Next()
		if err != nil {
			return 0, err
		}
		switch fr.bps {
		case 16:
			fr.out = raw
		case 8:
			for _, b := range raw {
				fr.out = binary.LittleEndian.AppendUint16(fr.out, uint16(int16(int8(b))<<8))
			}
		case 24:
			for i := 0; i+2 < len(raw); i += 3 {
				fr.out = append(fr.out, raw[i+1], raw[i+2])
			}
		}
	}
	n := copy(p, fr.out)
	fr.out = fr.out[n:]
	return n, nil
}
