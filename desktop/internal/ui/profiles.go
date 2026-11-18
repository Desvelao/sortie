package ui

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/launch"
	"sortie/internal/profile"
)

// stamp identifies a version of the profile file on disk.
type stamp struct {
	mtime time.Time
	size  int64
}

func statStamp(path string) (stamp, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return stamp{}, false
	}
	return stamp{st.ModTime(), st.Size()}, true
}

// profilePath is the file of the active profile.
func (m *MainWindow) profilePath() string {
	if m.profDir == "" {
		return ""
	}
	return profile.ResolvePath(m.profDir, m.core.ProfileID())
}

// rememberProfileFile records the current state of the profile file so the
// watcher only reacts to changes made from outside.
func (m *MainWindow) rememberProfileFile() {
	if p := m.profilePath(); p != "" {
		m.profStamp, m.profStampOK = statStamp(p)
	}
}

// refreshProfiles rebuilds the selector from the profiles folder and selects
// the active profile. Invalid files stay listed (marked) so they can be fixed.
func (m *MainWindow) refreshProfiles() {
	if m.profSelect == nil || m.profDir == "" {
		return
	}
	es, err := profile.List(m.profDir)
	if err != nil {
		slog.Warn("listing profiles failed", "error", err)
	}
	count := map[string]int{}
	for _, e := range es {
		count[e.Name]++
	}
	m.profLabels = map[string]string{}
	var labels []string
	active := m.core.ProfileID()
	selected := ""
	for _, e := range es {
		label := e.Name
		if count[e.Name] > 1 {
			label = fmt.Sprintf("%s (%s)", e.Name, e.ID)
		}
		if e.Err != nil {
			label = fmt.Sprintf("⚠ %s (invalid)", e.ID)
		}
		m.profLabels[label] = e.ID
		labels = append(labels, label)
		if e.ID == active {
			selected = label
		}
	}
	sort.Strings(labels)
	if selected == "" { // the active profile is the unsaved recovery profile
		selected = m.core.Profile().Name + " (unsaved)"
		labels = append([]string{selected}, labels...)
		m.profLabels[selected] = active
	}
	m.profSelecting = true
	m.profSelect.Options = labels
	m.profSelect.SetSelected(selected)
	m.profSelecting = false
}

// onProfileSelected switches profiles when the user picks one.
func (m *MainWindow) onProfileSelected(label string) {
	if m.profSelecting {
		return
	}
	id := m.profLabels[label]
	if id == "" || id == m.core.ProfileID() {
		return
	}
	if !m.switchProfile(id) {
		m.refreshProfiles() // put the selector back on the active profile
	}
}

// switchProfile makes another profile of the folder the active one. It
// reports whether it succeeded; failures are shown and nothing changes.
func (m *MainWindow) switchProfile(id string) bool {
	es, err := profile.List(m.profDir)
	if err != nil {
		dialog.ShowError(err, m.win)
		return false
	}
	for _, e := range es {
		if e.ID != id {
			continue
		}
		if e.Err != nil {
			dialog.ShowError(fmt.Errorf("profile %q cannot be used until its file is fixed:\n%w", id, e.Err), m.win)
			return false
		}
		if err := m.core.ApplyProfile(id, e.Profile, m.profDir); err != nil {
			dialog.ShowError(err, m.win)
			return false
		}
		m.cfg.ActiveProfile = id
		m.saveConfig()
		m.rememberProfileFile()
		m.recursive.SetChecked(e.Profile.Recursive)
		m.applyShortcuts() // the new profile has its own tag keys
		m.refreshProfiles()
		m.rescan()
		m.update()
		return true
	}
	dialog.ShowError(fmt.Errorf("profile %q no longer exists", id), m.win)
	return false
}

// reloadProfile re-reads the active profile's file and applies it. On any
// problem the profile in use stays as it is. manual=true reports the outcome
// in a dialog; automatic reloads (file edited elsewhere) use the status line.
func (m *MainWindow) reloadProfile(manual bool) {
	path := m.profilePath()
	if path == "" {
		return
	}
	m.rememberProfileFile() // do not retry the same broken version every tick
	p, err := profile.Load(path)
	if err == nil {
		err = m.applyReloaded(p)
	}
	if err != nil {
		slog.Warn("profile reload failed", "path", path, "error", err)
		if manual {
			dialog.ShowError(fmt.Errorf("the profile in use was kept:\n%w", err), m.win)
		} else {
			m.statusLabel.SetText("Profile file has errors, keeping the previous version: " + err.Error())
		}
		return
	}
	if !manual {
		m.statusLabel.SetText("Profile reloaded after the file changed")
	}
}

