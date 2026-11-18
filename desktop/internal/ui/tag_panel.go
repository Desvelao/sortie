package ui

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/destination"
	"sortie/internal/tags"
)

// TagPanel lists configured tags with their shortcut and assigned state.
type TagPanel struct {
	box      *fyne.Container
	checks   *fyne.Container
	root     fyne.CanvasObject
	onToggle func(tagID int)
}

func NewTagPanel(onToggle func(tagID int)) *TagPanel {
	p := &TagPanel{box: container.NewVBox(), checks: container.NewVBox(), onToggle: onToggle}
	title := widget.NewLabelWithStyle("Tags", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	dtitle := widget.NewLabelWithStyle("Destinations", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	bottom := container.NewVBox(widget.NewSeparator(), dtitle, container.NewVScroll(p.checks))
	p.root = container.NewBorder(title, bottom, nil, nil, container.NewVScroll(p.box))
	return p
}

func (p *TagPanel) Object() fyne.CanvasObject { return p.root }

// Refresh rebuilds the rows. hasFile=false disables toggling.
func (p *TagPanel) Refresh(ts []tags.Tag, assigned func(tagID int) bool, hasFile bool) {
	p.box.Objects = nil
	if len(ts) == 0 {
		p.box.Add(widget.NewLabel("No tags configured.\nOpen Settings (Ctrl+,)."))
	}
	for _, t := range ts {
		id := t.ID
		mark := "☐"
		if assigned(id) {
			mark = "☑"
		}
		key := t.Shortcut
		if key == "" {
			key = "–"
		}
		label := fmt.Sprintf("%s [%s] %s", mark, key, t.Name)
		if t.Destination != "" {
			label += " → " + t.Destination
		}
		if !t.Enabled {
			label += " (disabled)"
		}
		b := widget.NewButton(label, func() { p.onToggle(id) })
		b.Alignment = widget.ButtonAlignLeading
		if assigned(id) {
			b.Importance = widget.HighImportance
		}
		if !t.Enabled || !hasFile {
			b.Disable()
		}
		sw := canvas.NewRectangle(TagColor(t))
		sw.CornerRadius = 3
		sw.SetMinSize(fyne.NewSize(8, 8))
		p.box.Add(container.NewBorder(nil, nil, sw, nil, b))
	}
	p.box.Refresh()
}

// SetChecks shows the destination and status of each assigned tag.
// loading=true shows a placeholder while hashes are computed.
func (p *TagPanel) SetChecks(cs []destination.Check, loading bool, root string) {
	p.checks.Objects = nil
	add := func(text string, style fyne.TextStyle) {
		l := widget.NewLabel(text)
		l.Wrapping = fyne.TextWrapBreak
		l.TextStyle = style
		p.checks.Add(l)
	}
	switch {
	case loading:
		add("Checking destinations…", fyne.TextStyle{Italic: true})
	case len(cs) == 0:
		add("No tags assigned.", fyne.TextStyle{Italic: true})
	}
	if !loading && len(cs) > 0 && root == "" {
		add("Destination root not set (Settings → Destination).", fyne.TextStyle{Italic: true})
	}
	if !loading {
		for _, c := range cs {
			text := fmt.Sprintf("%s %s — %s", StatusIcon(c.Status), c.TagName, c.Status)
			if c.Path != "" {
				text += "\n" + c.Path
			}
			if c.Detail != "" {
				text += "\n" + c.Detail
			}
			add(text, fyne.TextStyle{})
		}
	}
	p.checks.Refresh()
}

// StatusIcon is a compact marker for a destination status.
func StatusIcon(s destination.Status) string {
	switch s {
	case destination.StatusReady:
		return "●"
	case destination.StatusAlreadyCopied:
		return "✔"
	case destination.StatusConflict:
		return "⚠"
	default:
		return "✖"
	}
}
