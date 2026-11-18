// Package vars resolves user-defined variables (year, camera, client...) for a
// file from an ordered chain of sources: manual override, filename, path,
// metadata, modification time or a constant. It has no GUI dependency and
// knows nothing about what a variable is used for.
package vars

import (
	"errors"
	"fmt"
	"regexp"
	"regexp/syntax"
	"strconv"
	"strings"
	"time"
)

// Kind identifies a source type.
type Kind string

const (
	KindManual   Kind = "manual"
	KindFilename Kind = "filename"
	KindPath     Kind = "path"
	KindMetadata Kind = "metadata"
	KindMTime    Kind = "mtime"
	KindConstant Kind = "constant"
)

// Status is the outcome of resolving one variable.
type Status string

const (
	StatusResolved   Status = "RESOLVED"
	StatusUnresolved Status = "UNRESOLVED"
	StatusInvalid    Status = "INVALID" // something was found but failed validation
)

// Source is one step of a variable's extraction chain.
type Source struct {
	Kind    Kind   `yaml:"kind" json:"kind"`
	Pattern string `yaml:"pattern,omitempty" json:"pattern,omitempty"` // filename, path
	Group   int    `yaml:"group,omitempty" json:"group,omitempty"`     // capture group (default 1; 0 = whole match when the pattern has no groups)
	Field   string `yaml:"field,omitempty" json:"field,omitempty"`     // metadata
	Format  string `yaml:"format,omitempty" json:"format,omitempty"`   // Go time layout for dates
	Value   string `yaml:"value,omitempty" json:"value,omitempty"`     // constant
}

// Validation restricts accepted values. All fields are optional.
type Validation struct {
	Regex string `yaml:"regex,omitempty" json:"regex,omitempty"`
	Min   *int   `yaml:"min,omitempty" json:"min,omitempty"`
	Max   *int   `yaml:"max,omitempty" json:"max,omitempty"`
}

// Definition declares one variable.
type Definition struct {
	Name     string     `yaml:"name" json:"name"`
	Sources  []Source   `yaml:"sources" json:"sources"`
	Validate Validation `yaml:"validate,omitempty" json:"validate,omitempty"`
	Fallback string     `yaml:"fallback,omitempty" json:"fallback,omitempty"`
}

// FileContext is everything a source may look at. Fields is lazy so metadata
// is only read when a metadata source is reached.
type FileContext struct {
	Path    string // absolute path
	Name    string // base name
	RelDir  string // directory relative to the input root, "/" separated
	ModTime time.Time
	Manual  map[string]string // per-file manual overrides by variable name
	Fields  func() map[string]string
}

// Value is the resolved value of one variable for one file.
type Value struct {
	Value  string
	Source Kind
	Status Status
	Detail string
}

// OK reports whether a usable value exists.
func (v Value) OK() bool { return v.Status == StatusResolved }

type compiledSource struct {
	Source
	re *regexp.Regexp
}

type compiled struct {
	def     Definition
	sources []compiledSource
	valRe   *regexp.Regexp
}

// Set is a validated group of variable definitions.
type Set struct {
	vars  []compiled
	names []string
}

var nameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)

// Reserved names are provided by the template engine itself.
var Reserved = map[string]bool{"tag": true, "filename": true, "name": true, "ext": true, "parent": true}

func compileRe(p string) (*regexp.Regexp, error) {
	re, err := regexp.Compile(p)
	if err != nil {
		msg := err.Error()
		var pe *syntax.Error
		if errors.As(err, &pe) {
			msg = fmt.Sprintf("%s: `%s`", pe.Code, pe.Expr)
		}
		return nil, fmt.Errorf("invalid regular expression: %s", msg)
	}
	return re, nil
}

