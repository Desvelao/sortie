package ui

import (
	"fmt"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/app"
	"sortie/internal/fileinfo"
	"sortie/internal/player"
	"sortie/internal/preview"
)

// FileViewer shows an optional format preview above the generic metadata of
// the current file. The preview is purely informational: the metadata and
// variable controls always work, whatever happens to the preview.
type FileViewer struct {
	preview    *fyne.Container
	player     *player.Player
	openFile   func(path string)
	onError    func(error)
	audioPath  string
	stopTicker chan struct{}

	root                             fyne.CanvasObject
	name, ext, mime, size, mod, path *widget.Label
	varsBox                          *fyne.Container // one row per variable of the profile
	varSelect                        *widget.Select  // variable the manual entry applies to
	ManualEntry                      *widget.Entry
	clearBtn, setBtn                 *widget.Button
	manual                           map[string]bool // variable → has a manual value (for the current file)
}

// NewFileViewer builds the viewer. onSet receives the variable name and the
// text typed in the manual entry; onClear removes that variable's override.
func NewFileViewer(onSet func(name, text string), onClear func(name string), pl *player.Player, openFile func(string), onError func(error)) *FileViewer {
	v := &FileViewer{
		preview: container.NewStack(), player: pl, openFile: openFile, onError: onError,
		name: widget.NewLabel(""), ext: widget.NewLabel(""), mime: widget.NewLabel(""),
		size: widget.NewLabel(""), mod: widget.NewLabel(""), path: widget.NewLabel(""),
		varsBox: container.NewVBox(), manual: map[string]bool{},
	}
	v.varSelect = widget.NewSelect(nil, func(string) { v.updateClear() })
	v.ManualEntry = widget.NewEntry()
	v.ManualEntry.SetPlaceHolder("manual value (Ctrl+Y), Enter to apply")
	v.ManualEntry.OnSubmitted = func(t string) { onSet(v.varSelect.Selected, t) }
	v.clearBtn = widget.NewButton("Clear manual", func() { onClear(v.varSelect.Selected) })
	v.setBtn = widget.NewButton("Set", func() { onSet(v.varSelect.Selected, v.ManualEntry.Text) })
	v.name.TextStyle = fyne.TextStyle{Bold: true}
	v.name.Wrapping = fyne.TextWrapBreak
	v.path.Wrapping = fyne.TextWrapBreak
	form := widget.NewForm(
		widget.NewFormItem("Filename", v.name),
		widget.NewFormItem("Extension", v.ext),
		widget.NewFormItem("MIME type", v.mime),
		widget.NewFormItem("Size", v.size),
		widget.NewFormItem("Modified", v.mod),
		widget.NewFormItem("Path", v.path),
		widget.NewFormItem("Variables", v.varsBox),
		widget.NewFormItem("Manual value", container.NewBorder(nil, nil, v.varSelect, container.NewHBox(v.setBtn, v.clearBtn), v.ManualEntry)),
	)
	split := container.NewVSplit(v.preview, container.NewVScroll(form))
	split.SetOffset(0.55)
	v.root = split
	return v
}

func (v *FileViewer) setPreview(o fyne.CanvasObject) {
	v.preview.Objects = []fyne.CanvasObject{o}
	v.preview.Refresh()
}

// ShowLoading indicates that a preview is being prepared.
func (v *FileViewer) ShowLoading() {
	v.endAudio()
	v.setPreview(container.NewCenter(widget.NewLabel("Loading preview…")))
}

// Clear removes the preview.
func (v *FileViewer) Clear() {
	v.endAudio()
	v.setPreview(container.NewCenter(widget.NewLabel("")))
}

// ShowPreview renders a finished preview.
func (v *FileViewer) ShowPreview(p preview.Preview) {
	v.endAudio()
	var main fyne.CanvasObject
	switch {
	case p.Kind == preview.KindImage && p.Image != nil:
		img := canvas.NewImageFromImage(p.Image)
		img.FillMode, img.ScaleMode = canvas.ImageFillContain, canvas.ImageScaleSmooth
		img.SetMinSize(fyne.NewSize(200, 150))
		main = img
	case p.Kind == preview.KindSVG:
		img := canvas.NewImageFromFile(p.Path)
		img.FillMode = canvas.ImageFillContain
		img.SetMinSize(fyne.NewSize(200, 150))
		main = img
	case p.Text != "":
		l := widget.NewLabel(p.Text)
		l.TextStyle = fyne.TextStyle{Monospace: true}
		main = container.NewScroll(l)
	}

	var bottom []fyne.CanvasObject
	if len(p.Fields) > 0 {
		form := widget.NewForm()
		for _, f := range p.Fields {
			val := widget.NewLabel(f.Value)
			val.Wrapping = fyne.TextWrapWord
			form.Append(f.Label, val)
		}
		bottom = append(bottom, form)
	}
	for _, n := range p.Notes {
		l := widget.NewLabel(n)
		l.Wrapping = fyne.TextWrapWord
		l.TextStyle = fyne.TextStyle{Italic: true}
		bottom = append(bottom, l)
	}
	if p.Kind == preview.KindAudio && p.Playable {
		bottom = append(bottom, v.audioControls(p.Path))
	}
	if p.Kind != preview.KindImage && p.Kind != preview.KindText && v.openFile != nil {
		path := p.Path
		bottom = append(bottom, widget.NewButton("Open with default application", func() { v.openFile(path) }))
	}
	box := container.NewVBox(bottom...)
	if main == nil {
		v.setPreview(container.NewVScroll(box))
		return
	}
	v.setPreview(container.NewBorder(nil, box, nil, nil, main))
}

