# Profiles, variables and destination templates

Sortie does not know what a "year" or a "client" is. A **profile** tells it what to extract from
each file (*variables*) and where each tag sends the file (*templates* that use those variables).
Everything about one use case lives in one YAML file, so you can keep several (family photos, music,
client documents…), switch between them and share them.

Application-wide settings (log level, shortcuts, which profile is active…) are separate and stay in
`~/.config/sortie/config.json`.

## Where profiles live

`~/.config/sortie/profiles/*.yaml` (or `.yml`; change the folder with `profiles_dir` in
`config.json`). The file name without extension is the profile's **id**; `name:` inside the file is
what you see in the selector. Ready-made examples are in [`docs/profiles/`](profiles/): copy one into
the folder, or use **Profiles ▾ → Import profile file…**.

A fresh install gets a starter profile (`default.yaml`: year from the file name, then metadata).

## File format

```yaml
version: 1                       # format version (required)
name: Family photos              # shown in the selector (required)
description: free text           # optional
input_directory: ~/Pictures/inbox
recursive: true
destination_root: ~/Pictures/Sorted
destination_template: "{year}/{tag}/{filename}"   # default for tags without their own
conflict_policy: skip            # skip | rename | replace (preselected in the copy dialog)

variables:                       # what to extract from each file
  - name: year
    sources: [...]               # tried in order, see below
    validate: {min: 1990, max: 2100}
    fallback: unknown

tags:                            # order here = order in the app
  - name: Family
    shortcut: "1"                # A–Z, 0–9 or F1–F12
    destination: Family          # what {tag} expands to (default: the tag name)
    destination_template: "..."  # optional: replaces the profile template for this tag
    enabled: true                # default true
    color: "#E4572E"             # optional #RRGGBB / #RGB marker color; default: automatic palette
```

* `~` and relative paths are allowed in `input_directory` and `destination_root`; relative paths are
  taken from the folder that holds the profile file. The application keeps them as written.
* Unknown keys, wrong types and invalid values are errors that name the line, e.g.
  `line 12: tags[2]: tag "Work" destination_template: unknown variable {clent}`.
* `conflict_policy: replace` only preselects the option: overwriting still asks for confirmation.

## Variables

A variable has a `name` (letters, digits, `_`; not one of the built-in names `tag filename name ext
parent`) and an ordered list of **sources**. For each file the sources are tried in order and the
**first valid value wins**.

| `kind` | Settings | Takes the value from |
|---|---|---|
| `manual` | – | what you type for that file (**Ctrl+Y**); manual values are saved per profile and always win when `manual` is listed first. Not range-checked by `validate` on read, but checked when you enter them |
| `filename` | `pattern`, `group` | a regular expression on the file name; `group` is the capture group (default 1; a pattern without parentheses uses the whole match). Every match is tried, the first valid one wins |
| `path` | `pattern`, `group` | a regular expression on the folder path **below the input directory**, `/`-separated (`clients/acme/2020`) |
| `metadata` | `field`, `format` | a field read from the file (see below); `format` re-renders dates |
| `mtime` | `format` | the file's modification time, formatted with a Go date layout (default `2006`) |
| `constant` | `value` | a fixed value |

Regular expressions use Go syntax (RE2: no look-ahead/look-behind). To match a standalone year use
`(^|[^0-9])(20[0-9]{2})([^0-9]|$)` with `group: 2`.

**Date layouts** are Go reference layouts: `2006` year, `2006-01` year-month, `2006-01-02` full date,
`01` month, `Jan` month name.

**Metadata fields** (absent when the file has no such data or is unsupported):
`date` (RFC 3339), `year`, `title`, `author`, `artist`, `album`, `genre`, `camera`, `subject`,
`creator`, `producer`, `pages`, `duration` (seconds), `width`, `height`, `format`. Sources: EXIF
(JPEG/TIFF), PDF info, audio tags (MP3/FLAC/OGG/M4A), video containers (MP4/MOV/MKV/WebM/3GP).
Metadata is only read when an earlier source did not already decide, and is cached per file version.

