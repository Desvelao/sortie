package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/destination"
	"sortie/internal/logging"
	"sortie/internal/pathtpl"
	"sortie/internal/profile"
	"sortie/internal/shortcuts"
	"sortie/internal/tags"
)

// openSettings shows the settings of the active profile (variables, tags,
// destination) and the application settings (shortcuts, general).
func (m *MainWindow) openSettings() {
	if m.dialogOpen {
		return
	}
	varsTab, collectVars, varNames, varsPending := m.buildVariablesTab()
	var recheckKeys func() // set once the shortcuts tab exists
	tagsTab, collectTags := m.buildTagsTab(varNames, func() {
		if recheckKeys != nil {
			recheckKeys()
		}
	})
	destTab, collectDest := m.buildDestinationTab(varNames)
	// The shortcuts tab checks against the tags as being edited, not the saved ones.
	editedTagKeys := func() []string {
		var keys []string
		for _, t := range collectTags() {
			keys = append(keys, t.Shortcut)
		}
		return keys
	}
	keysTab, collectKeys, recheck := m.buildShortcutsTab(editedTagKeys)
	recheckKeys = recheck
	generalTab, collectGeneral := m.buildGeneralTab()
	tabs := container.NewAppTabs(
		container.NewTabItem("Variables", varsTab),
		container.NewTabItem("Tags", tagsTab),
		container.NewTabItem("Destination", destTab),
		container.NewTabItem("Shortcuts", keysTab),
		container.NewTabItem("General", generalTab),
	)

	var d dialog.Dialog
	closeIt := func() {
		m.dialogOpen = false
		m.closeDialog = nil
		d.Hide()
	}
	// Everything the dialog edits, as comparable text, to detect unsaved edits.
	snapshot := func() string {
		gen, _ := collectGeneral()
		return fmt.Sprintf("%+v|%+v|%+v|%+v|%+v", collectTags(), collectVars(), collectDest(), collectKeys(), gen)
	}
	initial := snapshot()
	confirming := false // a confirmation is already showing (Escape pressed again)
	askClose := func() {
		if confirming {
			return
		}
		if snapshot() == initial {
			closeIt()
			return
		}
		confirming = true
		dialog.ShowConfirm("Discard changes?", "You have unsaved changes in the settings.", func(yes bool) {
			confirming = false
			if yes {
				closeIt()
			}
		}, m.win)
	}
	savedTagIDs := map[int]string{} // tags that exist now, by id
	for _, t := range m.core.Tags() {
		savedTagIDs[t.ID] = t.Name
	}
	doSave := func() {
		newTags := collectTags()
		err := varsPending() // unparsable numeric input must not be saved as "no limit"
		if err == nil {
			err = tags.Validate(newTags)
		}
		dest := collectDest()
		if err == nil {
			err = destination.ValidateRoot(dest.root)
		}
		newVars := collectVars()
		keys := collectKeys()
		gen, gerr := collectGeneral()
		if err == nil {
			err = gerr
		}
		if err == nil {
			err = shortcuts.Validate(keys, editedTagKeys())
		}
		if err == nil { // variables, tags and destination are one change to the profile
			pts := profile.FromTags(tags.Normalize(newTags))
			err = m.core.UpdateProfile(func(p *profile.Profile) {
				p.Variables, p.Tags, p.DestinationRoot, p.DestinationTemplate = newVars, pts, dest.root, dest.template
			})
		}
		if err != nil {
			dialog.ShowError(err, m.win)
			return
		}
		m.saveProfile()
		m.cfg.Shortcuts, m.cfg.LogLevel, m.cfg.ShowFilmstrip, m.cfg.ThumbnailCacheMB, m.cfg.Editor = keys, gen.logLevel, gen.filmstrip, gen.cacheMB, gen.editor
		if m.thumbs != nil {
			m.thumbs.SetLimit(int64(gen.cacheMB) << 20)
			go m.thumbs.Prune() // enforce a lowered limit now, not at the next start
		}
		m.saveConfig()
		closeIt()
		m.applyShortcuts()
		m.update()
	}
	save := widget.NewButton("Save", func() {
		if confirming {
			return
		}
		if msg := m.removedTagsWarning(savedTagIDs, collectTags()); msg != "" {
			confirming = true
			dialog.ShowConfirm("Remove tags?", msg, func(yes bool) {
				confirming = false
				if yes {
					doSave()
				}
			}, m.win)
			return
		}
		doSave()
	})
	save.Importance = widget.HighImportance
	cancel := widget.NewButton("Cancel", askClose)
	content := container.NewBorder(nil, container.NewHBox(cancel, save), nil, nil, tabs)

	d = dialog.NewCustomWithoutButtons("Settings", content, m.win)
	d.Resize(fyne.NewSize(760, 520))
	m.dialogOpen = true
	m.closeDialog = askClose
	d.Show()
}

