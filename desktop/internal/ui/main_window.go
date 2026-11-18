// Package ui contains the Fyne user interface. It holds no business logic.
package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"log/slog"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/app"
	"sortie/internal/cache"
	"sortie/internal/config"
	"sortie/internal/database"
	"sortie/internal/destination"
	"sortie/internal/fileinfo"
	"sortie/internal/player"
	"sortie/internal/preview"
	"sortie/internal/profile"
	"sortie/internal/scanner"
	"sortie/internal/shortcuts"
	"sortie/internal/tags"
)

// MainWindow is the primary window.
type MainWindow struct {
	win      fyne.Window
	core     *app.Application
	cfg      config.Config
	cfgPath  string
	profDir  string // folder of the profile files; "" = changes are not persisted
	viewer   *FileViewer
	tagPanel *TagPanel

	previews    *preview.Registry
	pcache      *preview.Cache
	player      *player.Player
	previewSem  chan struct{}
	previewGen  atomic.Int64
	shownKey    string // preview.Key of the file shown (path, size, mtime); "" = none
	checkGen    atomic.Int64
	checkCancel context.CancelFunc
	notice      string

	filter        *widget.Select
	filmstrip     *Filmstrip
	tagBar        *fyne.Container
	thumbs        *cache.Thumbnails
	thumbSem      chan struct{}
	statusRunning bool
	plainKeys     map[string]string // Fyne key name → action id
	registered    []fyne.Shortcut

	recursive     *widget.Check
	profSelect    *widget.Select
	profLabels    map[string]string // selector label → profile id
	profSelecting bool              // selector is being rebuilt: ignore its callback
	profStamp     stamp             // state of the profile file when last applied
	profStampOK   bool
	stopWatch     chan struct{}

	dirLabel, destLabel, countLabel, posLabel, statusLabel *widget.Label
	copyOne                                                *widget.Button // disabled while no destination of the current file can be copied
	scanning                                               bool
	dialogOpen                                             bool
	closeDialog                                            func()
	cancel                                                 context.CancelFunc
}

func NewMainWindow(a fyne.App, core *app.Application, cfg config.Config, cfgPath string) *MainWindow {
	m := &MainWindow{
		win: a.NewWindow("Sortie"), core: core, cfg: cfg, cfgPath: cfgPath,
		dirLabel:    widget.NewLabel("No directory selected"),
		destLabel:   widget.NewLabel("Destination: (not set)"),
		countLabel:  widget.NewLabel("Files: 0"),
		posLabel:    widget.NewLabel("0 / 0"),
		statusLabel: widget.NewLabel(""),
	}
	m.previews, m.pcache, m.player = preview.Default(), preview.NewCache(6), player.New()
	m.previewSem = make(chan struct{}, 2)
	m.viewer = NewFileViewer(m.setManualVar, m.clearManualVar, m.player, m.openExternal,
		func(err error) { dialog.ShowError(err, m.win) })
	m.tagPanel = NewTagPanel(m.toggleTag)
	m.dirLabel.Truncation = fyne.TextTruncateEllipsis
	m.destLabel.Truncation = fyne.TextTruncateEllipsis

	open := widget.NewButton("Select directory…", m.chooseDirectory)
	settings := widget.NewButton("Settings…", m.openSettings)
	m.copyOne = widget.NewButton("Copy current", func() { m.openCopyDialog(false) })
	copyOne := m.copyOne
	copyAll := widget.NewButton("Copy all classified…", func() { m.openCopyDialog(true) })
	refresh := widget.NewButton("Refresh", m.rescan)
	browse := widget.NewButton("Browse categories…", m.openCategoryViewer)
	m.recursive = widget.NewCheck("Recursive", func(b bool) {
		if m.core.Profile().Recursive == b {
			return
		}
		if m.updateProfile(func(p *profile.Profile) { p.Recursive = b }) {
			m.rescan()
			return
		}
		// the profile kept the old value: show it (the callback sees that it
		// equals the profile and returns at once)
		m.recursive.SetChecked(m.core.Profile().Recursive)
	})
	m.recursive.SetChecked(core.Profile().Recursive)
	m.profSelect = widget.NewSelect(nil, m.onProfileSelected)
	editBtn := widget.NewButton("Edit file", m.openProfileInEditor)
	showBtn := widget.NewButton("Show in folder", m.showProfileInFolder)
	reloadBtn := widget.NewButton("Reload", func() { m.reloadProfile(true) })
	var manageBtn *widget.Button
	manageBtn = widget.NewButton("Profiles ▾", func() { m.showManageMenu(manageBtn) })

	m.filter = widget.NewSelect(filterLabels(), func(s string) {
		m.core.SetFilter(app.FilterByLabel(s))
		m.win.Canvas().Unfocus()
		m.update()
	})
	m.filter.Selected = app.FilterAll.String() // no callback during construction
	m.thumbSem = make(chan struct{}, 2)
	m.tagBar = container.NewHBox()
	m.filmstrip = NewFilmstrip(func(path string) {
		if m.core.GoTo(path) {
			m.afterButton()
		}
	})
	header := container.NewVBox(
		container.NewBorder(nil, nil, open, container.NewHBox(settings, refresh, m.recursive), m.dirLabel),
		m.destLabel,
		container.NewBorder(nil, nil, widget.NewLabel("Profile:"), container.NewHBox(editBtn, showBtn, reloadBtn, manageBtn), m.profSelect),
		container.NewHBox(widget.NewLabel("Show:"), m.filter, m.countLabel, m.posLabel, m.statusLabel),
		m.tagBar,
		container.NewHBox(copyOne, copyAll, browse),
	)
	nav := newNavigation(
		func() { m.core.First(); m.afterButton() },
		func() { m.core.Prev(); m.afterButton() },
		func() { m.core.Next(); m.afterButton() },
		func() { m.core.Last(); m.afterButton() },
	)
	split := container.NewHSplit(m.viewer.Object(), m.tagPanel.Object())
	split.SetOffset(0.58)
	m.win.SetContent(container.NewBorder(header, container.NewVBox(m.filmstrip.Object(), nav), nil, nil, split))
	m.win.Resize(fyne.NewSize(1000, 560))
	m.installKeys()
	m.applyShortcuts()
	m.win.SetOnClosed(func() {
		m.stopProfileWatcher()
		m.player.Stop()
	})
	m.update()
	return m
}