**Validation** (optional) rejects values: `regex` (the whole value must match), and/or `min`/`max`
(the value must be a whole number in range). A rejected value is skipped and the next source is tried.

**Fallback** (optional) is used when no source gave a valid value. Without a fallback the variable is
*unresolved*; see below for what that means for copying.

## Destination templates

A template is a relative path under `destination_root`. Placeholders:

| Placeholder | Meaning |
|---|---|
| `{tag}` | the tag's `destination` folder (may be nested, e.g. `Work/Invoices`) |
| `{filename}` `{name}` `{ext}` | file name; name without extension; extension with its dot (empty if none) |
| `{parent}` | name of the folder the source file is in (unresolved for a file directly in `/`) |
| `{var}` | a variable of the profile |
| `{var\|default}` | the variable, or the literal `default` when it is unresolved |
| `{var:format}` | `upper`, `lower`, `slug` (`Acme Corp` → `acme-corp`; letters of any script are kept, other characters become `-`), `padN` (zero-pad to N: `{month:pad2}`) |

`{var:slug\|none}` combines both (format first). The last path component must contain `{filename}` or
`{name}`, and `{filename}` may only be in that last component.

Values are sanitised into **one path component**: a `/` in a value becomes `_`, control characters
are dropped, and `..` can never escape. Files are never written outside `destination_root` (symlinks
pointing outside are rejected too).

**Unresolved variables.** If a template uses a variable that has no value and no `|default`, that
(file, tag) gets the status `UNRESOLVED_VAR` and is **never copied**. Give the variable a
`fallback`, or the placeholder a `|default`, to copy such files into a catch-all folder. The
*Unresolved variables* filter lists the files where any variable is unresolved.

**Per-tag templates.** A tag's own `destination_template` replaces the profile's for that tag only,
so one tag can sort differently (`Tax/{year}/{client}/{filename}`). A file with several tags is
copied once per tag, each by its own template.

## Working with profiles in the app

* The **Profile** selector switches profile: variables, tags, destination and input directory change
  and the folder is rescanned. Tag assignments and manual values are kept **per profile**, so the same
  folder can be sorted two ways without mixing them.
* **Edit file** opens the profile in your editor: the app's *Profile editor* setting (Settings →
  General), else `$VISUAL`, else `$EDITOR`, else the desktop default. Terminal editors (vim, nano…)
  open in a terminal emulator (`$TERMINAL` or the first one found). **Show in folder** reveals the
  file in your file manager.
* **Reload** re-reads the file; this also happens by itself about a second after you save it in your
  editor. If the new version has errors the app **keeps the previous one** and shows the error
  (with the line number). While Settings is open, outside edits wait until it is closed.
* **Profiles ▾**: New (starter profile), Duplicate, Rename, Delete, Import, Export, Open profiles
  folder. *Rename* changes only the `name:`; the file name (id) stays. *Delete* moves the file to
  `profiles/.trash/` (restore it by moving it back) and removes the profile's tag assignments; the
  profile in use cannot be deleted. *Import* validates the file and never replaces an existing one.
* **Settings → Variables / Tags / Destination** edit the same file. Saving from the app keeps the
  comments you wrote (matched by key and by tag/variable name); formatting is normalised.

## Things to know

* **Tags are identified by name within a profile.** Renaming a tag in the file makes it a *new* tag:
  its file assignments are dropped (copied files are never touched, and copy history keeps the old
  name). Rename tags only if you accept that, or re-tag afterwards.
* Removing a variable from a profile also removes the manual values you typed for it.
* A profile is plain text: keep it in version control, or copy it to another machine. Its tagging
  (SQLite) is per machine and keyed by the profile id.
* Profiles do not contain secrets; absolute paths in them are machine-specific, so prefer `~` or
  relative paths in profiles you share.
