// Package destination computes where classified files go and whether the
// destination already holds them. It has no GUI dependency and never writes.
package destination

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sortie/internal/pathtpl"
	"sortie/internal/tags"
)

// DefaultTemplate is used when neither the tag nor the resolver sets a template.
const DefaultTemplate = "{tag}/{filename}"

// ErrUnresolved means a variable used by the template has no value and no
// default; it wraps pathtpl.ErrUnresolved.
var ErrUnresolved = pathtpl.ErrUnresolved

// Resolver turns (file, variables, tag) into a destination path under Root.
type Resolver struct {
	Root     string   // absolute destination root
	Template string   // default template, e.g. {year}/{tag}/{filename}; a tag's own template wins
	VarNames []string // user-defined variable names templates may use
}

// ValidateTemplate checks a template against the built-in variables and names.
func ValidateTemplate(t string, names []string) error { return pathtpl.Validate(t, names) }

// ValidateRoot accepts "" (not configured yet) or an absolute path.
func ValidateRoot(root string) error {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	if strings.ContainsRune(root, 0) {
		return errors.New("destination root contains an invalid character")
	}
	if !filepath.IsAbs(root) {
		return errors.New("destination root must be an absolute path")
	}
	return nil
}

// CheckRoot returns an error unless Root can receive copies right now: configured,
// absolute, existing and a directory. The pre-copy Checker and the copy engine both
// call it, so a destination shown as ready cannot then fail on its root.
func (r Resolver) CheckRoot() error {
	if strings.TrimSpace(r.Root) == "" {
		return errors.New("destination root is not configured")
	}
	if err := ValidateRoot(r.Root); err != nil {
		return err
	}
	st, err := os.Stat(r.Root)
	if err != nil {
		return fmt.Errorf("destination root %s is not available (is the drive mounted?): %w", r.Root, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("destination root %s is not a directory", r.Root)
	}
	return nil
}

// TemplateFor returns the template that applies to tg.
func (r Resolver) TemplateFor(tg tags.Tag) string {
	if t := strings.TrimSpace(tg.DestinationTemplate); t != "" {
		return t
	}
	if t := strings.TrimSpace(r.Template); t != "" {
		return t
	}
	return DefaultTemplate
}

// Resolve returns the absolute destination path for one tag. vars holds the
// resolved user variables by name (unresolved ones are absent). The result is
// guaranteed to lie inside Root (also when existing directories along the way
// are symlinks). A variable without value or default yields an error wrapping
// ErrUnresolved.
func (r Resolver) Resolve(name string, vars map[string]string, tg tags.Tag) (string, error) {
	if strings.TrimSpace(r.Root) == "" {
		return "", errors.New("destination root is not configured")
	}
	if err := ValidateRoot(r.Root); err != nil {
		return "", err
	}
	tpl := r.TemplateFor(tg)
	if err := ValidateTemplate(tpl, r.VarNames); err != nil {
		return "", err
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\x00") {
		return "", fmt.Errorf("invalid file name %q", name)
	}
	if err := tags.ValidateDestination(tg.Destination); err != nil {
		return "", fmt.Errorf("tag %q: %w", tg.Name, err)
	}
	ext := filepath.Ext(name)
	if ext == name { // dotfile such as ".bashrc": the whole name, no extension
		ext = ""
	}
	stem := strings.TrimSuffix(name, ext)
	tagDir := strings.ReplaceAll(strings.TrimSpace(tg.Destination), `\`, "/")
	parts, err := pathtpl.Render(tpl, func(n string) (string, bool) {
		switch n {
		case "tag":
			return tagDir, true
		case "filename":
			return name, true
		case "name":
			return stem, true
		case "ext":
			return ext, true
		}
		v, ok := vars[n]
		return v, ok
	})
	if err != nil {
		return "", err
	}
	root := filepath.Clean(r.Root)
	full := filepath.Join(append([]string{root}, parts...)...)
	if !within(root, full) {
		return "", errors.New("destination escapes the destination root")
	}
	if err := checkSymlinks(root, full); err != nil {
		return "", err
	}
	return full, nil
}

// within reports whether path is root or below it (lexically).
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// checkSymlinks verifies that the deepest existing part of path still lies
// inside root after resolving symlinks.
func checkSymlinks(root, path string) error {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil // root does not exist yet: nothing can escape
	}
	p := path
	for {
		if _, err := os.Lstat(p); err == nil {
			break
		}
		parent := filepath.Dir(p)
		if parent == p {
			return nil
		}
		p = parent
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return fmt.Errorf("cannot resolve %s: %w", p, err)
	}
	if !within(realRoot, real) {
		return fmt.Errorf("%s leads outside the destination root (symbolic link)", p)
	}
	return nil
}
