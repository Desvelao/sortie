package player

import (
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ebitengine/oto/v3"
)

// State of the player.
type State int

const (
	Stopped State = iota
	Playing
	Paused
)

// Player plays one track at a time. All methods are safe for concurrent use.
type Player struct {
	mu    sync.Mutex
	ctx   *oto.Context
	op    *oto.Player
	src   *source
	count *countingReader
	state State
	path  string
	// offset is where the current track started (after a Seek); played bytes
	// are counted from there.
	offset     time.Duration
	onFinished func(path string)
	gen        int // identifies the current track for its watcher goroutine
}

func New() *Player { return &Player{} }

type countingReader struct {
	r io.Reader
	n atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

func (p *Player) context() (*oto.Context, error) {
	if p.ctx != nil {
		return p.ctx, nil
	}
	ctx, ready, err := oto.NewContext(&oto.NewContextOptions{
		SampleRate: outRate, ChannelCount: outChannels, Format: oto.FormatSignedInt16LE,
	})
	if err != nil {
		return nil, fmt.Errorf("no audio output available: %w", err)
	}
	<-ready
	p.ctx = ctx
	return ctx, nil
}

// SetOnFinished registers fn, called (from a separate goroutine) with the
// path of a track that ended on its own. Stop, Play and Seek do not call it.
func (p *Player) SetOnFinished(fn func(path string)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onFinished = fn
}

// Play starts path, replacing any current track.
func (p *Player) Play(path string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
	return p.startLocked(path, 0)
}

// Seek moves the current track to pos (clamped to its length). The track is
// decoded again from the start up to pos, so the cost grows with pos; it keeps
// the paused/playing state. It fails if nothing is loaded.
func (p *Player) Seek(pos time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshLocked()
	if p.state == Stopped || p.src == nil {
		return fmt.Errorf("nothing is playing")
	}
	if pos < 0 {
		pos = 0
	}
	if l := p.src.dur; l > 0 && pos > l {
		pos = l
	}
	path, wasPaused := p.path, p.state == Paused
	// Open and decode the new position first: if that fails (file moved,
	// decoder error) the current track keeps playing where it was.
	t, err := p.prepareLocked(path, pos)
	if err != nil {
		return err
	}
	p.stopLocked()
	p.playLocked(t)
	if wasPaused {
		p.op.Pause()
		p.state = Paused
	}
	return nil
}

// prepared is a track that is opened and decoded up to its start position but
// not playing yet, so a failure can be reported without touching the track
// that is currently playing.
type prepared struct {
	src  *source
	conv io.Reader
	path string
	from time.Duration
}

func (p *Player) prepareLocked(path string, from time.Duration) (*prepared, error) {
	src, err := open(path)
	if err != nil {
		return nil, err
	}
	if _, err := p.context(); err != nil {
		src.close()
		return nil, err
	}
	conv := newConverter(src.r, src.rate, src.channels)
	if err := skipDecoded(conv, from); err != nil {
		src.close()
		return nil, err
	}
	return &prepared{src: src, conv: conv, path: path, from: from}, nil
}

// playLocked starts a prepared track; the previous track must be stopped.
func (p *Player) playLocked(t *prepared) {
	cr := &countingReader{r: t.conv}
	op := p.ctx.NewPlayer(cr)
	op.Play()
	p.gen++
	p.op, p.src, p.count, p.state, p.path, p.offset = op, t.src, cr, Playing, t.path, t.from
	go p.watch(p.gen, op)
}

func (p *Player) startLocked(path string, from time.Duration) error {
	t, err := p.prepareLocked(path, from)
	if err != nil {
		return err
	}
	p.playLocked(t)
	return nil
}

// skipDecoded discards the first d of decoded output (44.1 kHz stereo s16le).
func skipDecoded(r io.Reader, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	n := int64(d) * bytesPerSec / int64(time.Second)
	n -= n % 4 // whole frames
	_, err := io.CopyN(io.Discard, r, n)
	if err == io.EOF { // seeking past the end just leaves an empty tail
		return nil
	}
	return err
}

// watch reports the end of a track that played to its end, without relying on
// the UI polling Status.
func (p *Player) watch(gen int, op *oto.Player) {
	for {
		time.Sleep(250 * time.Millisecond)
		p.mu.Lock()
		if p.gen != gen || p.op != op { // stopped, replaced or sought
			p.mu.Unlock()
			return
		}
		p.refreshLocked() // stops the track and fires the callback if it ended
		ended := p.gen != gen
		p.mu.Unlock()
		if ended {
			return
		}
	}
}

// TogglePause pauses or resumes the current track.
func (p *Player) TogglePause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshLocked()
	switch p.state {
	case Playing:
		p.op.Pause()
		p.state = Paused
	case Paused:
		p.op.Play()
		p.state = Playing
	}
}

// Stop halts playback and releases the track.
func (p *Player) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked()
}

func (p *Player) stopLocked() {
	if p.op != nil {
		p.op.Pause()
		p.op.Close()
	}
	if p.src != nil && p.src.close != nil {
		p.src.close()
	}
	p.op, p.src, p.count, p.state, p.path, p.offset = nil, nil, nil, Stopped, "", 0
	p.gen++ // retire the watcher of the track that was playing
}

// refreshLocked detects that a track finished on its own.
func (p *Player) refreshLocked() {
	if p.state == Playing && p.op != nil && !p.op.IsPlaying() {
		path, cb := p.path, p.onFinished
		p.stopLocked()
		if cb != nil {
			go cb(path)
		}
	}
}

// Status is a snapshot for the UI.
type Status struct {
	State            State
	Path             string
	Position, Length time.Duration
}

// Status returns the current state, playing path, position and length.
func (p *Player) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refreshLocked()
	st := Status{State: p.state, Path: p.path}
	if p.src != nil {
		st.Length = p.src.dur
		played := p.count.n.Load() - int64(p.op.BufferedSize())
		if played < 0 {
			played = 0
		}
		st.Position = p.offset + time.Duration(played)*time.Second/bytesPerSec
		if st.Length > 0 && st.Position > st.Length {
			st.Position = st.Length
		}
	}
	return st
}