func (m *MainWindow) applyReloaded(p profile.Profile) error {
	old := m.core.Profile()
	if err := m.core.ApplyProfile(m.core.ProfileID(), p, m.profDir); err != nil {
		return err
	}
	m.recursive.SetChecked(p.Recursive)
	m.refreshProfiles()
	m.applyShortcuts()
	if old.InputDirectory != p.InputDirectory || old.Recursive != p.Recursive {
		m.rescan()
	}
	m.update()
	return nil
}

// checkProfileFile reloads the active profile if its file changed on disk
// (edited in an external editor). It does nothing while a dialog is open so
// edits in Settings are not clobbered; the change is picked up afterwards.
func (m *MainWindow) checkProfileFile() {
	path := m.profilePath()
	if path == "" || m.dialogOpen {
		return
	}
	cur, ok := statStamp(path)
	if ok == m.profStampOK && cur == m.profStamp {
		return
	}
	if !ok { // removed or being replaced: wait for the next version
		return
	}
	m.reloadProfile(false)
}

// startProfileWatcher polls the profile file; polling is cheap (one stat) and
// works the same for every editor and file system.
func (m *MainWindow) startProfileWatcher() {
	if m.profDir == "" || m.stopWatch != nil {
		return
	}
	m.rememberProfileFile()
	stop := make(chan struct{})
	m.stopWatch = stop
	go func() {
		t := time.NewTicker(1500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				fyne.Do(m.checkProfileFile)
			}
		}
	}()
}

func (m *MainWindow) stopProfileWatcher() {
	if m.stopWatch != nil {
		close(m.stopWatch)
		m.stopWatch = nil
	}
}

// ---- external programs ----

func (m *MainWindow) openProfileInEditor() {
	path := m.profilePath()
	if path == "" {
		dialog.ShowInformation("Profile", "Profile changes are not saved to a file in this session.", m.win)
		return
	}
	if err := launch.Edit(launch.DefaultEnv(), m.cfg.Editor, path, launch.Exec); err != nil {
		dialog.ShowError(err, m.win)
	}
}

func (m *MainWindow) showProfileInFolder() {
	path := m.profilePath()
	if path == "" {
		return
	}
	if err := launch.Show(launch.DefaultEnv(), path, launch.Exec); err != nil {
		dialog.ShowError(fmt.Errorf("cannot open the file manager: %w", err), m.win)
	}
}

func (m *MainWindow) openProfilesFolder() {
	if err := launch.Open(launch.DefaultEnv(), m.profDir, launch.Exec); err != nil {
		dialog.ShowError(err, m.win)
	}
}

// ---- managing profiles ----

// showManageMenu pops up the profile actions next to the button.
func (m *MainWindow) showManageMenu(anchor fyne.CanvasObject) {
	if m.profDir == "" || m.dialogOpen {
		return
	}
	menu := fyne.NewMenu("",
		fyne.NewMenuItem("New profile…", m.newProfile),
		fyne.NewMenuItem("Duplicate this profile…", m.duplicateProfile),
		fyne.NewMenuItem("Rename this profile…", m.renameProfile),
		fyne.NewMenuItem("Delete this profile…", m.deleteProfile),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Import profile file…", m.importProfile),
		fyne.NewMenuItem("Export this profile…", m.exportProfile),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Open profiles folder", m.openProfilesFolder),
	)
	pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(anchor).Add(fyne.NewPos(0, anchor.Size().Height))
	widget.ShowPopUpMenuAtPosition(menu, m.win.Canvas(), pos)
}

// askName shows a one-field dialog and calls ok with the entered text.
func (m *MainWindow) askName(title, confirm, initial string, ok func(name string)) {
	entry := widget.NewEntry()
	entry.SetText(initial)
	m.dialogOpen = true
	d := dialog.NewForm(title, confirm, "Cancel", []*widget.FormItem{widget.NewFormItem("Name", entry)}, func(accepted bool) {
		m.dialogOpen = false
		if accepted {
			ok(entry.Text)
		}
	}, m.win)
	d.SetOnClosed(func() { m.dialogOpen = false })
	d.Resize(fyne.NewSize(420, 140))
	d.Show()
	m.win.Canvas().Focus(entry)
}

func (m *MainWindow) newProfile() {
	m.askName("New profile", "Create", "", func(name string) {
		id, err := profile.Create(m.profDir, name)
		if err != nil {
			dialog.ShowError(err, m.win)
			return
		}
		m.switchProfile(id)
	})
}

