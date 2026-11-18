package ui

import (
	"fmt"
	"image"
	"image/color"
	"strings"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/fileinfo"
	"sortie/internal/tags"
)

// tile is one clickable entry of the filmstrip.
type tile struct {
	widget.BaseWidget
	holder *fyne.Container
	onTap  func()
	name   string
	tagged []tags.Tag
}

func newTile(f fileinfo.FileInfo, ts []tags.Tag, onTap func()) *tile {
	t := &tile{onTap: onTap, name: f.Name, tagged: ts}
	ext := strings.ToUpper(strings.TrimPrefix(f.Ext, "."))
	if ext == "" {
		ext = "FILE"
	}
	placeholder := widget.NewLabelWithStyle(ext, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	t.holder = container.NewStack(placeholder)
	t.ExtendBaseWidget(t)
	return t
}

// SetImage replaces the placeholder with a thumbnail.
func (t *tile) SetImage(img image.Image) {
	im := canvas.NewImageFromImage(img)
	im.FillMode, im.ScaleMode = canvas.ImageFillContain, canvas.ImageScaleSmooth
	t.holder.Objects = []fyne.CanvasObject{im}
	t.holder.Refresh()
}

func (t *tile) Tapped(*fyne.PointEvent) { t.onTap() }

func (t *tile) CreateRenderer() fyne.WidgetRenderer {
	label := widget.NewLabel(t.name)
	label.Truncation = fyne.TextTruncateEllipsis
	label.Alignment = fyne.TextAlignCenter
	t.holder.Resize(fyne.NewSize(96, 72))
	spacer := canvas.NewRectangle(color.Transparent)
	spacer.SetMinSize(fyne.NewSize(104, 72))
	body := container.NewStack(spacer, t.holder)
	if len(t.tagged) > 0 { // colored dots, one per tag, in the top-left corner
		body.Add(container.NewVBox(container.NewHBox(tagDots(t.tagged, 10)), layout.NewSpacer()))
	}
	return widget.NewSimpleRenderer(container.NewBorder(nil, label, nil, nil, body))
}

// Filmstrip shows thumbnails of the files around the current one.
type Filmstrip struct {
	box    *fyne.Container
	scroll *container.Scroll
	root   fyne.CanvasObject
	sig    string
	gen    atomic.Int64 // incremented per rebuild; retires thumbnail loads of old tiles
	onPick func(path string)
}

func NewFilmstrip(onPick func(path string)) *Filmstrip {
	f := &Filmstrip{box: container.NewHBox(), onPick: onPick}
	f.scroll = container.NewHScroll(f.box)
	f.root = f.scroll
	return f
}

func (f *Filmstrip) Object() fyne.CanvasObject { return f.root }

// Set rebuilds the strip unless it already shows exactly these files.
// loadThumb is called per tile to fetch a thumbnail asynchronously; its last
// argument reports (from any goroutine) whether the tile is still part of the
// strip, so work for tiles that were rebuilt away can be skipped.
func (f *Filmstrip) Set(files []fileinfo.FileInfo, cur int, tagsOf func(fileinfo.FileInfo) []tags.Tag, loadThumb func(fileinfo.FileInfo, func(image.Image), func() bool)) {
	var sig strings.Builder
	for i, fi := range files {
		sig.WriteString(fi.Path)
		if tagsOf != nil {
			for _, t := range tagsOf(fi) {
				sig.WriteString(fmt.Sprintf("|%d:%s", t.ID, t.Color))
			}
		}
		if i == cur {
			sig.WriteString("*")
		}
		sig.WriteString("\n")
	}
	if sig.String() == f.sig {
		return
	}
	f.sig = sig.String()
	gen := f.gen.Add(1)
	stillShown := func() bool { return f.gen.Load() == gen }
	f.box.Objects = nil
	for i, fi := range files {
		fi, current := fi, i == cur
		var ts []tags.Tag
		if tagsOf != nil {
			ts = tagsOf(fi)
		}
		t := newTile(fi, ts, func() { f.onPick(fi.Path) })
		var o fyne.CanvasObject = t
		if current {
			frame := canvas.NewRectangle(color.Transparent)
			frame.StrokeColor, frame.StrokeWidth = theme.Color(theme.ColorNamePrimary), 3
			o = container.NewStack(frame, t)
		}
		f.box.Add(o)
		if loadThumb != nil {
			loadThumb(fi, t.SetImage, stillShown)
		}
	}
	f.box.Refresh()
	f.scrollToTile(cur)
}

// scrollToTile scrolls so that tile i is centred in the visible part of the
// strip; without it the current tile can sit outside a narrow window.
func (f *Filmstrip) scrollToTile(i int) {
	if i < 0 || i >= len(f.box.Objects) {
		return
	}
	pad := theme.Padding()
	var x float32
	for _, o := range f.box.Objects[:i] {
		x += o.MinSize().Width + pad
	}
	centre := x + f.box.Objects[i].MinSize().Width/2
	view := f.scroll.Size().Width
	if view <= 0 {
		view = 800 // not laid out yet: assume a typical width
	}
	off := centre - view/2
	if off < 0 {
		off = 0
	}
	// The scroller clamps the offset to the content it has laid out so far;
	// size the new content first, as the scroller's own layout would.
	min, size := f.box.MinSize(), f.scroll.Size()
	f.box.Resize(fyne.NewSize(max(min.Width, size.Width), max(min.Height, size.Height)))
	f.scroll.ScrollToOffset(fyne.NewPos(off, 0))
}
