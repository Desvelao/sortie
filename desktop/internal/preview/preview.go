// Package preview builds optional, format-specific previews. It is
// GUI-independent: handlers return plain data (decoded image, text, metadata
// fields). A missing or failing handler never prevents classification.
package preview

import (
	"fmt"
	"image"
	"os"
	"time"
)

// Kind identifies what a preview contains.
type Kind string

const (
	KindGeneric Kind = "generic" // nothing to show beyond file metadata
	KindImage   Kind = "image"
	KindSVG     Kind = "svg"
	KindText    Kind = "text"
	KindPDF     Kind = "pdf"
	KindAudio   Kind = "audio"
	KindVideo   Kind = "video"
)

// Field is one label/value line of format metadata.
type Field struct{ Label, Value string }

// Preview is the result of a handler.
type Preview struct {
	Kind      Kind
	Path      string
	Image     image.Image // downscaled for display (KindImage)
	Text      string      // KindText, or first-page excerpt for KindPDF
	Truncated bool
	Fields    []Field
	Notes     []string // e.g. why a preview is unavailable
	Playable  bool     // audio that the built-in player can play
}

// PreviewHandler is implemented by every preview module (PLAN §10).
type PreviewHandler interface {
	CanHandle(mimeType string, extension string) bool
	CreatePreview(path string) (Preview, error)
}

// Registry dispatches to the first handler that accepts a file.
type Registry struct {
	handlers []PreviewHandler
	Timeout  time.Duration
	inflight chan struct{} // bounds handlers still running (timed-out ones keep running)
}

// maxInflight is how many handlers may run at once; a handler that timed out
// cannot be cancelled and holds its slot until it returns.
const maxInflight = 8

// NewRegistry builds a registry; order decides priority.
func NewRegistry(h ...PreviewHandler) *Registry {
	return &Registry{handlers: h, Timeout: 15 * time.Second, inflight: make(chan struct{}, maxInflight)}
}

// Default returns the registry with all built-in handlers.
func Default() *Registry {
	return NewRegistry(ImageHandler{}, PDFHandler{}, AudioHandler{}, VideoHandler{}, TextHandler{})
}

// Register adds a handler with the lowest priority.
func (r *Registry) Register(h PreviewHandler) { r.handlers = append(r.handlers, h) }

// Create never fails: panics, errors and timeouts degrade to a generic
// preview carrying an explanatory note.
func (r *Registry) Create(path, mimeType, ext string) Preview {
	generic := Preview{Kind: KindGeneric, Path: path}
	// Opening a FIFO or device node would block forever (and leak the handler
	// goroutine), so only regular files are previewed. A path that cannot be
	// stat'ed is left to the handler, which reports the real error.
	if st, err := os.Stat(path); err == nil && !st.Mode().IsRegular() {
		generic.Notes = []string{"Preview skipped: not a regular file"}
		return generic
	}
	for _, h := range r.handlers {
		if !h.CanHandle(mimeType, ext) {
			continue
		}
		select {
		case r.inflight <- struct{}{}:
		default:
			generic.Notes = []string{"Preview unavailable: too many previews are still running"}
			return generic
		}
		type result struct {
			p   Preview
			err error
		}
		ch := make(chan result, 1)
		go func() {
			defer func() { <-r.inflight }()
			defer func() {
				if v := recover(); v != nil {
					ch <- result{err: fmt.Errorf("preview handler crashed: %v", v)}
				}
			}()
			p, err := h.CreatePreview(path)
			ch <- result{p, err}
		}()
		select {
		case res := <-ch:
			if res.err != nil {
				generic.Notes = []string{"Preview unavailable: " + res.err.Error()}
				return generic
			}
			res.p.Path = path
			return res.p
		case <-time.After(r.Timeout):
			generic.Notes = []string{"Preview unavailable: timed out"}
			return generic
		}
	}
	return generic
}
