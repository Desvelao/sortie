package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"sortie/internal/media"
	"sortie/internal/vars"
)

var sourceKinds = []string{
	string(vars.KindManual), string(vars.KindFilename), string(vars.KindPath),
	string(vars.KindMetadata), string(vars.KindMTime), string(vars.KindConstant),
}

var kindHelp = map[vars.Kind]string{
	vars.KindManual:   "the value you type for a file (Ctrl+Y)",
	vars.KindFilename: "regex on the file name; the capture group (or whole match if none) is the value",
	vars.KindPath:     "regex on the folder path below the input directory (a/b/c)",
	vars.KindMetadata: "a metadata field (EXIF, PDF, audio, video); optional date format",
	vars.KindMTime:    "the file's modification time, formatted with a Go date layout",
	vars.KindConstant: "a fixed value",
}

func cloneDefs(in []vars.Definition) []vars.Definition {
	out := make([]vars.Definition, len(in))
	for i, d := range in {
		d.Sources = append([]vars.Source(nil), d.Sources...)
		if d.Validate.Min != nil {
			v := *d.Validate.Min
			d.Validate.Min = &v
		}
		if d.Validate.Max != nil {
			v := *d.Validate.Max
			d.Validate.Max = &v
		}
		out[i] = d
	}
	return out
}

func intText(p *int) string {
	if p == nil {
		return ""
	}
	return strconv.Itoa(*p)
}

// parseOptInt parses an optional integer entry: "" is nil.
func parseOptInt(s string) (*int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return nil, fmt.Errorf("%q is not a whole number", s)
	}
	return &n, nil
}

// freeVarName returns the first "varN" that no variable uses yet (len+1 would
// collide after deleting an earlier variable).
func freeVarName(defs []vars.Definition) string {
	used := map[string]bool{}
	for _, d := range defs {
		used[d.Name] = true
	}
	for n := 1; ; n++ {
		if name := fmt.Sprintf("var%d", n); !used[name] {
			return name
		}
	}
}

