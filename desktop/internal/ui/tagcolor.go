package ui

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/tags"
)

// Okabe-Ito based palette: distinguishable for most color-vision types.
var tagPalette = []color.NRGBA{
	{0xE6, 0x9F, 0x00, 0xFF}, {0x56, 0xB4, 0xE9, 0xFF}, {0x00, 0x9E, 0x73, 0xFF},
	{0xD5, 0x5E, 0x00, 0xFF}, {0xCC, 0x79, 0xA7, 0xFF}, {0x00, 0x72, 0xB2, 0xFF},
	{0xF0, 0xE4, 0x42, 0xFF}, {0x99, 0x99, 0x99, 0xFF}, {0x8E, 0x44, 0xAD, 0xFF},
}

// TagColor returns the tag's configured color, or a stable palette color
// chosen by its position when none (or an invalid one) is set.
func TagColor(t tags.Tag) color.Color {
	if t.Color != "" {
		if c, err := tags.ParseColor(t.Color); err == nil {
			return c
		}
	}
	i := t.SortOrder
	if i < 0 {
		i = 0
	}
	return tagPalette[i%len(tagPalette)]
}

// tagDots is a row of small colored circles, one per tag.
func tagDots(ts []tags.Tag, size float32) *fyne.Container {
	row := container.NewHBox()
	for _, t := range ts {
		d := canvas.NewCircle(TagColor(t))
		d.StrokeColor, d.StrokeWidth = color.NRGBA{0, 0, 0, 0x90}, 1
		holder := container.NewGridWrap(fyne.NewSize(size, size), d)
		row.Add(holder)
	}
	return row
}

// tagChips is a row of colored swatches with the tag names.
func tagChips(ts []tags.Tag) *fyne.Container {
	row := container.NewHBox()
	if len(ts) == 0 {
		row.Add(widget.NewLabelWithStyle("Untagged", fyne.TextAlignLeading, fyne.TextStyle{Italic: true}))
		return row
	}
	for _, t := range ts {
		sw := canvas.NewRectangle(TagColor(t))
		sw.CornerRadius = 3
		sw.SetMinSize(fyne.NewSize(12, 12))
		row.Add(container.NewHBox(container.NewCenter(sw), widget.NewLabel(t.Name)))
	}
	return row
}
