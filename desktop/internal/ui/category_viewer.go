package ui

import (
	"fmt"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/app"
	"sortie/internal/fileinfo"
	"sortie/internal/tags"
)

const (
	matchAnyLabel = "Any selected tag"
	matchAllLabel = "All selected tags"
)

// openCategoryViewer lists the files that carry one or several tags, with the
// source and destination directories on top. Choosing a file jumps to it in
// the main window. Nothing is changed by looking.
func (m *MainWindow) openCategoryViewer() {
	if m.dialogOpen {
		return
	}
	all := m.core.Tags()
	labelToID := map[string]int{}
	var labels []string
	for _, t := range all {
		l := t.Name
		if t.Destination != "" {
			l += " → " + t.Destination
		}
		labelToID[l] = t.ID
		labels = append(labels, l)
	}

	var shown []fileinfo.FileInfo
	count := widget.NewLabel("")
	list := widget.NewList(
		func() int { return len(shown) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < 0 || i >= len(shown) {
				return
			}
			f := shown[i]
			o.(*widget.Label).SetText(fmt.Sprintf("%s   [%s]", f.Name, tagNames(m.tagsFor(f))))
		},
	)

	var selected []int
	mode := widget.NewRadioGroup([]string{matchAnyLabel, matchAllLabel}, nil)
	mode.SetSelected(matchAnyLabel)
	refresh := func() {
		shown = m.core.FilesByTags(selected, mode.Selected == matchAllLabel)
		switch {
		case len(selected) == 0:
			count.SetText("Select one or more tags to list their files.")
		default:
			count.SetText(fmt.Sprintf("%d file(s)", len(shown)))
		}
		list.UnselectAll()
		list.Refresh()
	}
	group := widget.NewCheckGroup(labels, func(sel []string) {
		selected = selected[:0]
		for _, l := range sel {
			selected = append(selected, labelToID[l])
		}
		refresh()
	})
	mode.OnChanged = func(string) { refresh() }

	dirs := widget.NewLabel(m.dirsText())
	dirs.Wrapping = fyne.TextWrapBreak

	var closeIt func()
	list.OnSelected = func(i widget.ListItemID) {
		if i < 0 || i >= len(shown) {
			return
		}
		path := shown[i].Path
		if !m.core.GoTo(path) {
			m.core.SetFilter(app.FilterAll)
			m.filter.SetSelected(app.FilterAll.String())
			if !m.core.GoTo(path) {
				list.UnselectAll()
				return
			}
		}
		closeIt()
		m.afterButton()
	}
	closeBtn := widget.NewButton("Close", func() { closeIt() })

	side := container.NewBorder(nil, mode, nil, nil, container.NewVScroll(group))
	content := container.NewBorder(
		container.NewVBox(dirs, count), container.NewHBox(closeBtn), side, nil, list)
	d := dialog.NewCustomWithoutButtons("Browse categories", content, m.win)
	d.Resize(fyne.NewSize(760, 520))
	closeIt = func() {
		m.dialogOpen = false
		m.closeDialog = nil
		d.Hide()
	}
	m.dialogOpen = true
	m.closeDialog = closeIt
	refresh()
	d.Show()
}

// dirsText describes where files come from and where tagged files are copied.
func (m *MainWindow) dirsText() string {
	src, dst := m.core.InputDir(), m.core.Resolver().Root
	if src == "" {
		src = "(not set)"
	}
	if dst == "" {
		dst = "(not set — Settings → Destination)"
	}
	return "Source: " + src + "\nDestination: " + dst
}

func tagNames(ts []tags.Tag) string {
	var n []string
	for _, t := range ts {
		n = append(n, t.Name)
	}
	if len(n) == 0 {
		return "untagged"
	}
	return strings.Join(n, ", ")
}