// removedTagsWarning describes the assignments that saving newTags would
// delete (tags in saved that are no longer present); "" if no tag is removed.
func (m *MainWindow) removedTagsWarning(saved map[int]string, newTags []tags.Tag) string {
	kept := map[int]bool{}
	for _, t := range newTags {
		kept[t.ID] = true
	}
	var lines []string
	for id, name := range saved {
		if kept[id] {
			continue
		}
		n := len(m.core.FilesByTags([]int{id}, false))
		lines = append(lines, fmt.Sprintf("• %s (%d file(s) in the current list)", name, n))
	}
	if len(lines) == 0 {
		return ""
	}
	sort.Strings(lines)
	return "Removing these tags deletes their assignments (copied files are never touched):\n" + strings.Join(lines, "\n")
}

// buildTagsTab returns the tag editor and a function that collects the edited
// tag list (nothing is applied until the user presses Save).
// onChange is called whenever the edited tags change.
func (m *MainWindow) buildTagsTab(varNames func() []string, onChange func()) (fyne.CanvasObject, func() []tags.Tag) {
	original := m.core.Tags()
	work := append([]tags.Tag(nil), original...)
	rows := container.NewVBox()
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	preview := widget.NewLabel("")
	preview.Wrapping = fyne.TextWrapBreak

	check := func() {
		err := tags.Validate(work)
		for i := 0; err == nil && i < len(work); i++ {
			if t := strings.TrimSpace(work[i].DestinationTemplate); t != "" {
				if e := pathtpl.Validate(t, varNames()); e != nil {
					err = fmt.Errorf("tag %q template: %w", work[i].Name, e)
				}
			}
		}
		if err != nil {
			status.SetText("✗ " + err.Error())
			preview.SetText("")
		} else {
			status.SetText("✓ valid")
			preview.SetText(m.tagTemplatePreview(work, varNames()))
		}
		onChange()
	}
	var rebuild func()
	rebuild = func() {
		rows.Objects = nil
		rows.Add(container.NewBorder(nil, nil, widget.NewLabel("On "), widget.NewLabel("                  "),
			container.NewGridWithColumns(3,
				widget.NewLabelWithStyle("Name", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				widget.NewLabelWithStyle("Shortcut (A–Z, 0–9, F1–F12)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				widget.NewLabelWithStyle("Destination (relative)", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}))))
		for i := range work {
			i := i
			name := widget.NewEntry()
			name.SetText(work[i].Name)
			name.OnChanged = func(s string) { work[i].Name = s; check() }
			key := widget.NewEntry()
			key.SetText(work[i].Shortcut)
			key.OnChanged = func(s string) { work[i].Shortcut = s; check() }
			dest := widget.NewEntry()
			dest.SetText(work[i].Destination)
			dest.OnChanged = func(s string) { work[i].Destination = s; check() }
			tpl := widget.NewEntry()
			tpl.SetText(work[i].DestinationTemplate)
			tpl.SetPlaceHolder("empty = profile template")
			tpl.OnChanged = func(s string) { work[i].DestinationTemplate = s; check() }
			swatch := canvas.NewRectangle(TagColor(work[i]))
			swatch.CornerRadius = 3
			swatch.SetMinSize(fyne.NewSize(18, 18))
			col := widget.NewEntry()
			col.SetText(work[i].Color)
			col.SetPlaceHolder("empty = automatic")
			col.OnChanged = func(s string) {
				work[i].Color = s
				swatch.FillColor = TagColor(work[i]) // palette fallback while invalid
				swatch.Refresh()
				check()
			}
			colorCell := container.NewBorder(nil, nil, container.NewCenter(swatch), nil, col)
			en := widget.NewCheck("", func(b bool) { work[i].Enabled = b })
			en.SetChecked(work[i].Enabled)
			up := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
				if i > 0 {
					work[i-1], work[i] = work[i], work[i-1]
					rebuild()
				}
			})
			down := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
				if i < len(work)-1 {
					work[i+1], work[i] = work[i], work[i+1]
					rebuild()
				}
			})
			del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				work = append(work[:i:i], work[i+1:]...)
				rebuild()
			})
			rows.Add(container.NewBorder(nil, nil, en, container.NewHBox(up, down, del),
				container.NewGridWithColumns(5, name, key, dest, tpl, colorCell)))
		}
		rows.Refresh()
		check()
	}
	add := widget.NewButtonWithIcon("Add tag", theme.ContentAddIcon(), func() {
		id := tags.NextID(original, work)
		n := fmt.Sprintf("Tag %d", id)
		work = append(work, tags.Tag{ID: id, Name: n, Destination: n, Enabled: true})
		rebuild()
	})
	rebuild()
	note := widget.NewLabel("Removing a tag deletes its assignments, never any copied files.")
	note.Wrapping = fyne.TextWrapWord
	tab := container.NewBorder(nil, container.NewVBox(status, preview, note, add), nil, nil, container.NewVScroll(rows))
	collect := func() []tags.Tag {
		out := append([]tags.Tag(nil), work...)
		for i := range out {
			out[i].SortOrder = i
		}
		return out
	}
	return tab, collect
}

