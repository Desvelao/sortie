package media

import (
	"errors"
	"io"
	"math"
	"time"
)

// EBML element ids (Matroska/WebM).
const (
	idSegment   = 0x18538067
	idInfo      = 0x1549A966
	idTracks    = 0x1654AE6B
	idCluster   = 0x1F43B675
	idTrackEnt  = 0xAE
	idVideo     = 0xE0
	idTimeScale = 0x2AD7B1
	idDuration  = 0x4489
	idDateUTC   = 0x4461
	idPixelW    = 0xB0
	idPixelH    = 0xBA
)

var mkvEpoch = time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)

func readEBML(r io.ReaderAt, size int64) (VideoInfo, error) {
	v := VideoInfo{Container: "Matroska/WebM"}
	scale := int64(1000000)
	var dur float64
	found := false

	readVint := func(pos int64, keepMarker bool) (val uint64, n int, unknown bool, ok bool) {
		var b [8]byte
		if _, err := r.ReadAt(b[:1], pos); err != nil {
			return
		}
		l := 1
		for mask := byte(0x80); l <= 8 && b[0]&mask == 0; mask >>= 1 {
			l++
		}
		if l > 8 {
			return
		}
		if l > 1 {
			if _, err := r.ReadAt(b[1:l], pos+1); err != nil {
				return
			}
		}
		val = uint64(b[0])
		if !keepMarker {
			val &= (1 << (8 - uint(l))) - 1
		}
		allOnes := b[0]&((1<<(8-uint(l)))-1) == (1<<(8-uint(l)))-1
		for i := 1; i < l; i++ {
			val = val<<8 | uint64(b[i])
			allOnes = allOnes && b[i] == 0xFF
		}
		return val, l, !keepMarker && allOnes, true
	}
	readUint := func(pos, n int64) uint64 {
		var b [8]byte
		if n > 8 || n < 1 {
			return 0
		}
		r.ReadAt(b[8-n:], pos)
		var x uint64
		for _, c := range b {
			x = x<<8 | uint64(c)
		}
		return x
	}

	var walk func(start, end int64, depth int, inVideo bool) bool // false = stop (reached clusters)
	walk = func(start, end int64, depth int, inVideo bool) bool {
		for pos := start; pos < end; {
			id, n1, _, ok := readVint(pos, true)
			if !ok {
				return true
			}
			sz, n2, unknown, ok := readVint(pos+int64(n1), false)
			if !ok {
				return true
			}
			body := pos + int64(n1+n2)
			stop := end
			if !unknown && body+int64(sz) < end {
				stop = body + int64(sz)
			}
			switch id {
			case idCluster:
				return false
			case idSegment, idInfo, idTracks, idTrackEnt, idVideo:
				if depth < 6 {
					if !walk(body, stop, depth+1, id == idVideo || inVideo) {
						return false
					}
				}
			case idTimeScale:
				scale = int64(readUint(body, stop-body))
			case idDuration:
				if l := stop - body; l == 4 {
					dur = float64(math.Float32frombits(uint32(readUint(body, 4))))
					found = true
				} else if l == 8 {
					dur = math.Float64frombits(readUint(body, 8))
					found = true
				}
			case idDateUTC:
				ns := int64(readUint(body, stop-body))
				v.Created = mkvEpoch.Add(time.Duration(ns))
			case idPixelW:
				if inVideo {
					v.Width = int(readUint(body, stop-body))
				}
			case idPixelH:
				if inVideo {
					v.Height = int(readUint(body, stop-body))
				}
			}
			if unknown {
				return true
			}
			pos = stop
		}
		return true
	}
	walk(0, size, 0, false)
	if !found && v.Width == 0 {
		return v, errors.New("no Matroska info found")
	}
	v.Duration = time.Duration(dur * float64(scale))
	return v, nil
}
