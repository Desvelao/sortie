// Package pathtpl parses and renders destination path templates such as
// "{tag}/{year}/{camera|unknown}/{filename}". Placeholders:
//
//	{name}            value of a built-in or user-defined variable
//	{name:format}     with a format: upper, lower, slug, padN (zero-pad to N)
//	{name|default}    literal default when the variable is unresolved
//	{name:fmt|dflt}   both
//
// Rendered values are sanitized into single path components, so a value can
// never add or remove directory levels or contain "..".
package pathtpl

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrUnresolved is wrapped by Render errors when a variable has no value and
// no default.
var ErrUnresolved = errors.New("variable cannot be determined")

// UnresolvedError names the variable that could not be resolved.
type UnresolvedError struct{ Name string }

func (e *UnresolvedError) Error() string {
	return fmt.Sprintf("variable {%s} cannot be determined", e.Name)
}
func (e *UnresolvedError) Unwrap() error { return ErrUnresolved }

// Builtins are always available. {filename} may only appear in the last path
// component; {tag} is the tag's name.
var Builtins = []string{"tag", "filename", "name", "ext", "parent"}

var placeholderRe = regexp.MustCompile(`\{[^{}]*\}`)

type placeholder struct {
	Name, Format, Default string
	HasDefault            bool
}

func parsePlaceholder(raw string) (placeholder, error) {
	body := raw[1 : len(raw)-1]
	var p placeholder
	if i := strings.Index(body, "|"); i >= 0 {
		p.Default, p.HasDefault = body[i+1:], true
		body = body[:i]
	}
	if i := strings.Index(body, ":"); i >= 0 {
		p.Format = body[i+1:]
		body = body[:i]
	}
	p.Name = strings.TrimSpace(body)
	if p.Name == "" {
		return p, fmt.Errorf("empty placeholder %s", raw)
	}
	if strings.ContainsAny(p.Default, "/\\") {
		return p, fmt.Errorf("default in %s must not contain a path separator", raw)
	}
	if err := checkFormat(p.Format); err != nil {
		return p, fmt.Errorf("%s: %w", raw, err)
	}
	return p, nil
}

func checkFormat(f string) error {
	switch {
	case f == "", f == "upper", f == "lower", f == "slug":
		return nil
	case strings.HasPrefix(f, "pad"):
		if n, err := strconv.Atoi(f[3:]); err == nil && n > 0 && n <= 32 {
			return nil
		}
	}
	return fmt.Errorf("unknown format %q (use upper, lower, slug, padN)", f)
}

// Validate checks syntax and that every variable is built in or in known.
func Validate(tpl string, known []string) error {
	t := strings.TrimSpace(tpl)
	if t == "" {
		return errors.New("template is empty")
	}
	if strings.HasPrefix(t, "/") {
		return errors.New("template must be relative (no leading /)")
	}
	if strings.ContainsRune(t, 0) {
		return errors.New("template contains an invalid character")
	}
	ok := map[string]bool{}
	for _, n := range Builtins {
		ok[n] = true
	}
	for _, n := range known {
		ok[n] = true
	}
	for _, raw := range placeholderRe.FindAllString(t, -1) {
		p, err := parsePlaceholder(raw)
		if err != nil {
			return err
		}
		if !ok[p.Name] {
			return fmt.Errorf("unknown variable {%s}", p.Name)
		}
	}
	if strings.ContainsAny(placeholderRe.ReplaceAllString(t, ""), "{}") {
		return errors.New("unbalanced braces in template")
	}
	parts := SplitPath(t)
	if len(parts) == 0 {
		return errors.New("template is empty")
	}
	for i, part := range parts {
		if part == ".." {
			return errors.New("template must not contain '..'")
		}
		if i < len(parts)-1 && strings.Contains(part, "{filename}") {
			return errors.New("{filename} is only allowed in the last path component")
		}
	}
	last := parts[len(parts)-1]
	hasName := false
	for _, raw := range placeholderRe.FindAllString(last, -1) {
		// placeholders were parsed above, so errors cannot occur here
		if p, err := parsePlaceholder(raw); err == nil && (p.Name == "filename" || p.Name == "name") {
			hasName = true
		}
	}
	if !hasName {
		return errors.New("the last path component must contain {filename} or {name}")
	}
	return nil
}

// SplitPath splits on "/" dropping empty and "." components.
func SplitPath(p string) []string {
	var out []string
	for _, s := range strings.Split(p, "/") {
		if s != "" && s != "." {
			out = append(out, s)
		}
	}
	return out
}

// MaxComponentBytes is the longest file or folder name Linux filesystems
// accept (NAME_MAX). Longer rendered components would only fail at copy time.
const MaxComponentBytes = 255

// Lookup returns the value of a variable; ok=false means unresolved (an
// empty value with ok=true is allowed, e.g. {ext} of a file without extension).
type Lookup func(name string) (string, bool)

// Render fills the template and returns the relative path components. The
// template must already be valid.
func Render(tpl string, lookup Lookup) ([]string, error) {
	var renderErr error
	out := placeholderRe.ReplaceAllStringFunc(strings.TrimSpace(tpl), func(raw string) string {
		if renderErr != nil {
			return ""
		}
		p, err := parsePlaceholder(raw)
		if err != nil {
			renderErr = err
			return ""
		}
		v, ok := lookup(p.Name)
		if ok {
			v = strings.TrimSpace(v)
		}
		if !ok {
			if !p.HasDefault {
				renderErr = &UnresolvedError{Name: p.Name}
				return ""
			}
			v = p.Default
		}
		return applyFormat(sanitize(v, p.Name == "tag"), p.Format)
	})
	if renderErr != nil {
		return nil, renderErr
	}
	// SplitPath drops empty components, so an empty last one would silently
	// turn the previous component (e.g. the tag folder) into the file name.
	if segs := strings.Split(out, "/"); strings.TrimSpace(segs[len(segs)-1]) == "" || segs[len(segs)-1] == "." {
		return nil, errors.New("the file name part of the resolved path is empty")
	}
	parts := SplitPath(out)
	if len(parts) == 0 {
		return nil, errors.New("resolved path is empty")
	}
	for _, p := range parts {
		if p == ".." {
			return nil, errors.New("resolved path contains '..'")
		}
		if len(p) > MaxComponentBytes {
			return nil, fmt.Errorf("a path component is %d bytes long (maximum %d): %.40q…", len(p), MaxComponentBytes, p)
		}
	}
	return parts, nil
}

// sanitize makes v safe inside one path component. The tag destination keeps
// "/" because a tag may map to a nested folder (e.g. "Work/Invoices").
func sanitize(v string, keepSlash bool) string {
	v = strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return -1
		case r == '\\':
			return '/'
		}
		return r
	}, v)
	if !keepSlash {
		v = strings.ReplaceAll(v, "/", "_")
	}
	return v
}

func applyFormat(v, f string) string {
	switch {
	case f == "upper":
		return strings.ToUpper(v)
	case f == "lower":
		return strings.ToLower(v)
	case f == "slug":
		var b strings.Builder
		dash := false
		for _, r := range strings.ToLower(v) {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				b.WriteRune(r)
				dash = false
			} else if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
		return strings.Trim(b.String(), "-")
	case strings.HasPrefix(f, "pad"):
		n, _ := strconv.Atoi(f[3:])
		if l := utf8.RuneCountInString(v); l < n {
			return strings.Repeat("0", n-l) + v
		}
	}
	return v
}