// tagTemplatePreview shows, for every tag with its own template, the path the
// current file would get (or why it cannot be resolved).
func (m *MainWindow) tagTemplatePreview(ts []tags.Tag, names []string) string {
	root := m.core.Resolver().Root
	if strings.TrimSpace(root) == "" {
		root = "/<destination root>"
	}
	name, values := "IMG_0001.jpg", map[string]string{}
	if f, ok := m.core.Current(); ok {
		name = f.Name
		for _, v := range m.core.VarsOf(f) {
			if v.OK() {
				values[v.Name] = v.Value.Value
			}
		}
	}
	var lines []string
	for _, t := range ts {
		if strings.TrimSpace(t.DestinationTemplate) == "" {
			continue
		}
		r := destination.Resolver{Root: root, VarNames: names}
		if p, err := r.Resolve(name, values, t); err != nil {
			lines = append(lines, fmt.Sprintf("%s → %v", t.Name, err))
		} else {
			lines = append(lines, fmt.Sprintf("%s → %s", t.Name, p))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "Example for the current file:\n" + strings.Join(lines, "\n")
}

type destSettings struct{ root, template string }

// buildDestinationTab edits the destination root and the profile's default
// template, with a live example path.
func (m *MainWindow) buildDestinationTab(varNames func() []string) (fyne.CanvasObject, func() destSettings) {
	prof := m.core.Profile()
	root := widget.NewEntry()
	root.SetText(prof.DestinationRoot)
	root.SetPlaceHolder("/path/to/Sorted")
	tpl := widget.NewEntry()
	tpl.SetText(prof.DestinationTemplate)
	tpl.SetPlaceHolder(profile.DefaultTemplate)
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	example := widget.NewLabel("")
	example.Wrapping = fyne.TextWrapBreak

	refresh := func(string) {
		names := varNames()
		var problems []string
		if err := destination.ValidateRoot(root.Text); err != nil {
			problems = append(problems, err.Error())
		}
		if t := strings.TrimSpace(tpl.Text); t != "" {
			if err := destination.ValidateTemplate(t, names); err != nil {
				problems = append(problems, err.Error())
			}
		}
		if len(problems) > 0 {
			status.SetText("✗ " + strings.Join(problems, "; "))
			example.SetText("")
			return
		}
		status.SetText("✓ valid")
		if d := strings.TrimSpace(m.core.InputDir()); d != "" && strings.TrimSpace(root.Text) != "" &&
			strings.HasPrefix(strings.TrimSuffix(root.Text, "/")+"/", strings.TrimSuffix(d, "/")+"/") {
			status.SetText("✓ valid — warning: the destination is inside the input directory, copies would be rescanned as new files")
		}
		name, values, tg := "IMG_0001.jpg", map[string]string{}, tags.Tag{Name: "Family", Destination: "Family"}
		if f, ok := m.core.Current(); ok {
			name = f.Name
			for _, v := range m.core.VarsOf(f) {
				if v.OK() {
					values[v.Name] = v.Value.Value
				}
			}
		}
		if ts := m.core.Tags(); len(ts) > 0 {
			tg = ts[0]
		}
		r := destination.Resolver{Root: root.Text, Template: tpl.Text, VarNames: names}
		if strings.TrimSpace(root.Text) == "" {
			r.Root = "/<destination root>"
		}
		if p, err := r.Resolve(name, values, tg); err != nil {
			example.SetText("Example: " + err.Error())
		} else {
			example.SetText("Example: " + p)
		}
	}
	for _, e := range []*widget.Entry{root, tpl} {
		e.OnChanged = refresh
	}
	refresh("")

	browse := widget.NewButton("Browse…", func() {
		d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
			if err == nil && u != nil {
				root.SetText(u.Path())
			}
		}, m.win)
		d.Resize(fyne.NewSize(650, 420))
		d.Show()
	})
	help := widget.NewLabel("Placeholders: {tag} {filename} {name} {ext} and the profile's variables (" + strings.Join(varNames(), ", ") + "). " +
		"{var|default} supplies a value when a variable cannot be determined; {var:upper|lower|slug|padN} formats it. " +
		"{tag} is the tag's folder; a tag may have its own template on the Tags tab. " +
		"The last component must contain {filename} or {name}. Files are never written outside the root.")
	help.Wrapping = fyne.TextWrapWord
	form := widget.NewForm(
		widget.NewFormItem("Destination root", container.NewBorder(nil, nil, nil, browse, root)),
		widget.NewFormItem("Template", tpl),
		widget.NewFormItem("Validation", status),
		widget.NewFormItem("", example),
	)
	return container.NewVBox(form, help), func() destSettings {
		return destSettings{strings.TrimSpace(root.Text), strings.TrimSpace(tpl.Text)}
	}
}

