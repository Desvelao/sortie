# Architecture

Business logic is independent of Fyne and testable without a display (`go test ./...`).

| Package | Responsibility |
|---|---|
| `cmd/sortie` | wiring: config → logging → database → profile (activate) → `app` → UI |
| `internal/app` | application state: file list, filtered view, current position, active profile, variable values, tags, destination checks, copy orchestration, session |
| `internal/scanner`, `fileinfo` | read-only discovery, MIME detection (content first, extension second) |
| `internal/profile` | profile = one use case as a YAML file: strict load with line-numbered errors, validation, comment-preserving save, list/create/duplicate/rename/delete/import/export, active-profile selection |
| `internal/vars` | user-defined variables: ordered source chain (manual, filename, path, metadata, mtime, constant), validation, fallback; knows nothing about what a variable is for |
| `internal/pathtpl` | destination template syntax (`{var\|default}`, `{var:format}`), validation, rendering with path-component sanitising |
| `internal/launch` | opens the profile in the user's editor / file manager; pure command builders (no shell), thin runner |
| `internal/tags` | tag model (incl. optional per-tag template), validation (unique names/keys, safe destinations) |
| `internal/destination` | resolves (file, variables, tag) to a path via `pathtpl`, containment (incl. symlinks), SHA-256 duplicate/conflict checks, bounded async hashing |
| `internal/copier` | batch engine: temp file, verification, no-clobber rename, conflict policies, history, cancellation |
| `internal/database` | SQLite (pure-Go driver), schema created on open, inventory, per-profile tags/assignments/manual values, history, session |
| `internal/config` | application-wide JSON config (not use-case settings): defaults, validation, atomic save |
| `internal/shortcuts` | shortcut parsing/validation (no GUI) |
| `internal/media`, `preview`, `player`, `cache` | metadata readers (`media.Fields` feeds `metadata` variable sources), preview handlers, audio playback, thumbnail cache |
| `internal/logging` | slog setup with a rotating log file |
| `scripts/`, `packaging/` | build, release validation and clean-environment verification; install/uninstall scripts, desktop entry, icon, release README |
| `internal/ui` | Fyne windows/dialogs only; slow work runs in goroutines and returns via `fyne.Do` |

## Decisions
- **The app provides the mechanism, profiles provide the use case.** Nothing in the code is specific to years: `year` is just a variable in the starter profile. Adding a new way to extract data means a new source kind in `vars`; adding a new way to use it means a template feature in `pathtpl`.
- **YAML profiles are the source of truth for a use case** (tags, variables, paths); the JSON config only holds application-wide settings. SQLite holds operational state and is keyed by profile id. A profile that fails to load never replaces the one in use, and a broken file is never overwritten.
- **Tags are keyed by name within a profile.** Ids are assigned by the database, so profile files stay free of numbers; the price is that a rename is a new tag. Removing a tag removes its assignments, never copied files; history keeps the tag name.
- **Profile switching and reload are one operation (`Application.ApplyProfile`)**: validate everything first, then swap atomically under the lock, so an invalid profile changes nothing.
- **Edits made in the app preserve comments**: the new YAML is rendered from the model and the old file's comments are carried over by key (tags/variables by name).
- **One SQLite connection** (`MaxOpenConns(1)`) serialises access; transactions never call back into the pool.
- **Safety invariants:** sources are opened read-only; destinations are only written by the copier through temp files and `renameat2(RENAME_NOREPLACE)` (with fallbacks); overwriting requires the replace policy plus a confirmation; nothing in the destination is ever deleted by the application. Rendered template values are single path components, and the result is re-checked against the root (incl. symlinks).
- **Commands for external programs are argument lists**, never shell strings, so file names cannot inject anything.
- **Filtered view:** `Application.view` holds indices of matching files; the current file is pinned into the view while it is being edited, and dropped on the next move.
- **Destination statuses** are computed on demand (never stored, they depend on the filesystem) and invalidated with a generation counter so stale background results are discarded.
- **Previews are plugins** (`PreviewHandler`) run with panic recovery and a timeout; the UI never depends on a preview succeeding.
- **Why no PDF/video rendering:** the only mature renderers are CGO/AGPL; the spec's fallback (metadata + open externally) is used.
