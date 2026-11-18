package preview

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"sortie/internal/media"
)

// PDFHandler shows PDF metadata, page count and a first-page text excerpt.
// Page rendering would need a native rasteriser (not available as a
// permissively licensed pure-Go library), so the UI offers "open with the
// default application" for visual inspection.
type PDFHandler struct{}

func (PDFHandler) CanHandle(mime, ext string) bool {
	return strings.EqualFold(ext, ".pdf") || mime == "application/pdf"
}

func (PDFHandler) CreatePreview(path string) (Preview, error) {
	info, err := media.ReadPDF(path, true)
	if err != nil {
		return Preview{}, fmt.Errorf("cannot read PDF: %w", err)
	}
	p := Preview{Kind: KindPDF}
	add := func(l, v string) {
		if v = strings.TrimSpace(v); v != "" && utf8.ValidString(v) {
			p.Fields = append(p.Fields, Field{l, v})
		}
	}
	add("Pages", fmt.Sprint(info.Pages))
	add("Title", info.Title)
	add("Author", info.Author)
	add("Subject", info.Subject)
	add("Creator", info.Creator)
	add("Producer", info.Producer)
	if !info.Created.IsZero() {
		add("Created", info.Created.Format("2006-01-02 15:04"))
	}
	if t := info.FirstPageText; t != "" {
		if len(t) > 4000 {
			t = strings.ToValidUTF8(t[:4000], "") + "…"
		}
		p.Text = t
	} else {
		p.Notes = append(p.Notes, "No extractable text on the first page")
	}
	return p, nil
}