// buildShortcutsTab edits the application shortcuts (tag keys are on the Tags tab).
// tagKeys returns the tag shortcuts currently being edited; the returned
// function re-runs the check when they change.
func (m *MainWindow) buildShortcutsTab(tagKeys func() []string) (fyne.CanvasObject, func() map[string]string, func()) {
	entries := map[string]*widget.Entry{}
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	collect := func() map[string]string {
		out := map[string]string{}
		for id, e := range entries {
			out[id] = strings.TrimSpace(e.Text)
		}
		return out
	}
	check := func(string) {
		if err := shortcuts.Validate(collect(), tagKeys()); err != nil {
			status.SetText("✗ " + err.Error())
		} else {
			status.SetText("✓ valid — applied when you press Save")
		}
	}
	form := widget.NewForm()
	for _, a := range shortcuts.Actions {
		e := widget.NewEntry()
		e.SetText(m.cfg.Shortcuts[a.ID])
		if e.Text == "" {
			e.SetText(a.Default)
		}
		e.OnChanged = check
		entries[a.ID] = e
		form.Append(a.Label, e)
	}
	reset := widget.NewButton("Restore default shortcuts", func() {
		for _, a := range shortcuts.Actions {
			entries[a.ID].SetText(a.Default)
		}
	})
	help := widget.NewLabel("Examples: Right, Home, Space, F5, Ctrl+O, Ctrl+Shift+C, Alt+Left. " +
		"Shift must be combined with Ctrl or Alt. Keys without Ctrl/Alt cannot also be tag shortcuts. Escape always closes dialogs.")
	help.Wrapping = fyne.TextWrapWord
	check("")
	return container.NewBorder(nil, container.NewVBox(status, help, reset), nil, nil, container.NewVScroll(form)), collect, func() { check("") }
}

type generalSettings struct {
	editor    string
	logLevel  string
	filmstrip bool
	cacheMB   int
}

// buildGeneralTab edits view and logging preferences.
func (m *MainWindow) buildGeneralTab() (fyne.CanvasObject, func() (generalSettings, error)) {
	film := widget.NewCheck("Show filmstrip with thumbnails of neighbouring files", nil)
	film.SetChecked(m.cfg.ShowFilmstrip)
	level := widget.NewSelect([]string{"debug", "info", "warn", "error"}, nil)
	level.SetSelected(m.cfg.LogLevel)
	size := widget.NewEntry()
	size.SetText(strconv.Itoa(m.cfg.ThumbnailCacheMB))
	editor := widget.NewEntry()
	editor.SetText(m.cfg.Editor)
	editor.SetPlaceHolder("empty = $VISUAL, then $EDITOR, then the desktop default")
	clear := widget.NewButton("Clear thumbnail cache", func() {
		if m.thumbs != nil {
			if err := m.thumbs.Clear(); err != nil {
				dialog.ShowError(err, m.win)
				return
			}
		}
		dialog.ShowInformation("Thumbnail cache", "Cleared. Thumbnails are regenerated when needed.", m.win)
	})
	logPath := "(unavailable)"
	if p, err := logging.DefaultPath(); err == nil {
		logPath = p
	}
	where := widget.NewLabel("Log file: " + logPath + "\nThe log level applies after a restart.")
	where.Wrapping = fyne.TextWrapBreak
	form := widget.NewForm(
		widget.NewFormItem("", film),
		widget.NewFormItem("Thumbnail cache (MB)", size),
		widget.NewFormItem("", clear),
		widget.NewFormItem("Log level", level),
		widget.NewFormItem("Profile editor", editor),
	)
	return container.NewVBox(form, where), func() (generalSettings, error) {
		mb, err := strconv.Atoi(strings.TrimSpace(size.Text))
		if err != nil || mb < 10 || mb > 10000 {
			return generalSettings{}, fmt.Errorf("thumbnail cache size must be a number between 10 and 10000 MB")
		}
		return generalSettings{editor: strings.TrimSpace(editor.Text), logLevel: level.Selected, filmstrip: film.Checked, cacheMB: mb}, nil
	}
}
