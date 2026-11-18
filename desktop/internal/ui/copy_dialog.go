package ui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/copier"
	"sortie/internal/destination"
	"sortie/internal/profile"
)

const (
	policySkipLabel    = "Skip conflicts (default)"
	policyRenameLabel  = "Keep both: save as “name (1).ext”"
	policyReplaceLabel = "Replace existing files (asks for confirmation)"
)

type copyPhase int

const (
	phaseAnalysing copyPhase = iota
	phaseReady
	phaseRunning
	phaseDone
)

// openCopyDialog analyses the destinations of the current file (all=false) or
// of every tagged file, lets the user pick a conflict policy, and runs the
// copy with progress. Everything slow runs off the UI thread. Classification
// is never changed, so a failed or cancelled copy can simply be repeated.
func (m *MainWindow) openCopyDialog(all bool) {
	if m.dialogOpen {
		return
	}
	if m.core.Resolver().Root == "" {
		dialog.ShowInformation("Destination not set", "Choose a destination root in Settings → Destination first.", m.win)
		return
	}
	jobs := m.core.CopyJobs(all)
	title := "Copy current file"
	if all {
		title = "Copy all classified files"
	}

	dirs := widget.NewLabel(m.dirsText())
	dirs.Wrapping = fyne.TextWrapBreak
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	bar := widget.NewProgressBar()
	current := widget.NewLabel("")
	current.Truncation = fyne.TextTruncateEllipsis
	policy := widget.NewRadioGroup([]string{policySkipLabel, policyRenameLabel, policyReplaceLabel}, nil)
	policy.SetSelected(policyLabelFor(m.core.Profile().ConflictPolicy))
	report := widget.NewLabel("")
	report.Wrapping = fyne.TextWrapBreak
	scroll := container.NewVScroll(report)
	scroll.SetMinSize(fyne.NewSize(600, 150))

	var (
		d        dialog.Dialog
		cancel   context.CancelFunc = func() {}
		phase    copyPhase
		plan     []destination.Check
		copyBtn  *widget.Button
		closeBtn *widget.Button
		evMu     sync.Mutex
		analyse  func()
	)

	counts := func() (ready, already, conflict, invalid, errs int) {
		for _, c := range plan {
			switch c.Status {
			case destination.StatusReady:
				ready++
			case destination.StatusAlreadyCopied:
				already++
			case destination.StatusConflict:
				conflict++
			case destination.StatusUnresolvedVar, destination.StatusInvalidPath:
				invalid++
			default:
				errs++
			}
		}
		return
	}
	updateButtons := func() {
		switch phase {
		case phaseReady:
			ready, _, conflict, _, _ := counts()
			if ready > 0 || (conflict > 0 && policy.Selected != policySkipLabel) {
				copyBtn.Enable()
			} else {
				copyBtn.Disable()
			}
			copyBtn.SetText("Copy")
			closeBtn.SetText("Close")
			policy.Enable()
		case phaseRunning:
			copyBtn.Disable()
			closeBtn.SetText("Cancel")
			policy.Disable()
		case phaseDone:
			copyBtn.SetText("Check again")
			copyBtn.Enable()
			closeBtn.SetText("Close")
			policy.Enable()
		default:
			copyBtn.Disable()
			closeBtn.SetText("Close")
		}
	}
	policy.OnChanged = func(string) { updateButtons() }

	closeIt := func() {
		cancel()
		m.dialogOpen = false
		m.closeDialog = nil
		d.Hide()
		m.update() // destination statuses may have changed
	}
	confirming := false // the "Replace existing files?" confirmation is showing
	// Escape / Close: cancels a running copy first, closes otherwise
	onClose := func() {
		if confirming {
			return // Escape must not close the dialog under the confirmation
		}
		if phase == phaseRunning {
			cancel()
			status.SetText("Cancelling…")
			return
		}
		closeIt()
	}

	analyse = func() {
		phase = phaseAnalysing
		updateButtons()
		bar.SetValue(0)
		current.SetText("")
		report.SetText("")
		if len(jobs) == 0 {
			scope := "."
			if !all {
				scope = " to the current file."
			}
			status.SetText("Nothing to copy: no enabled tags are assigned" + scope)
			return
		}
		status.SetText(fmt.Sprintf("Checking %d destination(s)…", len(jobs)))
		ctx, c := context.WithCancel(context.Background())
		cancel = c
		go func() {
			p := m.core.PlanCopy(ctx, jobs, func(done, total int) {
				fyne.Do(func() { bar.SetValue(float64(done) / float64(total)) })
			})
			if ctx.Err() != nil {
				return
			}
			fyne.Do(func() {
				plan = p
				ready, already, conflict, invalid, errs := counts()
				txt := fmt.Sprintf("%d to copy · %d already copied · %d conflicts · %d invalid · %d errors",
					ready, already, conflict, invalid, errs)
				if invalid > 0 {
					txt += "\nFiles whose destination uses a variable that cannot be determined are not copied (unless the template gives a default, e.g. {year|unknown})."
				}
				status.SetText(txt)
				var lines []string
				for i, c := range plan {
					if c.Status != destination.StatusReady && c.Status != destination.StatusAlreadyCopied && len(lines) < 200 {
						lines = append(lines, fmt.Sprintf("%s %s → %s: %s %s", StatusIcon(c.Status), jobs[i].Name, c.TagName, c.Status, c.Detail))
					}
				}
				report.SetText(strings.Join(lines, "\n"))
				phase = phaseReady
				updateButtons()
			})
		}()
	}

	run := func(opts copier.Options) {
		phase = phaseRunning
		updateButtons()
		status.SetText("Copying…")
		report.SetText("")
		ctx, c := context.WithCancel(context.Background())
		cancel = c
		var last time.Time
		opts.OnEvent = func(ev copier.Event) {
			evMu.Lock()
			defer evMu.Unlock()
			if ev.Kind != copier.EventDone && time.Since(last) < 100*time.Millisecond {
				return
			}
			last = time.Now()
			fyne.Do(func() {
				if phase != phaseRunning {
					return
				}
				bar.SetValue(float64(ev.Done) / float64(ev.Total))
				text := fmt.Sprintf("%d / %d — %s → %s", ev.Done, ev.Total, ev.Job.Name, ev.Job.Tag.Name)
				if ev.Kind == copier.EventBytes && ev.BytesTotal > 0 {
					text += fmt.Sprintf(" (%d%%)", ev.Bytes*100/ev.BytesTotal)
				}
				current.SetText(text)
			})
		}
		go func() {
			sum, err := m.core.RunCopy(ctx, jobs, opts)
			fyne.Do(func() {
				current.SetText("")
				phase = phaseDone
				updateButtons()
				if err != nil {
					status.SetText("Copy not started: " + err.Error())
					return
				}
				bar.SetValue(1)
				status.SetText(summaryText(sum))
				report.SetText(problemLines(sum))
				m.update()
			})
		}()
	}

	start := func() {
		opts := copier.Options{Workers: 2}
		switch policy.Selected {
		case policyRenameLabel:
			opts.Policy = copier.PolicyRename
		case policyReplaceLabel:
			_, _, conflict, _, _ := counts()
			confirming = true
			dialog.NewConfirm("Replace existing files?",
				fmt.Sprintf("%d existing destination file(s) may be overwritten with different content. This cannot be undone.\n\nReplace them?", conflict),
				func(ok bool) {
					confirming = false
					if ok {
						opts.Policy, opts.ConfirmReplace = copier.PolicyReplace, true
						run(opts)
					}
				}, m.win).Show()
			return
		}
		run(opts)
	}

	copyBtn = widget.NewButton("Copy", func() {
		if phase == phaseDone {
			analyse()
		} else if phase == phaseReady {
			start()
		}
	})
	copyBtn.Importance = widget.HighImportance
	closeBtn = widget.NewButton("Close", onClose)

	content := container.NewBorder(
		container.NewVBox(dirs, status, bar, current, widget.NewLabel("When a different file already exists:"), policy),
		container.NewHBox(copyBtn, closeBtn), nil, nil, scroll)
	d = dialog.NewCustomWithoutButtons(title, content, m.win)
	d.Resize(fyne.NewSize(680, 520))
	m.dialogOpen = true
	m.closeDialog = onClose
	d.Show()
	analyse()
}

