package preview

import (
	"fmt"
	"strings"
	"time"

	"sortie/internal/media"
	"sortie/internal/player"
)

// AudioHandler shows tags and technical data; playback is offered when the
// built-in player supports the format.
type AudioHandler struct{}

func (AudioHandler) CanHandle(mime, ext string) bool {
	switch strings.ToLower(ext) {
	case ".mp3", ".flac", ".wav", ".ogg", ".oga", ".m4a", ".m4b", ".aac":
		return true
	}
	return strings.HasPrefix(mime, "audio/")
}

func (AudioHandler) CreatePreview(path string) (Preview, error) {
	a, err := media.ReadAudio(path)
	p := Preview{Kind: KindAudio, Playable: player.Supported(extOf(path))}
	if err != nil {
		// Like VideoHandler: an unreadable audio file (raw .aac, untagged
		// exotic formats) still gets a valid preview, so it stays classifiable.
		if f := strings.ToUpper(strings.TrimPrefix(extOf(path), ".")); f != "" {
			p.Fields = append(p.Fields, Field{"Format", f})
		}
		p.Notes = append(p.Notes, "Audio metadata unavailable: "+err.Error())
		if !p.Playable {
			p.Notes = append(p.Notes, "Built-in playback supports MP3, FLAC, WAV and OGG")
		}
		return p, nil
	}
	add := func(l, v string) {
		if v != "" {
			p.Fields = append(p.Fields, Field{l, v})
		}
	}
	add("Title", a.Title)
	add("Artist", a.Artist)
	add("Album", a.Album)
	add("Genre", a.Genre)
	if a.Year > 0 {
		add("Year", fmt.Sprint(a.Year))
	}
	add("Duration", FormatDuration(a.Duration))
	add("Format", a.Format)
	if a.SampleRate > 0 {
		add("Audio", fmt.Sprintf("%d Hz, %d ch", a.SampleRate, a.Channels))
	}
	if a.BitrateKbps > 0 {
		add("Bitrate", fmt.Sprintf("~%d kbps", a.BitrateKbps))
	}
	if !p.Playable {
		p.Notes = append(p.Notes, "Built-in playback supports MP3, FLAC, WAV and OGG")
	}
	return p, nil
}

// FormatDuration renders d as m:ss or h:mm:ss ("" for zero).
func FormatDuration(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	s := int(d.Round(time.Second).Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