func (v *FileViewer) audioControls(path string) fyne.CanvasObject {
	v.audioPath = path
	play := widget.NewButton("▶ Play", nil)
	stop := widget.NewButton("■ Stop", func() { v.player.Stop() })
	bar := widget.NewProgressBar()
	bar.TextFormatter = func() string { return "" }
	clock := widget.NewLabel("0:00")
	play.OnTapped = v.TogglePlayback
	done := make(chan struct{})
	v.stopTicker = done
	go func() {
		t := time.NewTicker(250 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				st := v.player.Status()
				fyne.Do(func() {
					mine := st.Path == path && st.State != player.Stopped
					switch {
					case mine && st.State == player.Playing:
						play.SetText("⏸ Pause")
					case mine:
						play.SetText("▶ Resume")
					default:
						play.SetText("▶ Play")
					}
					if mine && st.Length > 0 {
						bar.SetValue(float64(st.Position) / float64(st.Length))
						clock.SetText(preview.FormatDuration(st.Position) + " / " + preview.FormatDuration(st.Length))
					} else if !mine {
						bar.SetValue(0)
						clock.SetText("0:00")
					}
				})
			}
		}
	}()
	return container.NewBorder(nil, nil, container.NewHBox(play, stop), clock, bar)
}

// TogglePlayback plays, pauses or resumes the previewed audio file (Space).
func (v *FileViewer) TogglePlayback() {
	if v.player == nil || v.audioPath == "" {
		return
	}
	st := v.player.Status()
	if st.Path == v.audioPath && st.State != player.Stopped {
		v.player.TogglePause()
		return
	}
	if err := v.player.Play(v.audioPath); err != nil && v.onError != nil {
		v.onError(err)
	}
}

func (v *FileViewer) endAudio() {
	if v.stopTicker != nil {
		close(v.stopTicker)
		v.stopTicker = nil
	}
	v.audioPath = ""
	if v.player != nil {
		v.player.Stop()
	}
}

func (v *FileViewer) Object() fyne.CanvasObject { return v.root }

// Show displays f and the values of the profile's variables; ok=false clears
// the viewer.
func (v *FileViewer) Show(f fileinfo.FileInfo, ok bool, vals []app.VarInfo) {
	if !ok {
		for _, l := range []*widget.Label{v.name, v.ext, v.mime, v.size, v.mod, v.path} {
			l.SetText("")
		}
		v.name.SetText("No file")
		v.showVars(nil)
		return
	}
	ext := f.Ext
	if ext == "" {
		ext = "(none)"
	}
	v.name.SetText(f.Name)
	v.ext.SetText(ext)
	v.mime.SetText(f.MIMEType)
	v.size.SetText(humanSize(f.Size))
	v.mod.SetText(f.ModTime.Format("2006-01-02 15:04:05"))
	v.path.SetText(f.Path)
	v.showVars(vals)
}

// showVars lists every variable with its value and where it came from, and
// keeps the manual-entry selector in step with the profile's variables.
func (v *FileViewer) showVars(vals []app.VarInfo) {
	v.varsBox.Objects = nil
	v.manual = map[string]bool{}
	names := make([]string, 0, len(vals))
	for _, x := range vals {
		names = append(names, x.Name)
		v.manual[x.Name] = x.Manual
		value, src := "—", "—"
		if x.OK() {
			value, src = x.Value.Value, string(x.Source)
		}
		line := widget.NewLabel(fmt.Sprintf("%s = %s   (%s)", x.Name, value, src))
		line.Wrapping = fyne.TextWrapBreak
		v.varsBox.Add(line)
		if !x.OK() && x.Detail != "" {
			d := widget.NewLabel("    " + string(x.Status) + ": " + x.Detail)
			d.TextStyle = fyne.TextStyle{Italic: true}
			d.Wrapping = fyne.TextWrapWord
			v.varsBox.Add(d)
		}
	}
	if len(vals) == 0 {
		l := widget.NewLabel("No variables defined in this profile")
		l.TextStyle = fyne.TextStyle{Italic: true}
		v.varsBox.Add(l)
	}
	v.varsBox.Refresh()

	sel := v.varSelect.Selected
	v.varSelect.Options = names
	if !contains(names, sel) {
		sel = ""
		if len(names) > 0 {
			sel = names[0]
		}
	}
	v.varSelect.Selected = sel
	v.varSelect.Refresh()
	if len(names) == 0 {
		v.ManualEntry.Disable()
		v.setBtn.Disable()
		v.varSelect.Disable()
	} else {
		v.ManualEntry.Enable()
		v.setBtn.Enable()
		v.varSelect.Enable()
	}
	v.updateClear()
}

func (v *FileViewer) updateClear() {
	if v.manual[v.varSelect.Selected] {
		v.clearBtn.Enable()
	} else {
		v.clearBtn.Disable()
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB (%d bytes)", float64(n)/float64(div), "KMGTPE"[exp], n)
}
