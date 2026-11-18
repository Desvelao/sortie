# Sortie

A keyboard-driven Linux desktop app (Go + Fyne) for classifying files with tags and copying them into
folders you define — by default `year/tag`, but any layout built from data extracted from the file name,
folder, or metadata (artist, client, month…). Any regular file can be classified — previews are optional
conveniences. A **profile** (a YAML file) describes one use case; you can keep several and share them:
see [docs/PROFILES.md](../docs/PROFILES.md).
Version: see `../VERSION` (shared with the Android app); release process: [docs/RELEASE.md](docs/RELEASE.md).

## Workflow
1. **Select a directory** (Ctrl+O). Files are scanned (optionally recursive); symlinks are skipped; sources are never modified.
2. **Browse** with ←/→, Home/End, the filmstrip or the *Show* filter.
3. **Tag** with the tag keys (default 1–9); pressing a key again removes the tag. A file can have many tags.
4. **Variables** (e.g. `year`) are defined by the active *profile*: each is extracted from the filename, folder path, file metadata or modification time (first source that yields a valid value wins), or set manually (Ctrl+Y). The starter profile defines `year` (manual > filename `20xx` > metadata).
5. **Check** the tag panel: each assigned tag shows its destination and `READY / ALREADY_COPIED / CONFLICT / UNRESOLVED_VAR / INVALID_PATH / ERROR`.
6. **Copy** the current file (Ctrl+Enter) or all classified files (Ctrl+Shift+C), then resume or retry any time.

State is saved continuously: closing the app loses nothing; the active profile, last directory, position, tags, manual values and copy history are restored.

## Shortcuts (all configurable in Settings → Shortcuts / Tags)
| Action | Default | | Action | Default |
|---|---|---|---|---|
| Next / previous file | → / ← | | Copy all classified | Ctrl+Shift+C |
| First / last file | Home / End | | Copy current file | Ctrl+Enter |
| Toggle tag | 1–9 (per tag) | | Edit manual variable value | Ctrl+Y |
| Select directory | Ctrl+O | | Play/pause audio | Space |
| Settings | Ctrl+, | | Close dialog / cancel copy | Escape (fixed) |

Keys in the `shortcuts` map of `config.json` are these action IDs (the Settings dialog is the easier way to edit them):

| ID | Action | ID | Action |
|---|---|---|---|
| `next` / `prev` | Next / previous file | `copy_all` | Copy all classified |
| `first` / `last` | First / last file | `copy_current` | Copy current file |
| `open_dir` | Select input directory | `focus_year` | Edit manual variable value |
| `settings` | Open settings | `play` | Play / pause audio |

Bindings are `Key` or `Ctrl+Key`, `Alt+Key`, `Ctrl+Shift+Key` (Shift needs Ctrl or Alt). Tag keys are single keys A–Z, 0–9, F1–F12 and cannot collide with plain application keys. Changes apply on Save, no restart. Shortcuts never fire while a text field has focus.

## Filters (the *Show* menu)
All · Untagged · Tagged · Unresolved variables · Ready to copy · Already copied · Conflicts · Files with errors.
Status filters need destination checks (hashing), which run in the background and are refreshed on rescan or settings changes. A file you just changed stays visible until you move away from it, so tagging in the *Untagged* view never makes the file vanish under you.
Rescans keep classifications: new files are added, vanished files are marked `MISSING` (never deleted), modified files are updated, and **renamed files** (unique size + mtime match) keep their tags and manual values.

## Profiles
A profile holds everything for one use case: input and destination folders, tags, the **variables** to extract from each file and the destination **templates** that use them. It is a plain YAML file in `~/.config/sortie/profiles/` — copy it to reuse it for another project or machine. Four ready-made examples (photos by year, music by artist/album, client documents, invoices by month) are in [docs/profiles/](../docs/profiles/).

```yaml
name: Music library
destination_root: ~/Music/Library
destination_template: "{artist}/{album}/{filename}"
variables:
  - name: artist
    sources: [{kind: manual}, {kind: metadata, field: artist}]
    fallback: Unknown artist
tags:
  - {name: Keep, shortcut: K}
  - {name: Favourite, shortcut: F, destination_template: "Favourites/{artist}/{filename}"}
```

In the window: pick a profile in the **Profile** selector; **Edit file** opens it in your editor (`$VISUAL`/`$EDITOR`, or the *Profile editor* setting), **Show in folder** reveals it, and edits are picked up automatically when you save (an invalid edit is reported and the previous version stays in use). **Profiles ▾** creates, duplicates, renames, deletes, imports and exports profiles. Tagging is kept per profile. Full reference: [docs/PROFILES.md](../docs/PROFILES.md).

## Previews (optional; a failing or missing preview never blocks sorting)
Images (JPG/PNG/GIF/BMP/TIFF/WebP, EXIF orientation/date; SVG), text (first 64 KiB), PDF (metadata, page count, first-page text — no page rendering), audio (tags, duration; playback for MP3/FLAC/OGG/16-bit WAV via Oto), video (MP4/MOV/MKV/WebM/AVI metadata only). Unknown files show generic metadata and *Open with default application*.
The **filmstrip** shows thumbnails of neighbouring files; thumbnails are cached as JPEGs in `~/.cache/sortie/thumbnails` (size-limited, least-recently-used pruned, safe to delete).