// buildVariablesTab edits the profile's variables: for each one an ordered
// chain of sources (first valid value wins), optional validation and a
// fallback. Nothing is applied until Save. It returns the tab, a function
// collecting the edited definitions, one returning their current names
// (for validating templates on the other tabs) and one returning the first
// unparsable input (numeric fields), which must block Save: the parsed value
// of such a field is dropped, so saving would silently remove the setting.
func (m *MainWindow) buildVariablesTab() (fyne.CanvasObject, func() []vars.Definition, func() []string, func() error) {
	work := cloneDefs(m.core.Profile().Variables)
	inputErr := map[string]string{} // widget key → parse problem
	box := container.NewVBox()
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	result := widget.NewLabel("")
	result.Wrapping = fyne.TextWrapWord

	sampleName := widget.NewEntry()
	sampleDir := widget.NewEntry()
	sampleName.SetText("IMG_2021_0315.jpg")
	sampleDir.SetPlaceHolder("folder below the input directory, e.g. clients/acme/2020")
	if f, ok := m.core.Current(); ok {
		sampleName.SetText(f.Name)
	}

	check := func() {
		if len(inputErr) > 0 {
			for _, e := range inputErr {
				status.SetText("✗ " + e)
				break
			}
			result.SetText("")
			return
		}
		set, err := vars.New(work)
		if err != nil {
			status.SetText("✗ " + err.Error())
			result.SetText("")
			return
		}
		status.SetText("✓ valid")
		mt := time.Now()
		if f, ok := m.core.Current(); ok {
			mt = f.ModTime
		}
		vals := set.ResolveAll(vars.FileContext{Name: strings.TrimSpace(sampleName.Text), RelDir: strings.Trim(strings.TrimSpace(sampleDir.Text), "/"), ModTime: mt})
		var lines []string
		for _, n := range set.Names() {
			v := vals[n]
			if v.OK() {
				lines = append(lines, fmt.Sprintf("%s = %s   (%s)", n, v.Value, v.Source))
			} else {
				lines = append(lines, fmt.Sprintf("%s: %s %s", n, v.Status, v.Detail))
			}
		}
		if len(lines) == 0 {
			lines = []string{"no variables"}
		}
		result.SetText(strings.Join(lines, "\n"))
	}
	sampleName.OnChanged = func(string) { check() }
	sampleDir.OnChanged = func(string) { check() }

	// setErr records or clears the parse problem of one widget.
	setErr := func(key string, err error) {
		if err != nil {
			inputErr[key] = err.Error()
		} else {
			delete(inputErr, key)
		}
	}

	var rebuild func()
	rebuild = func() {
		// the entries are recreated from `work`, which holds the last parsed
		// values: pending parse problems (keyed by position) are obsolete
		clear(inputErr)
		box.Objects = nil
		for vi := range work {
			vi := vi
			name := widget.NewEntry()
			name.SetText(work[vi].Name)
			name.SetPlaceHolder("variable name, e.g. year")
			name.OnChanged = func(s string) { work[vi].Name = strings.TrimSpace(s); check() }
			up := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
				if vi > 0 {
					work[vi-1], work[vi] = work[vi], work[vi-1]
					rebuild()
				}
			})
			down := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
				if vi < len(work)-1 {
					work[vi+1], work[vi] = work[vi], work[vi+1]
					rebuild()
				}
			})
			del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
				work = append(work[:vi:vi], work[vi+1:]...)
				rebuild()
			})
			box.Add(container.NewBorder(nil, nil, widget.NewLabelWithStyle("Variable", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
				container.NewHBox(up, down, del), name))

			box.Add(widget.NewLabel("Sources, tried in order — the first valid value wins:"))
			for si := range work[vi].Sources {
				si := si
				box.Add(m.sourceRow(&work[vi], si, check, rebuild, func(err error) { setErr(fmt.Sprintf("grp%d_%d", vi, si), err) }))
			}
			addSrc := widget.NewButtonWithIcon("Add source", theme.ContentAddIcon(), func() {
				work[vi].Sources = append(work[vi].Sources, vars.Source{Kind: vars.KindFilename, Pattern: "(.+)", Group: 1})
				rebuild()
			})

			regex := widget.NewEntry()
			regex.SetText(work[vi].Validate.Regex)
			regex.SetPlaceHolder("value must match this regex (optional)")
			regex.OnChanged = func(s string) { work[vi].Validate.Regex = s; check() }
			minE, maxE := widget.NewEntry(), widget.NewEntry()
			minE.SetText(intText(work[vi].Validate.Min))
			maxE.SetText(intText(work[vi].Validate.Max))
			minE.SetPlaceHolder("min (numeric)")
			maxE.SetPlaceHolder("max (numeric)")
			keyMin, keyMax := fmt.Sprintf("min%d", vi), fmt.Sprintf("max%d", vi)
			minE.OnChanged = func(s string) {
				v, err := parseOptInt(s)
				setErr(keyMin, err)
				work[vi].Validate.Min = v
				check()
			}
			maxE.OnChanged = func(s string) {
				v, err := parseOptInt(s)
				setErr(keyMax, err)
				work[vi].Validate.Max = v
				check()
			}
			fb := widget.NewEntry()
			fb.SetText(work[vi].Fallback)
			fb.SetPlaceHolder("value when no source matches (optional)")
			fb.OnChanged = func(s string) { work[vi].Fallback = strings.TrimSpace(s); check() }
			box.Add(addSrc)
			box.Add(widget.NewForm(
				widget.NewFormItem("Valid if", container.NewGridWithColumns(3, regex, minE, maxE)),
				widget.NewFormItem("Otherwise", fb),
			))
			box.Add(widget.NewSeparator())
		}
		if len(work) == 0 {
			box.Add(widget.NewLabel("No variables yet. Add one to use it in destination templates, e.g. {year}/{tag}/{filename}."))
		}
		box.Refresh()
		check()
	}
	add := widget.NewButtonWithIcon("Add variable", theme.ContentAddIcon(), func() {
		work = append(work, vars.Definition{Name: freeVarName(work), Sources: []vars.Source{{Kind: vars.KindManual}}})
		rebuild()
	})
	rebuild()

	tester := widget.NewForm(
		widget.NewFormItem("Test file name", sampleName),
		widget.NewFormItem("Test folder", sampleDir),
		widget.NewFormItem("Result", result),
	)
	note := widget.NewLabel("Metadata sources are not evaluated in this test. Removing a variable also removes manual values you typed for it.")
	note.Wrapping = fyne.TextWrapWord
	tab := container.NewBorder(nil, container.NewVBox(status, tester, note, add), nil, nil, container.NewVScroll(box))
	collect := func() []vars.Definition { return cloneDefs(work) }
	names := func() []string {
		var out []string
		for _, d := range work {
			if d.Name != "" {
				out = append(out, d.Name)
			}
		}
		return out
	}
	pending := func() error {
		if len(inputErr) == 0 {
			return nil
		}
		keys := make([]string, 0, len(inputErr))
		for k := range inputErr {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return fmt.Errorf("variables: %s", inputErr[keys[0]])
	}
	return tab, collect, names, pending
}

