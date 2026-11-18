package media

import (
	"encoding/binary"
	"errors"
	"io"
	"time"
)

// WAV describes a PCM/RIFF WAVE file.
type WAV struct {
	Format        int // 1 = PCM, 3 = IEEE float (WAVE_FORMAT_EXTENSIBLE is resolved to its sub-format)
	Channels      int
	SampleRate    int
	BitsPerSample int
	ByteRate      int
	DataOffset    int64
	DataSize      int64
}

// Duration of the audio data.
func (w WAV) Duration() time.Duration {
	if w.ByteRate <= 0 {
		return 0
	}
	return time.Duration(w.DataSize * int64(time.Second) / int64(w.ByteRate))
}

// ReadWAV parses RIFF chunks up to the data chunk.
func ReadWAV(r io.ReadSeeker) (w WAV, err error) {
	var h [12]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return
	}
	if string(h[0:4]) != "RIFF" || string(h[8:12]) != "WAVE" {
		return w, errors.New("not a WAVE file")
	}
	pos := int64(12)
	haveFmt := false
	for {
		var ch [8]byte
		if _, err = io.ReadFull(r, ch[:]); err != nil {
			return w, errors.New("WAVE data chunk not found")
		}
		size := int64(binary.LittleEndian.Uint32(ch[4:]))
		pos += 8
		switch string(ch[0:4]) {
		case "fmt ":
			if size < 16 {
				return w, errors.New("bad fmt chunk")
			}
			n := int64(16)
			if size >= 40 { // room for the WAVE_FORMAT_EXTENSIBLE extension
				n = 40
			}
			b := make([]byte, n)
			if _, err = io.ReadFull(r, b); err != nil {
				return
			}
			w.Format = int(binary.LittleEndian.Uint16(b[0:]))
			if w.Format == 0xFFFE && n == 40 {
				// the real format is the first two bytes of the sub-format GUID
				w.Format = int(binary.LittleEndian.Uint16(b[24:]))
			}
			w.Channels = int(binary.LittleEndian.Uint16(b[2:]))
			w.SampleRate = int(binary.LittleEndian.Uint32(b[4:]))
			w.ByteRate = int(binary.LittleEndian.Uint32(b[8:]))
			w.BitsPerSample = int(binary.LittleEndian.Uint16(b[14:]))
			haveFmt = true
			if _, err = r.Seek(size-n+(size&1), io.SeekCurrent); err != nil {
				return
			}
		case "data":
			if !haveFmt {
				return w, errors.New("data chunk before fmt chunk")
			}
			w.DataOffset, w.DataSize = pos, size
			// Streamed or truncated files declare 0, 0xFFFFFFFF or more bytes
			// than exist: use what is really there.
			if end, serr := r.Seek(0, io.SeekEnd); serr == nil {
				remaining := end - pos
				riffSize := int64(binary.LittleEndian.Uint32(h[4:8]))
				streamed := size == 0 && (riffSize == 0 || riffSize == 0xFFFFFFFF)
				if size > remaining || size == 0xFFFFFFFF || streamed {
					w.DataSize = remaining
				}
				if _, serr := r.Seek(pos, io.SeekStart); serr != nil {
					return w, serr
				}
			}
			return w, nil
		default:
			if _, err = r.Seek(size+(size&1), io.SeekCurrent); err != nil {
				return
			}
		}
		pos += size + (size & 1)
	}
}

// readAVI extracts duration and size from the main AVI header.
func readAVI(r io.ReaderAt) (v VideoInfo, err error) {
	b := make([]byte, 96)
	if _, err = r.ReadAt(b, 0); err != nil && err != io.EOF {
		return
	}
	if string(b[0:4]) != "RIFF" || string(b[8:12]) != "AVI " || string(b[12:16]) != "LIST" || string(b[20:24]) != "hdrl" || string(b[24:28]) != "avih" {
		return v, errors.New("unrecognised AVI header")
	}
	le := binary.LittleEndian
	usPerFrame := int64(le.Uint32(b[32:]))
	frames := int64(le.Uint32(b[48:]))
	v.Width, v.Height = int(le.Uint32(b[64:])), int(le.Uint32(b[68:]))
	v.Duration = time.Duration(usPerFrame*frames) * time.Microsecond
	v.Container = "AVI"
	return v, nil
}
