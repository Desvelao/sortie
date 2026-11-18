package media

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
)

// PDFInfo is document metadata plus a text excerpt of the first page.
type PDFInfo struct {
	Pages                                     int
	Title, Author, Subject, Creator, Producer string
	Created                                   time.Time
	FirstPageText                             string
}

const maxPDFBytes = 256 << 20

// ReadPDF extracts metadata; withText also extracts the first page's text.
func ReadPDF(path string, withText bool) (info PDFInfo, err error) {
	defer recoverTo(&err, "PDF")
	f, err := os.Open(path)
	if err != nil {
		return info, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return info, err
	}
	if st.Size() > maxPDFBytes {
		return info, errTooLarge
	}
	r, err := pdf.NewReader(f, st.Size())
	if err != nil {
		return info, err
	}
	info.Pages = r.NumPage()
	meta := r.Trailer().Key("Info")
	info.Title = meta.Key("Title").Text()
	info.Author = meta.Key("Author").Text()
	info.Subject = meta.Key("Subject").Text()
	info.Creator = meta.Key("Creator").Text()
	info.Producer = meta.Key("Producer").Text()
	info.Created = ParsePDFDate(meta.Key("CreationDate").Text())
	if withText && info.Pages > 0 {
		info.FirstPageText = firstPageText(r)
	}
	return info, nil
}

// firstPageText extracts the first page's text. Malformed content streams make
// the PDF library panic; that must only lose the text, not the metadata
// already read, so it has its own recover.
func firstPageText(r *pdf.Reader) string {
	return safeText(func() (string, error) { return r.Page(1).GetPlainText(nil) })
}

// safeText runs extract and returns its trimmed text, or "" if it fails or panics.
func safeText(extract func() (string, error)) (text string) {
	defer func() {
		if recover() != nil {
			text = ""
		}
	}()
	txt, err := extract()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(txt)
}

// ParsePDFDate parses "D:YYYYMMDDHHmmSS..." (any prefix of it); zero if invalid.
func ParsePDFDate(s string) time.Time {
	s = strings.TrimPrefix(strings.TrimSpace(s), "D:")
	num := func(from, n, def int) int {
		if len(s) < from+n {
			return def
		}
		v, err := strconv.Atoi(s[from : from+n])
		if err != nil {
			return -1
		}
		return v
	}
	y, mo, d := num(0, 4, -1), num(4, 2, 1), num(6, 2, 1)
	h, mi, sec := num(8, 2, 0), num(10, 2, 0), num(12, 2, 0)
	if y < 0 || mo < 1 || mo > 12 || d < 1 || d > 31 || h < 0 || h > 23 || mi < 0 || mi > 59 || sec < 0 || sec > 60 {
		return time.Time{}
	}
	return time.Date(y, time.Month(mo), d, h, mi, sec, 0, time.UTC)
}