// New validates and compiles definitions.
func New(defs []Definition) (*Set, error) {
	s := &Set{}
	seen := map[string]bool{}
	for _, d := range defs {
		if !nameRe.MatchString(d.Name) {
			return nil, fmt.Errorf("variable name %q is invalid (letters, digits, _; must start with a letter)", d.Name)
		}
		if Reserved[d.Name] {
			return nil, fmt.Errorf("variable name %q is reserved", d.Name)
		}
		if seen[d.Name] {
			return nil, fmt.Errorf("variable %q is defined twice", d.Name)
		}
		seen[d.Name] = true
		c := compiled{def: d}
		if d.Validate.Regex != "" {
			re, err := compileRe(d.Validate.Regex)
			if err != nil {
				return nil, fmt.Errorf("variable %q validate: %w", d.Name, err)
			}
			c.valRe = re
		}
		if d.Validate.Min != nil && d.Validate.Max != nil && *d.Validate.Min > *d.Validate.Max {
			return nil, fmt.Errorf("variable %q: min is greater than max", d.Name)
		}
		if len(d.Sources) == 0 && d.Fallback == "" {
			return nil, fmt.Errorf("variable %q has no sources and no fallback", d.Name)
		}
		for i, src := range d.Sources {
			cs := compiledSource{Source: src}
			switch src.Kind {
			case KindManual, KindMTime:
			case KindFilename, KindPath:
				if strings.TrimSpace(src.Pattern) == "" {
					return nil, fmt.Errorf("variable %q source %d (%s): pattern is empty", d.Name, i+1, src.Kind)
				}
				re, err := compileRe(src.Pattern)
				if err != nil {
					return nil, fmt.Errorf("variable %q source %d: %w", d.Name, i+1, err)
				}
				g := src.Group
				if g == 0 && re.NumSubexp() > 0 {
					g = 1
					cs.Group = 1
				}
				if g < 0 || g > re.NumSubexp() {
					return nil, fmt.Errorf("variable %q source %d: capture group %d does not exist (the expression has %d group(s))", d.Name, i+1, g, re.NumSubexp())
				}
				cs.re = re
			case KindMetadata:
				if strings.TrimSpace(src.Field) == "" {
					return nil, fmt.Errorf("variable %q source %d: metadata field is empty", d.Name, i+1)
				}
			case KindConstant:
				if src.Value == "" {
					return nil, fmt.Errorf("variable %q source %d: constant value is empty", d.Name, i+1)
				}
			default:
				return nil, fmt.Errorf("variable %q source %d: unknown kind %q", d.Name, i+1, src.Kind)
			}
			c.sources = append(c.sources, cs)
		}
		s.vars = append(s.vars, c)
		s.names = append(s.names, d.Name)
	}
	return s, nil
}

// Names returns the variable names in definition order.
func (s *Set) Names() []string { return append([]string(nil), s.names...) }

// Has reports whether name is defined.
func (s *Set) Has(name string) bool {
	for _, n := range s.names {
		if n == name {
			return true
		}
	}
	return false
}

// ResolveAll resolves every variable for a file.
func (s *Set) ResolveAll(fc FileContext) map[string]Value {
	out := make(map[string]Value, len(s.vars))
	for i := range s.vars {
		out[s.vars[i].def.Name] = s.vars[i].resolve(fc)
	}
	return out
}

// Resolve resolves one variable by name.
func (s *Set) Resolve(name string, fc FileContext) (Value, bool) {
	for i := range s.vars {
		if s.vars[i].def.Name == name {
			return s.vars[i].resolve(fc), true
		}
	}
	return Value{}, false
}

// check validates a candidate value; ok=false carries a reason.
func (c *compiled) check(v string) (string, bool, string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", false, "empty value"
	}
	if c.valRe != nil && !c.valRe.MatchString(v) {
		return v, false, fmt.Sprintf("%q does not match %s", v, c.def.Validate.Regex)
	}
	if c.def.Validate.Min != nil || c.def.Validate.Max != nil {
		n, err := strconv.Atoi(v)
		if err != nil {
			return v, false, fmt.Sprintf("%q is not a number", v)
		}
		if c.def.Validate.Min != nil && n < *c.def.Validate.Min {
			return v, false, fmt.Sprintf("%d is below %d", n, *c.def.Validate.Min)
		}
		if c.def.Validate.Max != nil && n > *c.def.Validate.Max {
			return v, false, fmt.Sprintf("%d is above %d", n, *c.def.Validate.Max)
		}
	}
	return v, true, ""
}