// installKeys routes plain key presses: configured application shortcuts
// first, then tag shortcuts. Modified shortcuts (Ctrl+…) are registered by
// applyShortcuts. Key events never reach this handler while a text entry has
// focus, so typing is not affected.
func (m *MainWindow) installKeys() {
	m.win.Canvas().SetOnTypedKey(func(e *fyne.KeyEvent) {
		if m.dialogOpen {
			if e.Name == fyne.KeyEscape && m.closeDialog != nil {
				m.closeDialog()
			}
			return
		}
		if id, ok := m.plainKeys[string(e.Name)]; ok {
			m.actions()[id]()
			return
		}
		handled, err := m.core.ToggleByShortcut(string(e.Name))
		if err != nil {
			dialog.ShowError(err, m.win)
		}
		if handled {
			m.update()
		}
	})
}

// actions maps shortcut action ids to behaviour.
func (m *MainWindow) actions() map[string]func() {
	nav := func(move func()) func() { return func() { move(); m.update() } }
	return map[string]func(){
		"next":         nav(m.core.Next),
		"prev":         nav(m.core.Prev),
		"first":        nav(m.core.First),
		"last":         nav(m.core.Last),
		"open_dir":     m.chooseDirectory,
		"settings":     m.openSettings,
		"copy_all":     func() { m.openCopyDialog(true) },
		"copy_current": func() { m.openCopyDialog(false) },
		"focus_year":   func() { m.win.Canvas().Focus(m.viewer.ManualEntry) },
		"play":         m.viewer.TogglePlayback,
	}
}

func fyneModifier(b shortcuts.Binding) fyne.KeyModifier {
	var mod fyne.KeyModifier
	if b.Ctrl {
		mod |= fyne.KeyModifierControl
	}
	if b.Alt {
		mod |= fyne.KeyModifierAlt
	}
	if b.Shift {
		mod |= fyne.KeyModifierShift
	}
	return mod
}

// applyShortcuts (re)registers the configured shortcuts; it can be called
// again after the settings changed, without restarting.
func (m *MainWindow) applyShortcuts() {
	var tagKeys []string
	for _, t := range m.core.Tags() {
		tagKeys = append(tagKeys, t.Shortcut)
	}
	// One clashing or invalid binding falls back by itself; the rest of the
	// user's custom shortcuts stay in effect.
	binds, problems := shortcuts.ResolveLenient(m.cfg.Shortcuts, tagKeys)
	for _, p := range problems {
		slog.Warn("shortcut setting not applied", "problem", p)
	}
	c := m.win.Canvas()
	for _, sc := range m.registered {
		c.RemoveShortcut(sc)
	}
	m.registered = nil
	m.plainKeys = map[string]string{}
	acts := m.actions()
	for id, b := range binds {
		id, b := id, b
		if b.Plain() {
			m.plainKeys[b.FyneKey()] = id
			continue
		}
		sc := &desktop.CustomShortcut{KeyName: fyne.KeyName(b.FyneKey()), Modifier: fyneModifier(b)}
		c.AddShortcut(sc, func(fyne.Shortcut) {
			if !m.dialogOpen {
				acts[id]()
			}
		})
		m.registered = append(m.registered, sc)
	}
}