## Destinations and copying
Settings → Destination: root (must exist) and the profile's default template (starter profile: `{year}/{tag}/{filename}`). Templates can use `{tag}`, `{filename}`, `{name}`, `{ext}`, `{parent}` (the source file's folder name) and any variable of the profile; `{var|default}` supplies a value when a variable cannot be determined (e.g. `{year|unknown}`), `{var:upper}`/`lower`/`slug`/`pad4` format it. Each tag may have its own template (Settings → Tags). Paths can never leave the root (`..`, absolute paths and symlinks pointing outside are rejected).
Copying is write-safe: temp file next to the target → fsync → read-back SHA-256 verification → atomic **no-clobber** rename. Sources are opened read-only and re-checked before/after. Conflict policies (`conflict_policy` in the profile file: `skip` | `rename` | `replace`): skip (default), keep both = `rename` (`name (1).ext`), replace (explicit confirmation). Identical files are skipped by content (size, then SHA-256), so re-running resumes an interrupted batch. A crash leaves `IN_PROGRESS` history rows that become `INTERRUPTED` at the next start; stray `.sortie-*.tmp` files are reported, never auto-deleted.

## Files and data
| What | Where |
|---|---|
| Application settings (JSON, atomic writes, validated) | `~/.config/sortie/config.json` |
| Profiles (one YAML file per use case) | `~/.config/sortie/profiles/*.yaml` |
| Operational state (inventory, per-profile tag assignments and manual values, session, copy history; SQLite) | `~/.local/share/sortie/database.sqlite` |
| Log (structured, rotated at 2 MiB, 2 backups, level in config) | `~/.local/state/sortie/sortie.log` |
| Thumbnail cache | `~/.cache/sortie/thumbnails/` |

`config.json` holds only application-wide settings: `active_profile`, `profiles_dir`, `editor`, `shortcuts` (action → binding; missing = default), `log_level`, `show_filmstrip`, `thumbnail_cache_mb`. Everything use-case specific (input directory, destination, tags, variables) lives in a **profile** (see above). Tags are identified by name within a profile — renaming a tag in the file starts it over (its file assignments are dropped). Invalid files are rejected with a readable message and defaults are used; the app never needs SQLite to be managed by hand. Set `XDG_*` variables to relocate everything.

## Development (Docker only — nothing is installed on the host)
    make image   # build the dev image (Go 1.26, Fyne CGO deps, Xvfb)
    make check   # gofmt + vet + all tests + build + dynamic-deps listing + Xvfb startup test
    make test | test-race | vet | fmt | tidy | build | smoke | shell   # test-race = race detector, as in the release
    make run     # run the GUI via X11 passthrough (needs a local X server/XWayland)
    make release         # full release build + validation -> release/sortie-VERSION-linux-amd64.tar.gz
                         # (the version is read from the repo-root `VERSION` file, shared with the Android app: bump it first, see docs/RELEASE.md)
    make verify-release  # run that archive on clean Debian/Ubuntu/Fedora/Arch/openSUSE containers
The dev binary is `release/sortie`. Architecture notes: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Installing and running a release
Download `sortie-VERSION-linux-amd64.tar.gz`, check it with `sha256sum -c sortie-VERSION-linux-amd64.tar.gz.sha256`, extract it and run `./sortie`
(or `./install.sh` for a per-user install with menu entry; `sudo ./install.sh --system` for all users). No Go, Python or SQLite is needed — the database engine is compiled in.
`./sortie --self-test` runs a headless end-to-end check in a temporary folder; `--version` prints the version.

## Runtime requirements
- Linux x86_64, **glibc ≥ 2.34**, a desktop session (X11 or Wayland) with OpenGL (hardware or Mesa software).
- The binary is dynamically linked (CGO is required by Fyne) and is **not** static. At start-up it needs only `libGL.so.1`, `libwayland-client.so.0` (and through libGL `libX11`), plus glibc. GLFW loads more libraries on demand (`libXcursor`, `libXrandr`, `libXinerama`, `libXi`, `libXxf86vm`, `libxkbcommon`, `libwayland-cursor`, `libwayland-egl`); install them for full functionality (cursors, multi-monitor, keyboard layouts, Wayland). Exact package names per distribution: see `packaging/README.txt.in` / docs/RELEASE.md.
- Optional: PulseAudio/PipeWire-pulse or ALSA for audio playback (loaded dynamically; without it Play reports an error).
- Verified distributions (clean containers, release archive, see docs/RELEASE.md): Debian 12, Ubuntu 22.04, Ubuntu 24.04, Fedora 41, Arch Linux, openSUSE Tumbleweed. Not supported: glibc < 2.34 (Debian 11, Ubuntu 20.04) and musl (Alpine).

## Known limitations
- No PDF page rendering/zoom and no video frame preview/playback (no suitable pure-Go library); AAC/M4A is metadata-only.
- The clean-environment tests use Xvfb with Mesa software rendering. Real GPUs, real Wayland sessions, real sound devices and other CPU architectures (e.g. aarch64) have not been tested.
- Mouse-driven parts (filmstrip, tag panel clicks) and the settings dialogs have been exercised through Fyne's test driver only, not by hand on a real display.
- The destination root must already exist; the app never creates it (protects against unmounted drives).
