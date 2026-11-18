package media

import (
	"encoding/binary"
	"errors"
	"io"
	"time"
)

var mp4Epoch = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)

// readISOBMFF reads mvhd (duration, creation time) and tkhd (video size).
func readISOBMFF(r io.ReaderAt, size int64) (VideoInfo, error) {
	v := VideoInfo{Container: "MP4/MOV"}
	found := false
	var movScale uint32 // mvhd timescale
	var fragDur uint64  // mvex/mehd total duration of a fragmented file
	var walk func(start, end int64, depth int) error
	walk = func(start, end int64, depth int) error {
		for pos := start; pos+8 <= end; {
			var h [16]byte
			if _, err := r.ReadAt(h[:8], pos); err != nil {
				return nil
			}
			bs := int64(binary.BigEndian.Uint32(h[0:]))
			typ := string(h[4:8])
			hdr := int64(8)
			if bs == 1 {
				if _, err := r.ReadAt(h[8:16], pos+8); err != nil {
					return nil
				}
				bs, hdr = int64(binary.BigEndian.Uint64(h[8:])), 16
			} else if bs == 0 {
				bs = end - pos
			}
			if bs < hdr || pos+bs > end {
				bs = end - pos // tolerate truncated files
				if bs < hdr {
					return nil
				}
			}
			body := pos + hdr
			switch typ {
			case "moov", "trak", "mdia", "mvex":
				if depth < 6 {
					walk(body, pos+bs, depth+1)
				}
			case "mvhd":
				var b [32]byte
				n, _ := r.ReadAt(b[:], body)
				if n >= 20 {
					found = true
					var created uint64
					var scale uint32
					var dur uint64
					if b[0] == 1 && n >= 32 {
						created = binary.BigEndian.Uint64(b[4:])
						scale = binary.BigEndian.Uint32(b[20:])
						dur = binary.BigEndian.Uint64(b[24:])
					} else {
						created = uint64(binary.BigEndian.Uint32(b[4:]))
						scale = binary.BigEndian.Uint32(b[12:])
						dur = uint64(binary.BigEndian.Uint32(b[16:]))
					}
					movScale = scale
					if scale > 0 {
						v.Duration = time.Duration(float64(dur) / float64(scale) * float64(time.Second))
					}
					// time.Duration holds only ~292 years of nanoseconds, so build the
					// date from Unix seconds; implausibly large values (corrupt
					// headers) are ignored rather than filed under a bogus year.
					if created > 0 && created < 1<<33 {
						v.Created = time.Unix(int64(created)+mp4Epoch.Unix(), 0).UTC()
					}
				}
			case "mehd":
				var b [12]byte
				if n, _ := r.ReadAt(b[:], body); n >= 8 {
					if b[0] == 1 && n >= 12 {
						fragDur = binary.BigEndian.Uint64(b[4:]) // needs 12 bytes
					} else {
						fragDur = uint64(binary.BigEndian.Uint32(b[4:]))
					}
				}
			case "tkhd":
				var b [96]byte
				n, _ := r.ReadAt(b[:], body)
				off := 76
				if n > 0 && b[0] == 1 {
					off = 88
				}
				if n >= off+8 {
					w := int(binary.BigEndian.Uint32(b[off:]) >> 16)
					h := int(binary.BigEndian.Uint32(b[off+4:]) >> 16)
					// The 3x3 transformation matrix sits 36 bytes before the size.
					// Rotation by 90/270 degrees (portrait phone video) has a = d = 0
					// and swaps the displayed width and height.
					m := off - 36
					if a, d := binary.BigEndian.Uint32(b[m:]), binary.BigEndian.Uint32(b[m+16:]); a == 0 && d == 0 &&
						(binary.BigEndian.Uint32(b[m+4:]) != 0 || binary.BigEndian.Uint32(b[m+12:]) != 0) {
						w, h = h, w
					}
					if w*h > v.Width*v.Height {
						v.Width, v.Height = w, h
					}
				}
			}
			pos += bs
		}
		return nil
	}
	walk(0, size, 0)
	if v.Duration == 0 && fragDur > 0 && movScale > 0 {
		v.Duration = time.Duration(float64(fragDur) / float64(movScale) * float64(time.Second))
	}
	if !found {
		return v, errors.New("no movie header found")
	}
	return v, nil
}