func filterLabels() []string {
	var out []string
	for _, f := range app.AllFilters {
		out = append(out, f.String())
	}
	return out
}

// SetThumbnailCache enables the on-disk thumbnail cache for the filmstrip.
func (m *MainWindow) SetThumbnailCache(c *cache.Thumbnails) { m.thumbs = c }

// Run shows the window, loads the remembered directory and blocks.
func (m *MainWindow) Run() {
	if n := m.core.InterruptedAtStart(); n > 0 {
		m.notice = fmt.Sprintf("%d copy operation(s) were interrupted last time — copy again to resume", n)
		if left := len(m.core.LeftoverTempFiles()); left > 0 {
			m.notice += fmt.Sprintf("; %d leftover temporary file(s) are listed in the log", left)
		}
	}
	m.refreshProfiles()
	m.startProfileWatcher()
	if m.core.InputDir() != "" {
		m.rescan()
	} else if n := m.takeNotice(); n != "" {
		m.statusLabel.SetText(n) // no scan will run to show it
	}
	m.win.ShowAndRun()
}

func (m *MainWindow) Window() fyne.Window { return m.win }

// takeNotice returns the pending one-time startup notice and clears it.
func (m *MainWindow) takeNotice() string {
	n := m.notice
	m.notice = ""
	return n
}

// setInputDirectory stores dir in the active profile (the input folder is
// profile state, not config.json state) and rescans.
func (m *MainWindow) setInputDirectory(dir string) {
	if m.updateProfile(func(p *profile.Profile) { p.InputDirectory = dir }) {
		m.rescan()
	}
}

func (m *MainWindow) chooseDirectory() {
	if m.dialogOpen {
		return
	}
	m.dialogOpen = true
	d := dialog.NewFolderOpen(func(u fyne.ListableURI, err error) {
		m.dialogOpen = false
		if err != nil {
			dialog.ShowError(err, m.win)
			return
		}
		if u == nil {
			return
		}
		m.setInputDirectory(u.Path())
	}, m.win)
	if dir := m.core.InputDir(); dir != "" {
		if l, err := storage.ListerForURI(storage.NewFileURI(dir)); err == nil {
			d.SetLocation(l)
		}
	}
	d.SetOnClosed(func() { m.dialogOpen = false })
	d.Resize(fyne.NewSize(700, 450))
	d.Show()
}

// SetProfileDir sets the folder the active profile is saved to when it is
// changed from the UI.
func (m *MainWindow) SetProfileDir(dir string) { m.profDir = dir }

// updateProfile changes the active profile and persists it. It reports
// whether the change was applied; failures are shown to the user.
func (m *MainWindow) updateProfile(fn func(*profile.Profile)) bool {
	if err := m.core.UpdateProfile(fn); err != nil {
		dialog.ShowError(err, m.win)
		return false
	}
	m.saveProfile()
	return true
}

// saveProfile writes the active profile to its file (when a profile folder is
// set); the profile is already applied, so a failure only means it was not
// saved.
func (m *MainWindow) saveProfile() {
	if m.profDir == "" {
		return
	}
	path := profile.ResolvePath(m.profDir, m.core.ProfileID())
	err := profile.Save(path, m.core.Profile())
	m.rememberProfileFile() // our own write must not look like an outside edit
	if err != nil {
		slog.Error("saving profile failed", "path", path, "error", err)
		dialog.ShowError(fmt.Errorf("the change is active but could not be saved to %s: %w", path, err), m.win)
	}
}

func (m *MainWindow) saveConfig() {
	if err := config.Save(m.cfgPath, m.cfg); err != nil {
		slog.Error("saving config failed", "path", m.cfgPath, "error", err)
		dialog.ShowError(fmt.Errorf("the settings are active but could not be saved to %s, so they will be lost when the program closes: %w", m.cfgPath, err), m.win)
	}
}