// sourceRow edits one source of a variable: its kind and the settings that
// kind needs.
func (m *MainWindow) sourceRow(d *vars.Definition, si int, check func(), rebuild func(), setGroupErr func(error)) fyne.CanvasObject {
	src := &d.Sources[si]
	kind := widget.NewSelect(sourceKinds, nil)
	kind.Selected = string(src.Kind)
	kind.OnChanged = func(k string) {
		if k == string(d.Sources[si].Kind) {
			return
		}
		ns := vars.Source{Kind: vars.Kind(k)}
		switch ns.Kind {
		case vars.KindFilename, vars.KindPath:
			ns.Pattern, ns.Group = "(.+)", 1
		case vars.KindMetadata:
			ns.Field = "date"
		case vars.KindMTime:
			ns.Format = "2006"
		case vars.KindConstant:
			ns.Value = "value"
		}
		d.Sources[si] = ns
		rebuild()
	}
	entry := func(text, hint string, set func(string)) *widget.Entry {
		e := widget.NewEntry()
		e.SetText(text)
		e.SetPlaceHolder(hint)
		e.OnChanged = func(s string) { set(s); check() }
		return e
	}
	var fields fyne.CanvasObject
	switch src.Kind {
	case vars.KindFilename, vars.KindPath:
		pat := entry(src.Pattern, "regex, e.g. (20[0-9]{2})", func(s string) { d.Sources[si].Pattern = s })
		grp := entry(strconv.Itoa(max(src.Group, 1)), "group", func(s string) {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n < 0 {
				setGroupErr(fmt.Errorf("capture group %q is not a whole number (0 or more)", strings.TrimSpace(s)))
				return
			}
			setGroupErr(nil)
			d.Sources[si].Group = n
		})
		grp.Resize(fyne.NewSize(60, grp.MinSize().Height))
		fields = container.NewBorder(nil, nil, nil, container.NewGridWrap(fyne.NewSize(70, grp.MinSize().Height), grp), pat)
	case vars.KindMetadata:
		field := widget.NewSelectEntry(media.FieldNames)
		field.SetText(src.Field)
		field.OnChanged = func(s string) { d.Sources[si].Field = strings.TrimSpace(s); check() }
		format := entry(src.Format, "date layout, e.g. 2006-01 (optional)", func(s string) { d.Sources[si].Format = strings.TrimSpace(s) })
		fields = container.NewGridWithColumns(2, field, format)
	case vars.KindMTime:
		fields = entry(src.Format, "date layout, e.g. 2006 or 2006-01", func(s string) { d.Sources[si].Format = strings.TrimSpace(s) })
	case vars.KindConstant:
		fields = entry(src.Value, "value", func(s string) { d.Sources[si].Value = s })
	default:
		l := widget.NewLabel(kindHelp[vars.KindManual])
		l.TextStyle = fyne.TextStyle{Italic: true}
		fields = l
	}
	up := widget.NewButtonWithIcon("", theme.MoveUpIcon(), func() {
		if si > 0 {
			d.Sources[si-1], d.Sources[si] = d.Sources[si], d.Sources[si-1]
			rebuild()
		}
	})
	down := widget.NewButtonWithIcon("", theme.MoveDownIcon(), func() {
		if si < len(d.Sources)-1 {
			d.Sources[si+1], d.Sources[si] = d.Sources[si], d.Sources[si+1]
			rebuild()
		}
	})
	del := widget.NewButtonWithIcon("", theme.DeleteIcon(), func() {
		d.Sources = append(d.Sources[:si:si], d.Sources[si+1:]...)
		rebuild()
	})
	tip := widget.NewLabel(kindHelp[src.Kind])
	tip.TextStyle = fyne.TextStyle{Italic: true}
	tip.Wrapping = fyne.TextWrapWord
	return container.NewVBox(
		container.NewBorder(nil, nil, container.NewHBox(widget.NewLabel(fmt.Sprintf("%d.", si+1)), kind), container.NewHBox(up, down, del), fields),
		tip,
	)
}
