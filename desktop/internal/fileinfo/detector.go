package fileinfo

import (
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const unknownMIME = "application/octet-stream"

// Extension returns the lower-case extension of name (with dot). A name whose
// only dot is the leading one (".bashrc", ".gitignore") has no extension.
func Extension(name string) string {
	base := filepath.Base(name)
	ext := filepath.Ext(base)
	if ext == base {
		return ""
	}
	return strings.ToLower(ext)
}

// DetectMIME determines the MIME type of the file at path. Content sniffing
// takes precedence over the extension when it yields something specific, so
// misleading extensions are handled. Never fails: unknown files get
// application/octet-stream.
func DetectMIME(path string) string {
	byExt := mime.TypeByExtension(Extension(path))
	if f, err := os.Open(path); err == nil {
		defer f.Close()
		buf := make([]byte, 512)
		n, _ := io.ReadFull(f, buf)
		if n > 0 {
			sniffed := stripParams(http.DetectContentType(buf[:n]))
			if sniffed != unknownMIME && !(strings.HasPrefix(sniffed, "text/plain") && textual(byExt)) {
				return sniffed
			}
		}
	}
	if byExt != "" {
		return stripParams(byExt)
	}
	return unknownMIME
}

func stripParams(m string) string {
	if i := strings.Index(m, ";"); i >= 0 {
		m = m[:i]
	}
	return strings.TrimSpace(m)
}

// textual reports whether an extension-derived type is a text-based format
// that content sniffing cannot distinguish from plain text.
func textual(m string) bool {
	m = stripParams(m)
	return strings.HasPrefix(m, "text/") || strings.Contains(m, "json") ||
		strings.Contains(m, "xml") || strings.Contains(m, "javascript") || strings.Contains(m, "yaml")
}