// rescan scans the input directory in a goroutine; UI updates go through fyne.Do.
func (m *MainWindow) rescan() {
	dir, recursive := m.core.InputDir(), m.core.Profile().Recursive
	if dir == "" {
		return
	}
	if m.cancel != nil {
		m.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.statusLabel.SetText("Scanning…")
	go func() {
		res, err := scanner.Scan(ctx, dir, scanner.Options{Recursive: recursive})
		var sync database.SyncResult
		if err == nil && ctx.Err() == nil {
			sync, err = m.core.ApplyScan(dir, res.Files, recursive)
		}
		if ctx.Err() != nil {
			return // superseded by a newer scan
		}
		fyne.Do(func() {
			if err != nil {
				m.statusLabel.SetText(m.takeNotice()) // a failed scan must not swallow it
				dialog.ShowError(fmt.Errorf("scanning %s: %w", dir, err), m.win)
				return
			}
			var parts []string
			if n := len(res.Skipped); n > 0 {
				parts = append(parts, fmt.Sprintf("%d inaccessible skipped", n))
			}
			if sync.Added+sync.Modified+sync.Removed+sync.Renamed > 0 {
				parts = append(parts, fmt.Sprintf("+%d new, %d renamed, %d modified, %d removed", sync.Added, sync.Renamed, sync.Modified, sync.Removed))
			}
			if n := m.takeNotice(); n != "" {
				parts = append(parts, n)
			}
			m.statusLabel.SetText(strings.Join(parts, " · "))
			m.update()
		})
	}()
}

func (m *MainWindow) update() {
	m.dirLabel.SetText(m.core.InputDir())
	if m.core.InputDir() == "" {
		m.dirLabel.SetText("No directory selected")
	} else {
		m.dirLabel.SetText("Source: " + m.core.InputDir())
	}
	if root := m.core.Resolver().Root; root == "" {
		m.destLabel.SetText("Destination: (not set — Settings → Destination)")
	} else {
		m.destLabel.SetText("Destination: " + root)
	}
	pos, shown := m.core.Position()
	if all := m.core.Total(); shown == all {
		m.countLabel.SetText(fmt.Sprintf("Files: %d", all))
	} else {
		m.countLabel.SetText(fmt.Sprintf("Files: %d of %d", shown, all))
	}
	m.posLabel.SetText(fmt.Sprintf("%d / %d · tagged %d", pos, shown, m.core.TaggedCount()))
	f, ok := m.core.Current()
	var vals []app.VarInfo
	if ok {
		vals = m.core.VarsOf(f)
	}
	m.viewer.Show(f, ok, vals)
	m.refreshPreview(f, ok)
	m.refreshChecks(f, ok)
	m.refreshFilmstrip()
	m.ensureStatuses()
	m.tagBar.Objects = nil
	if ok {
		m.tagBar.Add(tagChips(m.tagsFor(f)))
	}
	m.tagBar.Refresh()
	m.tagPanel.Refresh(m.core.Tags(), func(id int) bool { return ok && m.core.HasTag(f, id) }, ok)
}

// afterButton refreshes the UI and returns keyboard focus to the canvas so
// shortcuts keep working after a mouse click.
func (m *MainWindow) afterButton() {
	m.win.Canvas().Unfocus()
	m.update()
}

func (m *MainWindow) toggleTag(tagID int) {
	if _, err := m.core.ToggleTag(tagID); err != nil {
		dialog.ShowError(err, m.win)
	}
	m.afterButton()
}

func (m *MainWindow) setManualVar(name, text string) {
	f, ok := m.core.Current()
	if !ok || name == "" {
		return
	}
	if err := m.core.SetManualVar(f, name, text); err != nil {
		dialog.ShowError(err, m.win)
		return
	}
	m.viewer.ManualEntry.SetText("")
	m.win.Canvas().Unfocus()
	m.update()
}

func (m *MainWindow) clearManualVar(name string) {
	if f, ok := m.core.Current(); ok && name != "" {
		if err := m.core.ClearManualVar(f, name); err != nil {
			dialog.ShowError(err, m.win)
		}
		m.win.Canvas().Unfocus()
		m.update()
	}
}

// refreshPreview loads the preview of f asynchronously. Nothing happens when
// the file did not change (same path, size and modification time, e.g. after
// toggling a tag), so audio keeps playing; a file edited in place, or replaced
// before a rescan, gets a new preview.
func (m *MainWindow) refreshPreview(f fileinfo.FileInfo, ok bool) {
	key := ""
	if ok {
		key = preview.Key(f.Path, f.Size, f.ModTime)
	}
	if key == m.shownKey {
		return
	}
	m.shownKey = key
	gen := m.previewGen.Add(1)
	if !ok {
		m.viewer.Clear()
		return
	}
	if p, hit := m.pcache.Get(key); hit {
		m.viewer.ShowPreview(p)
		return
	}
	m.viewer.ShowLoading()
	go func() {
		time.Sleep(80 * time.Millisecond) // skip work while the user is skimming
		if m.previewGen.Load() != gen {
			return
		}
		m.previewSem <- struct{}{}
		defer func() { <-m.previewSem }()
		if m.previewGen.Load() != gen {
			return
		}
		p := m.previews.Create(f.Path, f.MIMEType, f.Ext)
		m.pcache.Put(key, p)
		fyne.Do(func() {
			if m.previewGen.Load() == gen {
				m.viewer.ShowPreview(p)
			}
		})
	}()
}

// openExternal opens a file with the desktop's default application.
func (m *MainWindow) openExternal(path string) {
	if err := fyne.CurrentApp().OpenURL(&url.URL{Scheme: "file", Path: path}); err != nil {
		dialog.ShowError(fmt.Errorf("cannot open %s: %w", path, err), m.win)
	}
}

// refreshChecks re-evaluates destinations for the current file asynchronously
// (hashing can be slow). Called on every change that may affect them: file
// navigation, tag toggles, variable changes and settings.
func (m *MainWindow) refreshChecks(f fileinfo.FileInfo, ok bool) {
	if m.checkCancel != nil {
		m.checkCancel()
		m.checkCancel = nil
	}
	gen := m.checkGen.Add(1)
	root := m.core.Resolver().Root
	m.copyOne.Enable() // until the checks say otherwise
	if !ok || len(m.core.TagsOf(f)) == 0 {
		m.tagPanel.SetChecks(nil, false, root)
		return
	}
	m.tagPanel.SetChecks(nil, true, root)
	ctx, cancel := context.WithCancel(context.Background())
	m.checkCancel = cancel
	go func() {
		cs := m.core.Checks(ctx, f)
		if ctx.Err() != nil {
			return
		}
		fyne.Do(func() {
			if m.checkGen.Load() == gen {
				m.tagPanel.SetChecks(cs, false, root)
				if destination.AllBlocked(cs) {
					m.copyOne.Disable()
				}
			}
		})
	}()
}

// tagsFor returns the tags assigned to f, in display order.
func (m *MainWindow) tagsFor(f fileinfo.FileInfo) []tags.Tag {
	all := m.core.Tags()
	var out []tags.Tag
	for _, id := range m.core.TagsOf(f) {
		for _, t := range all {
			if t.ID == id {
				out = append(out, t)
				break
			}
		}
	}
	return out
}

// refreshFilmstrip shows thumbnails of the files around the current one.
func (m *MainWindow) refreshFilmstrip() {
	if !m.cfg.ShowFilmstrip {
		m.filmstrip.Object().Hide()
		return
	}
	m.filmstrip.Object().Show()
	files, cur := m.core.Neighbors(4, 4)
	m.filmstrip.Set(files, cur, m.tagsFor, m.loadThumb)
}

// loadThumb fetches the thumbnail of f in the background. stillShown tells
// whether its tile still exists; stale loads are dropped before the (costly)
// decode so that skimming does not queue work for tiles nobody sees.
func (m *MainWindow) loadThumb(f fileinfo.FileInfo, set func(image.Image), stillShown func() bool) {
	if m.thumbs == nil {
		return
	}
	switch strings.ToLower(f.Ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".bmp", ".tif", ".tiff", ".webp":
	default:
		return
	}
	go func() {
		m.thumbSem <- struct{}{}
		defer func() { <-m.thumbSem }()
		if !stillShown() {
			return
		}
		img, _, err := m.thumbs.Load(f.Path, f.Size, f.ModTime, func() (image.Image, error) {
			return preview.Thumbnail(f.Path, 160)
		})
		if err != nil {
			slog.Debug("no thumbnail", "path", f.Path, "error", err)
			return
		}
		if !stillShown() {
			return
		}
		fyne.Do(func() { set(img) })
	}()
}

// ensureStatuses computes destination statuses in the background when a
// status-based filter (ready / already copied / conflicts / errors) is active
// and they are not known yet.
func (m *MainWindow) ensureStatuses() {
	if !m.core.CurrentFilter().NeedsStatus() || m.core.StatusesKnown() || m.statusRunning {
		return
	}
	m.statusRunning = true
	m.statusLabel.SetText("Analysing destinations…")
	go func() {
		err := m.core.RefreshStatuses(context.Background(), nil)
		fyne.Do(func() {
			m.statusRunning = false
			switch {
			case err == nil:
				m.statusLabel.SetText("")
				m.update()
			case errors.Is(err, app.ErrStale):
				m.update() // inputs changed meanwhile: run again
			default:
				m.statusLabel.SetText("Could not analyse destinations: " + err.Error())
			}
		})
	}()
}
