package profile

import "testing"

func TestTagColorRoundTripAndValidation(t *testing.T) {
	p := New("c")
	p.Tags = []Tag{{Name: "A", Shortcut: "1", Color: "#E4572E"}, {Name: "B", Shortcut: "2"}}
	ts := p.ToTags(nil)
	if ts[0].Color != "#E4572E" || ts[1].Color != "" {
		t.Fatalf("ToTags colors: %q %q", ts[0].Color, ts[1].Color)
	}
	if back := FromTags(ts); back[0].Color != "#E4572E" {
		t.Fatalf("FromTags lost color: %q", back[0].Color)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("valid color rejected: %v", err)
	}
	p.Tags[0].Color = "red"
	if err := p.Validate(); err == nil {
		t.Fatal("bad color accepted")
	}
}
