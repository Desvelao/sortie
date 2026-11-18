package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// newNavigation builds First/Prev/Next/Last buttons.
func newNavigation(first, prev, next, last func()) fyne.CanvasObject {
	return container.NewGridWithColumns(4,
		widget.NewButtonWithIcon("First", theme.MediaSkipPreviousIcon(), first),
		widget.NewButtonWithIcon("Previous", theme.NavigateBackIcon(), prev),
		widget.NewButtonWithIcon("Next", theme.NavigateNextIcon(), next),
		widget.NewButtonWithIcon("Last", theme.MediaSkipNextIcon(), last),
	)
}
