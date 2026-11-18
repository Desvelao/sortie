package ui

import (
	"strings"
	"testing"

	"sortie/internal/copier"
	"sortie/internal/profile"
	"sortie/internal/tags"
)

func TestHumanSize(t *testing.T) {
	for _, c := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KiB (1024 bytes)"},
		{1536, "1.5 KiB (1536 bytes)"},
		{1 << 20, "1.0 MiB (1048576 bytes)"},
		{3 << 30, "3.0 GiB (3221225472 bytes)"},
	} {
		if got := humanSize(c.n); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestSummaryText(t *testing.T) {
	if got := summaryText(copier.Summary{Copied: 3}); got != "Copied 3" {
		t.Fatalf("%q", got)
	}
	got := summaryText(copier.Summary{Copied: 2, Renamed: 1, Replaced: 1, AlreadyCopied: 4, ConflictsSkipped: 5, Invalid: 6})
	for _, want := range []string{"Copied 2", "kept both 1", "replaced 1", "already copied 4", "conflicts skipped 5", "invalid (not copied) 6"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q missing %q", got, want)
		}
	}
	if strings.Contains(got, "Nothing was lost") {
		t.Error("the reassurance is only for failures and cancellations")
	}
	for _, s := range []copier.Summary{{Failed: 1}, {Cancelled: 2}} {
		if !strings.Contains(summaryText(s), "Nothing was lost") {
			t.Errorf("%+v: missing reassurance", s)
		}
	}
}

func TestProblemLines(t *testing.T) {
	res := func(st copier.ResultStatus, name string) copier.Result {
		return copier.Result{Job: copier.Job{Name: name, Tag: tags.Tag{Name: "Work"}}, Status: st, Detail: "why"}
	}
	s := copier.Summary{Results: []copier.Result{
		res(copier.StatusFailed, "bad.txt"),
		res(copier.StatusConflictSkip, "clash.txt"),
		res(copier.StatusInvalid, "inv.txt"),
	}}
	lines := strings.Split(problemLines(s), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "✖ bad.txt → Work") ||
		!strings.HasPrefix(lines[1], "⚠ clash.txt") || !strings.HasPrefix(lines[2], "✖ inv.txt") {
		t.Fatalf("%q", lines)
	}
	if problemLines(copier.Summary{}) != "" {
		t.Fatal("no results, no lines")
	}

	// output is capped
	var many copier.Summary
	for i := 0; i < 400; i++ {
		many.Results = append(many.Results, res(copier.StatusFailed, "f"))
	}
	got := strings.Split(problemLines(many), "\n")
	if len(got) != 301 || got[300] != "…" {
		t.Fatalf("%d lines, last %q", len(got), got[len(got)-1])
	}
}

func TestPolicyLabelFor(t *testing.T) {
	for p, want := range map[string]string{
		profile.ConflictRename:  policyRenameLabel,
		profile.ConflictReplace: policyReplaceLabel,
		profile.ConflictSkip:    policySkipLabel,
		"":                      policySkipLabel,
		"garbage":               policySkipLabel,
	} {
		if got := policyLabelFor(p); got != want {
			t.Errorf("policyLabelFor(%q) = %q, want %q", p, got, want)
		}
	}
}