func (m *MainWindow) duplicateProfile() {
	m.askName("Duplicate profile", "Duplicate", m.core.Profile().Name+" copy", func(name string) {
		id, err := profile.Duplicate(m.profDir, m.core.ProfileID(), name)
		if err != nil {
			dialog.ShowError(err, m.win)
			return
		}
		m.switchProfile(id)
	})
}

func (m *MainWindow) renameProfile() {
	m.askName("Rename profile", "Rename", m.core.Profile().Name, func(name string) {
		if err := profile.Rename(m.profDir, m.core.ProfileID(), name); err != nil {
			dialog.ShowError(err, m.win)
			return
		}
		m.reloadProfile(true)
	})
}

// deleteWarning says what deleting profile id discards.
func (m *MainWindow) deleteWarning(id string) string {
	if id == "" {
		return ""
	}
	n, err := m.core.TaggedFileCount(id)
	if err != nil {
		return "Its tag assignments will be removed."
	}
	return fmt.Sprintf("%d tagged file(s) will lose their classification. The profile file is moved to .trash.", n)
}

func (m *MainWindow) deleteProfile() {
	es, _ := profile.List(m.profDir)
	if len(es) <= 1 {
		dialog.ShowInformation("Delete profile", "This is the only profile; create another one first.", m.win)
		return
	}
	labels := make([]string, 0, len(es))
	ids := map[string]string{}
	for _, e := range es {
		l := fmt.Sprintf("%s (%s)", e.Name, e.ID)
		labels = append(labels, l)
		ids[l] = e.ID
	}
	warn := widget.NewLabel("")
	warn.Wrapping = fyne.TextWrapWord
	sel := widget.NewSelect(labels, func(l string) { warn.SetText(m.deleteWarning(ids[l])) })
	for _, l := range labels { // never open without a choice: Delete would do nothing
		if ids[l] != m.core.ProfileID() { // the profile in use cannot be deleted
			sel.SetSelected(l)
			break
		}
	}
	m.dialogOpen = true
	content := container.NewVBox(sel, warn)
	d := dialog.NewCustomConfirm("Delete profile", "Delete", "Cancel", content, func(yes bool) {
		m.dialogOpen = false
		id := ids[sel.Selected]
		if !yes || id == "" {
			return
		}
		if id == m.core.ProfileID() {
			dialog.ShowInformation("Delete profile", "The profile in use cannot be deleted. Switch to another profile first.", m.win)
			return
		}
		// Move the file first: if that fails the profile (and its tag
		// assignments) stay fully intact.
		moved, err := profile.Delete(m.profDir, id)
		if err != nil {
			dialog.ShowError(err, m.win)
			return
		}
		m.refreshProfiles()
		if err := m.core.DeleteProfileData(id); err != nil {
			dialog.ShowError(fmt.Errorf("the profile file was moved to %s but its tag assignments could not be removed: %w", moved, err), m.win)
			return
		}
		dialog.ShowInformation("Profile deleted", "Its tag assignments were removed. The file was moved to\n"+moved+"\nso it can be restored by hand.", m.win)
	}, m.win)
	d.SetOnClosed(func() { m.dialogOpen = false })
	d.Show()
}

func (m *MainWindow) importProfile() {
	m.dialogOpen = true
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		m.dialogOpen = false
		if err != nil {
			dialog.ShowError(err, m.win)
			return
		}
		if r == nil {
			return
		}
		path := r.URI().Path()
		r.Close()
		id, err := profile.Import(m.profDir, path)
		if err != nil {
			dialog.ShowError(err, m.win)
			return
		}
		m.switchProfile(id)
	}, m.win)
	d.SetFilter(storage.NewExtensionFileFilter([]string{".yaml", ".yml"}))
	d.SetOnClosed(func() { m.dialogOpen = false })
	d.Resize(fyne.NewSize(700, 450))
	d.Show()
}

func (m *MainWindow) exportProfile() {
	m.dialogOpen = true
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		m.dialogOpen = false
		if err != nil {
			dialog.ShowError(err, m.win)
			return
		}
		if w == nil {
			return
		}
		path := w.URI().Path()
		w.Close()
		if err := profile.Export(m.profDir, m.core.ProfileID(), path); err != nil {
			dialog.ShowError(err, m.win)
			return
		}
		dialog.ShowInformation("Profile exported", "Saved to "+path+"\nCopy it to another machine or project and use Import.", m.win)
	}, m.win)
	d.SetFileName(m.core.ProfileID() + ".yaml")
	d.SetOnClosed(func() { m.dialogOpen = false })
	d.Resize(fyne.NewSize(700, 450))
	d.Show()
}
