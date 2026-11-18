package media

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/dhowden/tag"
	"github.com/eaburns/flac"
	"github.com/hajimehoshi/go-mp3"
	"github.com/jfreymuth/oggvorbis"
)

var errTooLarge = errors.New("file too large to inspect")

// AudioInfo is tag data plus technical properties where available.
type AudioInfo struct {
	Format               string // e.g. "MP3", "FLAC"
	Title, Artist, Album string
	Genre                string
	Year                 int
	Duration             time.Duration
	SampleRate, Channels int
	BitrateKbps          int
}

// ReadAudio reads tags and duration. Tags and duration are independent:
// a failure of one does not hide the other.
func ReadAudio(path string) (a AudioInfo, err error) {
	defer recoverTo(&err, "audio")
	f, err := os.Open(path)
	if err != nil {
		return a, err
	}
	defer f.Close()
	st, _ := f.Stat()

	ext := strings.ToLower(extOf(path))
	a.Format = strings.ToUpper(strings.TrimPrefix(ext, "."))

	var tagErr error
	if m, e := tag.ReadFrom(f); e == nil {
		a.Title, a.Artist, a.Album, a.Genre, a.Year = m.Title(), m.Artist(), m.Album(), m.Genre(), m.Year()
		if ft := string(m.FileType()); ft != "" {
			a.Format = ft
		}
	} else {
		tagErr = e
	}
	f.Seek(0, 0)

	var durErr error
	switch ext {
	case ".mp3":
		if d, e := mp3.NewDecoder(f); e == nil {
			a.SampleRate, a.Channels = d.SampleRate(), 2
			if n := d.Length(); n > 0 && a.SampleRate > 0 {
				a.Duration = time.Duration(n / 4 * int64(time.Second) / int64(a.SampleRate))
			}
		} else {
			durErr = e
		}
	case ".flac":
		if d, e := flac.NewDecoder(f); e == nil {
			a.SampleRate, a.Channels = d.SampleRate, d.NChannels
			if d.SampleRate > 0 {
				a.Duration = time.Duration(d.TotalSamples * int64(time.Second) / int64(d.SampleRate))
			}
		} else {
			durErr = e
		}
	case ".ogg", ".oga":
		if r, e := oggvorbis.NewReader(f); e == nil {
			a.SampleRate, a.Channels = r.SampleRate(), r.Channels()
			if a.SampleRate > 0 {
				a.Duration = time.Duration(r.Length() * int64(time.Second) / int64(a.SampleRate))
			}
		} else {
			durErr = e
		}
	case ".wav":
		if w, e := ReadWAV(f); e == nil {
			a.SampleRate, a.Channels, a.Duration = w.SampleRate, w.Channels, w.Duration()
			a.Format = "WAV"
		} else {
			durErr = e
		}
	case ".m4a", ".m4b", ".mp4": // raw ADTS .aac is not an MP4 container
		if v, e := ReadVideo(path); e == nil {
			a.Duration = v.Duration
		} else {
			durErr = e
		}
	}
	if a.Duration > 0 && st != nil {
		a.BitrateKbps = int(float64(st.Size()) * 8 / a.Duration.Seconds() / 1000)
	}
	if tagErr != nil && durErr != nil && a.Duration == 0 {
		return a, tagErr
	}
	return a, nil
}

func extOf(p string) string {
	if i := strings.LastIndexByte(p, '.'); i >= 0 && !strings.ContainsRune(p[i:], '/') {
		return p[i:]
	}
	return ""
}
