package ui

import (
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/app"
	"sortie/internal/config"
)

// labelTexts collects the texts of all labels below o (not looking inside
// scroll containers or widget renderers).
func labelTexts(o fyne.CanvasObject) []string {
	var out []string
	switch v := o.(type) {
	case *widget.Label:
		out = append(out, v.Text)
	case *fyne.Container:
		for _, c := range v.Objects {
			out = append(out, labelTexts(c)...)
		}
	}
	return out
}

func hasText(o fyne.CanvasObject, sub string) bool {
	for _, t := range labelTexts(o) {
		if strings.Contains(t, sub) {
			return true
		}
	}
	return false
}

func TestShortcutsTabChecksAgainstEditedTagKeys(t *testing.T) {
	a := test.NewApp()
	t.Cleanup(a.Quit)
	mw := NewMainWindow(a, app.New(), config.Default(), filepath.Join(t.TempDir(), "config.json"))
	mw.cfg.Shortcuts["next"] = "N" // a plain key

	keys := []string{"1"}
	tab, _, recheck := mw.buildShortcutsTab(func() []string { return keys })
	if !hasText(tab, "✓ valid") {
		t.Fatalf("expected valid, got %v", labelTexts(tab))
	}
	keys = []string{"1", "N"} // the user types N into a tag's shortcut, unsaved
	recheck()
	if !hasText(tab, "✗") {
		t.Fatalf("conflict with an edited tag key not reported: %v", labelTexts(tab))
	}
}

func TestRemovedTagsWarning(t *testing.T) {
	mw, core := newWindowWithFiles(t, "a_2020.txt", "b_2020.txt")
	typeKey(mw.Window(), "1") // a gets the first tag
	all := core.Tags()
	saved := map[int]string{}
	for _, tg := range all {
		saved[tg.ID] = tg.Name
	}
	if w := mw.removedTagsWarning(saved, all); w != "" {
		t.Fatalf("nothing removed, got %q", w)
	}
	w := mw.removedTagsWarning(saved, all[1:]) // first tag removed
	if !strings.Contains(w, all[0].Name) || !strings.Contains(w, "1 file(s)") {
		t.Fatalf("warning should name the tag and its file count: %q", w)
	}
}

func TestTakeNoticeIsOneShot(t *testing.T) {
	mw, _ := newWindowWithFiles(t, "a")
	mw.notice = "2 copy operation(s) were interrupted last time"
	if got := mw.takeNotice(); got == "" {
		t.Fatal("notice lost")
	}
	if got := mw.takeNotice(); got != "" {
		t.Fatalf("notice must be cleared after being taken, got %q", got)
	}
}
