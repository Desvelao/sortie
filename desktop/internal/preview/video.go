package preview

import (
	"fmt"
	"strings"

	"sortie/internal/media"
)

// VideoHandler shows container metadata. Frame previews would need a video
// decoder, which is out of scope for the pure-Go build; playback is a future
// enhancement (use "open with default application").
type VideoHandler struct{}

func (VideoHandler) CanHandle(mime, ext string) bool {
	switch strings.ToLower(ext) {
	case ".mp4", ".mkv", ".avi", ".mov", ".webm", ".m4v", ".3gp":
		return true
	}
	return strings.HasPrefix(mime, "video/")
}

func (VideoHandler) CreatePreview(path string) (Preview, error) {
	p := Preview{Kind: KindVideo}
	v, err := media.ReadVideo(path)
	if err != nil {
		p.Notes = append(p.Notes, "Video metadata unavailable: "+err.Error())
		return p, nil // still a valid (empty) preview: the file stays classifiable
	}
	p.Fields = append(p.Fields, Field{"Container", v.Container})
	if d := FormatDuration(v.Duration); d != "" {
		p.Fields = append(p.Fields, Field{"Duration", d})
	}
	if v.Width > 0 && v.Height > 0 {
		p.Fields = append(p.Fields, Field{"Resolution", fmt.Sprintf("%d × %d", v.Width, v.Height)})
	}
	if !v.Created.IsZero() && v.Created.Year() > 1970 {
		p.Fields = append(p.Fields, Field{"Created", v.Created.Format("2006-01-02 15:04")})
	}
	p.Notes = append(p.Notes, "No frame preview; use “Open with default application”")
	return p, nil
}
