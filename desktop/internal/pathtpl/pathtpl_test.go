package pathtpl

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func lookup(m map[string]string) Lookup {
	return func(n string) (string, bool) { v, ok := m[n]; return v, ok }
}

func TestValidate(t *testing.T) {
	known := []string{"year", "camera", "namespace", "name_prefix"}
	good := []string{
		"{year}/{tag}/{filename}",
		"{tag}/{camera|unknown}/{name}_{year:pad4}{ext}",
		"Fixed/{filename}",
		"{camera:slug}/{filename}",
	}
	for _, g := range good {
		if err := Validate(g, known); err != nil {
			t.Errorf("%s: %v", g, err)
		}
	}
	bad := []string{
		"", "/abs/{filename}", "{year}/{tag}", "{nope}/{filename}", "{year/{filename}",
		"../{filename}", "{filename}/x/{name}", "{year:bogus}/{filename}", "{}/{filename}", "{camera|a/b}/{filename}",
		// user variables that merely start with "name" are not the file name
		"{tag}/{namespace}", "{tag}/{name_prefix}_{year}",
	}
	for _, b := range bad {
		if err := Validate(b, known); err == nil {
			t.Errorf("expected error for %q", b)
		}
	}
}

func TestRender(t *testing.T) {
	l := lookup(map[string]string{
		"year": "2021", "tag": "Work/Invoices", "filename": "a b.pdf", "name": "a b", "ext": ".pdf",
		"client": "Acme Corp/../x", "n": "7",
	})
	cases := map[string]string{
		"{year}/{tag}/{filename}":               "2021/Work/Invoices/a b.pdf",
		"{client:slug}/{filename}":              "acme-corp-x/a b.pdf",
		"{client}/{filename}":                   "Acme Corp_.._x/a b.pdf",
		"{missing|none}/{filename}":             "none/a b.pdf",
		"{n:pad3}/{missing:upper|x}/{filename}": "007/X/a b.pdf",
		"{name}-{year}{ext}":                    "a b-2021.pdf",
	}
	for tpl, want := range cases {
		parts, err := Render(tpl, l)
		if err != nil {
			t.Errorf("%s: %v", tpl, err)
			continue
		}
		if got := strings.Join(parts, "/"); got != want {
			t.Errorf("%s: got %q want %q", tpl, got, want)
		}
	}
}

func TestRenderUnresolvedAndTraversal(t *testing.T) {
	_, err := Render("{year}/{filename}", lookup(map[string]string{"filename": "a"}))
	var ue *UnresolvedError
	if !errors.As(err, &ue) || ue.Name != "year" || !errors.Is(err, ErrUnresolved) {
		t.Fatalf("%v", err)
	}
	// a value of ".." must not survive as its own component
	parts, err := Render("{v}/{filename}", lookup(map[string]string{"v": "..", "filename": "a"}))
	if err == nil && reflect.DeepEqual(parts[:1], []string{".."}) {
		t.Fatalf("traversal: %v", parts)
	}
	// a tag destination with .. is rejected
	if _, err := Render("{tag}/{filename}", lookup(map[string]string{"tag": "a/../..", "filename": "f"})); err == nil {
		t.Fatal("expected error for tag with ..")
	}
}

func TestEmptyBuiltinValueIsAllowed(t *testing.T) {
	parts, err := Render("{name}{ext}", lookup(map[string]string{"name": "noext", "ext": ""}))
	if err != nil || strings.Join(parts, "/") != "noext" {
		t.Fatalf("%v %v", parts, err)
	}
	if _, err := Render("{x}/{filename}", lookup(map[string]string{"filename": "a"})); err == nil {
		t.Fatal("missing variable must be unresolved")
	}
}

func TestEmptyLastComponentIsAnError(t *testing.T) {
	// {name} empty (e.g. a dotfile split wrongly) must not turn "{tag}/{name}"
	// into just "{tag}".
	if parts, err := Render("{tag}/{name}", lookup(map[string]string{"tag": "Work", "name": ""})); err == nil {
		t.Fatalf("expected error, got %v", parts)
	}
	// an empty value in a middle component is still dropped, as before
	parts, err := Render("{c}/{tag}/{filename}", lookup(map[string]string{"c": "", "tag": "Work", "filename": "a"}))
	if err != nil || strings.Join(parts, "/") != "Work/a" {
		t.Fatalf("%v %v", parts, err)
	}
}

func TestComponentLengthLimit(t *testing.T) {
	long := strings.Repeat("a", MaxComponentBytes+1)
	if _, err := Render("{tag}/{filename}", lookup(map[string]string{"tag": "T", "filename": long})); err == nil {
		t.Fatal("over-long component accepted")
	}
	ok := strings.Repeat("a", MaxComponentBytes)
	if _, err := Render("{tag}/{filename}", lookup(map[string]string{"tag": "T", "filename": ok})); err != nil {
		t.Fatal(err)
	}
	// the limit is in bytes, not characters
	multi := strings.Repeat("日", MaxComponentBytes/3+1)
	if _, err := Render("{tag}/{filename}", lookup(map[string]string{"tag": "T", "filename": multi})); err == nil {
		t.Fatal("byte length not enforced")
	}
}

func TestSlugKeepsUnicodeLettersAndPadCountsRunes(t *testing.T) {
	if got := applyFormat("Café Ünï 日本語!", "slug"); got != "café-ünï-日本語" {
		t.Fatalf("%q", got)
	}
	if got := applyFormat("日本", "pad4"); got != "00日本" {
		t.Fatalf("%q", got)
	}
}
