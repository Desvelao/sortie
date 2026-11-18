package player

import (
	"io"
	"math"
)

// Output format shared by every track (oto contexts are process-wide).
const (
	outRate     = 44100
	outChannels = 2
	bytesPerSec = outRate * outChannels * 2
)

// converter turns s16le PCM with any channel count/rate into 44.1 kHz stereo:
// multichannel audio is downmixed (ITU-R BS.775 style, LFE dropped) and rates
// above 44.1 kHz go through a Hann-windowed sinc low-pass at the output
// Nyquist frequency (no aliasing); lower rates use linear interpolation.
type converter struct {
	src    io.Reader
	ch     int
	ratio  float64 // source frames per output frame
	frames [][2]int16
	pos    float64
	eof    bool
	raw    []byte
	out    []byte
}

func newConverter(src io.Reader, rate, channels int) *converter {
	return &converter{src: src, ch: channels, ratio: float64(rate) / outRate}
}

func (c *converter) fill() {
	if c.raw == nil {
		c.raw = make([]byte, 4096*c.ch*2)
	}
	n, err := io.ReadFull(c.src, c.raw)
	if err != nil {
		c.eof = true
	}
	step := c.ch * 2
	var smp [8]int16
	for i := 0; i+step <= n; i += step {
		for k := 0; k < c.ch && k < len(smp); k++ {
			smp[k] = int16(uint16(c.raw[i+2*k]) | uint16(c.raw[i+2*k+1])<<8)
		}
		l, r := downmix(smp[:min(c.ch, len(smp))])
		c.frames = append(c.frames, [2]int16{l, r})
	}
}

// downmix reduces one frame of 1..8 channels (WAVE/FLAC channel order) to
// stereo. Mono is duplicated, stereo kept; for more channels the centre and
// surrounds are folded in at -3 dB and the LFE channel is dropped.
func downmix(s []int16) (int16, int16) {
	const k = 0.7071
	var l, r float64
	f := func(i int) float64 { return float64(s[i]) }
	switch len(s) {
	case 1:
		l, r = f(0), f(0)
	case 2:
		l, r = f(0), f(1)
	case 3: // FL FR FC
		l, r = f(0)+k*f(2), f(1)+k*f(2)
	case 4: // FL FR BL BR
		l, r = f(0)+k*f(2), f(1)+k*f(3)
	case 5: // FL FR FC BL BR
		l, r = f(0)+k*f(2)+k*f(3), f(1)+k*f(2)+k*f(4)
	case 6: // FL FR FC LFE BL BR
		l, r = f(0)+k*f(2)+k*f(4), f(1)+k*f(2)+k*f(5)
	case 7: // FL FR FC LFE BC SL SR
		l, r = f(0)+k*f(2)+0.5*f(4)+k*f(5), f(1)+k*f(2)+0.5*f(4)+k*f(6)
	default: // 8: FL FR FC LFE BL BR SL SR
		l, r = f(0)+k*f(2)+k*f(4)+k*f(6), f(1)+k*f(2)+k*f(5)+k*f(7)
	}
	return clip16(l), clip16(r)
}

func clip16(v float64) int16 {
	switch {
	case v > 32767:
		return 32767
	case v < -32768:
		return -32768
	}
	return int16(v)
}

// lowpassTaps is the filter half-width in output-Nyquist zero crossings.
const lowpassTaps = 4

func (c *converter) Read(p []byte) (int, error) {
	// half is the filter half-width in source frames, hist how many consumed
	// frames are kept behind pos for it; span is the look-ahead needed.
	half, hist, span := 0.0, 0, 1.0
	if c.ratio > 1 {
		half = lowpassTaps * c.ratio
		hist = int(half) + 1
		span = half
	}
	for len(c.out) == 0 {
		for int(c.pos+span)+1 >= len(c.frames) && !c.eof {
			c.fill()
		}
		if int(c.pos)+1 >= len(c.frames) { // end of stream
			if len(c.frames) > 0 && int(c.pos) < len(c.frames) {
				f := c.frames[int(c.pos)]
				if c.ratio > 1 { // filter the last frame too
					l, r := c.lowpassAt(half)
					f = [2]int16{clip16(l), clip16(r)}
				}
				c.out = appendFrame(c.out, f[0], f[1])
				c.pos = float64(len(c.frames))
				break
			}
			return 0, io.EOF
		}
		for len(c.out) < 8192 && int(c.pos)+1 < len(c.frames) && (c.eof || int(c.pos+span)+1 < len(c.frames)) {
			var l, r float64
			if c.ratio > 1 {
				l, r = c.lowpassAt(half)
			} else {
				i := int(c.pos)
				t := c.pos - float64(i)
				a, b := c.frames[i], c.frames[i+1]
				l = float64(a[0])*(1-t) + float64(b[0])*t
				r = float64(a[1])*(1-t) + float64(b[1])*t
			}
			c.out = appendFrame(c.out, clip16(l), clip16(r))
			c.pos += c.ratio
		}
		if n := int(c.pos) - hist; n > 0 { // drop consumed frames, keep filter history
			if n > len(c.frames) {
				n = len(c.frames)
			}
			c.frames = c.frames[n:]
			c.pos -= float64(n)
		}
	}
	n := copy(p, c.out)
	c.out = c.out[n:]
	return n, nil
}

// lowpassAt evaluates the windowed-sinc filter (cutoff = output Nyquist) at
// c.pos. Frames outside the stream count as silence, so the start and the end
// fade rather than amplify a truncated filter.
func (c *converter) lowpassAt(half float64) (l, r float64) {
	fc := 1 / c.ratio
	var wsum float64
	for k := int(math.Ceil(c.pos - half)); k <= int(math.Floor(c.pos+half)); k++ {
		d := float64(k) - c.pos
		w := 0.5 * (1 + math.Cos(math.Pi*d/half))
		if d != 0 {
			x := math.Pi * d * fc
			w *= math.Sin(x) / x
		}
		wsum += w
		if k >= 0 && k < len(c.frames) {
			l += w * float64(c.frames[k][0])
			r += w * float64(c.frames[k][1])
		}
	}
	if wsum == 0 {
		return 0, 0
	}
	return l / wsum, r / wsum
}

func appendFrame(b []byte, l, r int16) []byte {
	return append(b, byte(l), byte(uint16(l)>>8), byte(r), byte(uint16(r)>>8))
}
