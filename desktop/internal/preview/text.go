package preview

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const maxTextBytes = 64 << 10

var textExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".csv": true, ".tsv": true, ".json": true, ".xml": true,
	".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true, ".conf": true, ".log": true,
	".html": true, ".htm": true, ".css": true, ".js": true, ".ts": true, ".go": true, ".py": true,
	".rs": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true, ".java": true, ".sh": true,
	".sql": true, ".rb": true, ".php": true, ".tex": true, ".rst": true, ".srt": true, ".gitignore": true,
}

// TextHandler shows the beginning of text-like files (never the whole file).
type TextHandler struct{}

func (TextHandler) CanHandle(mime, ext string) bool {
	if textExts[strings.ToLower(ext)] {
		return true
	}
	return strings.HasPrefix(mime, "text/") || strings.Contains(mime, "json") ||
		strings.Contains(mime, "xml") || strings.Contains(mime, "yaml") || strings.Contains(mime, "javascript")
}

func (TextHandler) CreatePreview(path string) (Preview, error) {
	f, err := os.Open(path)
	if err != nil {
		return Preview{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Preview{}, err
	}
	buf := make([]byte, maxTextBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Preview{}, err
	}
	buf = buf[:n]
	truncated := st.Size() > int64(n)
	if truncated { // cut at a rune boundary
		for len(buf) > 0 && !utf8.Valid(buf) && len(buf) > n-4 {
			buf = buf[:len(buf)-1]
		}
	}
	// UTF-16 with a byte-order mark contains NULs by design: decode it before
	// the binary check. A UTF-8 BOM is just dropped.
	if len(buf) >= 2 && (buf[0] == 0xFF && buf[1] == 0xFE || buf[0] == 0xFE && buf[1] == 0xFF) {
		buf = []byte(decodeUTF16(buf))
	} else {
		buf = bytes.TrimPrefix(buf, []byte{0xEF, 0xBB, 0xBF})
	}
	probe := buf
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	for _, b := range probe {
		if b == 0 {
			return Preview{}, errors.New("file looks binary")
		}
	}
	if !utf8.Valid(buf) {
		buf = []byte(strings.ToValidUTF8(string(buf), "�"))
	}
	p := Preview{Kind: KindText, Text: string(buf), Truncated: truncated,
		Fields: []Field{{"Lines (shown)", fmt.Sprint(lineCount(buf))}}}
	if truncated {
		p.Notes = append(p.Notes, fmt.Sprintf("Showing the first %d KiB of %d KiB", len(buf)>>10, st.Size()>>10))
	}
	return p, nil
}

// decodeUTF16 decodes UTF-16 text that starts with a byte-order mark; a
// trailing odd byte (a cut-off read) is ignored.
func decodeUTF16(b []byte) string {
	order := binary.ByteOrder(binary.LittleEndian)
	if b[0] == 0xFE {
		order = binary.BigEndian
	}
	b = b[2:]
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, order.Uint16(b[i:]))
	}
	return string(utf16.Decode(u))
}

// lineCount counts the lines of b: a final line without a newline counts, a
// trailing newline does not start a new one.
func lineCount(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte{'\n'})
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}