func summaryText(s copier.Summary) string {
	parts := []string{fmt.Sprintf("Copied %d", s.Copied)}
	add := func(n int, label string) {
		if n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", label, n))
		}
	}
	add(s.Renamed, "kept both")
	add(s.Replaced, "replaced")
	add(s.AlreadyCopied, "already copied")
	add(s.ConflictsSkipped, "conflicts skipped")
	add(s.Invalid, "invalid (not copied)")
	add(s.Failed, "FAILED")
	add(s.Cancelled, "cancelled")
	txt := strings.Join(parts, " · ")
	if s.Failed > 0 || s.Cancelled > 0 {
		txt += "\nNothing was lost: tags are unchanged. Use “Check again” and Copy to retry."
	}
	return txt
}

func problemLines(s copier.Summary) string {
	var lines []string
	for _, r := range s.Results {
		switch r.Status {
		case copier.StatusFailed, copier.StatusConflictSkip, copier.StatusInvalid:
			if len(lines) >= 300 {
				lines = append(lines, "…")
				return strings.Join(lines, "\n")
			}
			lines = append(lines, fmt.Sprintf("%s %s → %s: %s %s", map[bool]string{true: "✖", false: "⚠"}[r.Status != copier.StatusConflictSkip],
				r.Job.Name, r.Job.Tag.Name, r.Status, r.Detail))
		}
	}
	return strings.Join(lines, "\n")
}

// policyLabelFor maps a profile's conflict_policy to the radio option that is
// preselected in the copy dialog. Unknown or empty values mean skip. Choosing
// "replace" only preselects it: overwriting still asks for confirmation.
func policyLabelFor(p string) string {
	switch p {
	case profile.ConflictRename:
		return policyRenameLabel
	case profile.ConflictReplace:
		return policyReplaceLabel
	}
	return policySkipLabel
}