func (c *compiled) resolve(fc FileContext) Value {
	var firstBad *Value
	note := func(kind Kind, detail string) {
		if firstBad == nil {
			firstBad = &Value{Source: kind, Status: StatusInvalid, Detail: detail}
		}
	}
	try := func(kind Kind, cand string) (Value, bool) {
		v, ok, why := c.check(cand)
		if ok {
			return Value{Value: v, Source: kind, Status: StatusResolved}, true
		}
		if v != "" {
			note(kind, why)
		}
		return Value{}, false
	}
	for _, src := range c.sources {
		switch src.Kind {
		case KindManual:
			if m, ok := fc.Manual[c.def.Name]; ok && strings.TrimSpace(m) != "" {
				// a manual value is the user's explicit choice: not range-checked
				return Value{Value: strings.TrimSpace(m), Source: KindManual, Status: StatusResolved}
			}
		case KindConstant:
			if v, ok := try(KindConstant, src.Value); ok {
				return v
			}
		case KindFilename:
			if v, ok := matchAll(c, src, fc.Name, try); ok {
				return v
			}
		case KindPath:
			if v, ok := matchAll(c, src, fc.RelDir, try); ok {
				return v
			}
		case KindMetadata:
			if fc.Fields == nil {
				continue
			}
			raw, ok := fc.Fields()[strings.ToLower(src.Field)]
			if !ok {
				continue
			}
			if v, ok := try(KindMetadata, formatDate(raw, src.Format)); ok {
				return v
			}
		case KindMTime:
			if fc.ModTime.IsZero() {
				continue
			}
			layout := src.Format
			if layout == "" {
				layout = "2006"
			}
			if v, ok := try(KindMTime, fc.ModTime.Format(layout)); ok {
				return v
			}
		}
	}
	if firstBad != nil && c.def.Fallback == "" {
		return *firstBad
	}
	if c.def.Fallback != "" {
		detail := "no source matched"
		if firstBad != nil {
			detail = firstBad.Detail
		}
		return Value{Value: c.def.Fallback, Source: KindConstant, Status: StatusResolved, Detail: "fallback: " + detail}
	}
	return Value{Status: StatusUnresolved, Detail: "no source matched"}
}

// matchAll scans every regexp match in s; the first value that validates wins.
func matchAll(c *compiled, src compiledSource, s string, try func(Kind, string) (Value, bool)) (Value, bool) {
	if s == "" {
		return Value{}, false
	}
	pos := 0
	for pos <= len(s) {
		loc := src.re.FindStringSubmatchIndex(s[pos:])
		if loc == nil {
			break
		}
		st, en := loc[2*src.Group], loc[2*src.Group+1]
		next := pos + loc[1]
		if st >= 0 {
			if v, ok := try(src.Kind, s[pos+st:pos+en]); ok {
				return v, true
			}
			next = pos + en
		}
		if next <= pos {
			next = pos + 1
		}
		pos = next
	}
	return Value{}, false
}

var dateLayouts = []string{
	time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006:01:02 15:04:05",
	"2006-01-02", "2006:01:02", "2006/01/02", "2006-01", "2006",
}

// formatDate reparses raw as a date and renders it with layout. Without a
// layout, or when raw is not a date, raw is returned unchanged.
func formatDate(raw, layout string) string {
	if layout == "" {
		return raw
	}
	raw = strings.TrimSpace(raw)
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, raw); err == nil {
			return t.Format(layout)
		}
	}
	return raw
}

// Definition returns the definition of a variable.
func (s *Set) Definition(name string) (Definition, bool) {
	for i := range s.vars {
		if s.vars[i].def.Name == name {
			return s.vars[i].def, true
		}
	}
	return Definition{}, false
}

// Check reports whether value would be accepted for variable name (its
// validation rules), for checking manual input before it is stored.
func (s *Set) Check(name, value string) error {
	for i := range s.vars {
		if s.vars[i].def.Name != name {
			continue
		}
		if _, ok, why := s.vars[i].check(value); !ok {
			return errors.New(why)
		}
		return nil
	}
	return fmt.Errorf("unknown variable %q", name)
}
