# AGENTS.md

Android-specific guidance for AI coding agents. Shared rules (feature and version parity, shared `VERSION`, profile
format, Docker-only toolchain, secrets) are in the root [`AGENTS.md`](../AGENTS.md) and apply here too. `README.md` is for humans.

## What this is

A native Android app (Kotlin, Jetpack Compose, Material 3) that tags files and copies them into folders built from
templates, with the features of the desktop Sortie (`../desktop`, Go + Fyne). Profiles are
desktop-compatible YAML. Copy only: sources are never modified or deleted. All files access
(`MANAGE_EXTERNAL_STORAGE`) and real paths; no `INTERNET` permission.

## Hard constraints (do not break)

1. **Docker-only toolchain.** Never ask the user to install Java, Gradle, Android Studio or SDK packages on the
   host. Every build, test and `adb` command runs through `docker compose run --rm dev ...` (or `make`).
2. **Name only in `app.properties`.** The app name, id and URL are build parameters kept in the tracked
   `app.properties`. Do not hard-code the name in code, resources or docs: read it from `R.string.app_name` /
   `BuildConfig`. Internal names stay neutral (`App`, `app.sortie`, `android-dev`).
3. **Native only.** No cross-platform frameworks, no WebView.
4. Do not commit secrets: `release.jks` and `keystore.properties` are git-ignored. Check `git status` before
   committing, and never `git add -A` blindly.
5. **Changelog.** Every user-visible change adds a line under `[Unreleased]` in `CHANGELOG.md`. CI
   (`../.github/workflows/release.yml`) builds on GitHub's runner, not in Docker; the Docker-only rule is about the
   user's host. When a release is tagged, its entries move from `[Unreleased]` to a `## [x.y.z] - date` heading
   and `[Unreleased]` starts empty. The version comes from the root `VERSION` file, shared with the desktop app.

## Commands

```bash
make image      # once, and after Dockerfile changes
make check      # the standard check: unit tests and lint
make check-release  # release lint and R8 shrinking (no signing needed)
make debug      # debug APK
make install    # needs a device/emulator
make release    # needs signing (README)
```

`RUN` defaults to the Docker wrapper; CI calls `make -C android check RUN=` and `make -C android release RUN=` so both use the same
Gradle commands. Keep the Makefile and the workflow in sync.

A change is done when tests pass, `lint` reports **0 errors**, and `make check-release` still passes. Do not chain
`git commit` after a check with `&&` through a pipe or `grep`: a failed build can still let the commit run.

## Layout

```
core/             pure Kotlin/JVM, no Android dependencies, domain rules, JUnit tests
app/              Android application (namespace app.sortie)
  src/main/kotlin/app/sortie/   App, MainActivity, ui/theme
```

```
core/.../core/   PathTemplate, Vars, GoLayout, Tags, Profile(+Yaml, Store), Destination, Copier, Scanner, Store,
                 Sorter (application state: files, view, tagging, checks, copy orchestration)
app/.../data/    Room (Db, RoomStore), Prefs, Storage (permission, volumes, `~` paths)
app/.../media/   Metadata (fields for variables), Pictures and Thumbnails
app/.../copy/    CopyController and the foreground CopyService
app/.../ui/      MainViewModel (UiState, every action) and the Compose screens; navigation is `UiState.screen`
                 plus `BackHandler`, not navigation-compose
```

Keep domain logic in `core` and out of the UI. All randomness and time go through injected
dependencies so tests are deterministic.

## Conventions
- **Database:** Room is set up in the build (`app/schemas/` holds the exported schema). Any schema change needs a
  version bump and a real `Migration`; never use destructive migration. Commit the exported schema.
- **Profiles:** the YAML format is shared with the desktop app (`version: 1`, same keys and validation). Do not
  add keys the desktop app rejects.
- **Copies:** only through `CopyEngine` (temp file, fsync, SHA-256 read-back, no-clobber move). Never write to or
  delete a source file.
- **UI text** lives in string resources, English in `values` and Spanish in `values-es`; every string exists in
  both (lint fails on a missing translation). The app name comes from `R.string.app_name`, never hard-coded.
- **Accessibility:** touch targets at least 48dp, `contentDescription` on icon-only controls, and never colour as
  the only cue.

## Gotchas

- Only `material-icons-core` is available; other icons are vector drawables in `res/drawable`.
- Lint rejects `LocalContext.current.getString(...)` in composables: resolve strings with `stringResource`.
- `HOME` must be set in the container for Robolectric (see `docker-compose.yml`); tests use `sdk=35`.
- AGP 9 has built-in Kotlin: do not apply `org.jetbrains.kotlin.android`. `compileSdk` and `targetSdk` are 37;
  `minSdk` is 26.
- The launcher icon folder must stay `mipmap-anydpi-v26`. Lint suggests `mipmap-anydpi`, but that breaks the build.
- Paint the background behind the system bars (the root `Surface` fills the window; only content is inset),
  otherwise the light theme shows dark bars.
- The debug and release builds use different signatures: uninstall one before installing the other.
- A released keystore cannot be regenerated. Never delete or overwrite `release.jks`.

## App parameters

`APP_NAME`, `APP_ID`, `APP_URL`, `APP_VERSION_NAME`, `APP_VERSION_CODE` come from, in order: a `-P` Gradle property,
an environment variable, the tracked `app.properties` (holds `APP_NAME`, `APP_ID`, `APP_URL` only; the versions default to the root
`VERSION`, and the release workflow overrides them), then the default in `app/build.gradle.kts`. Signing uses `SIGNING_KEYSTORE`,
`SIGNING_STORE_PASSWORD`, `SIGNING_KEY_ALIAS`, `SIGNING_KEY_PASSWORD` or `keystore.properties`.
