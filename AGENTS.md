# AGENTS.md

Guidance for AI coding agents working in this repository. `README.md` is for humans. App-specific rules are in
[`android/AGENTS.md`](android/AGENTS.md); the Go app's architecture is in
[`desktop/docs/ARCHITECTURE.md`](desktop/docs/ARCHITECTURE.md). Everything here applies to both apps.

## What this is

Sortie, one product on two platforms, kept in one repo:

| | Path | Stack | Ships as |
|---|---|---|---|
| Desktop | `desktop/` | Go + Fyne (Linux) | validated `.tar.gz` |
| Android | `android/` | Kotlin, Jetpack Compose, Material 3 | signed APK/AAB |

Both tag files and **copy** them into folders built from templates (`{year}/{tag}/{filename}`). Sources are never
modified or deleted.

## Parity rules (the point of this repo)

1. **Feature parity.** A user-visible feature, fix or behaviour change is implemented in **both** apps in the same
   change, with tests in both. If a platform genuinely cannot support it, do not skip silently: record it under
   "Known differences" in `android/README.md` (or `desktop/README.md`) and say why in the change description.
2. **Version parity.** `VERSION` at the repo root (`x.y.z`) is the only version number. The desktop build
   (`desktop/scripts/release.sh`) and the Android Gradle build (`android/app/build.gradle.kts`) both read it; never
   hard-code a version elsewhere. A release is one tag `vX.Y.Z` equal to `VERSION`; both apps release together.
   Android's `versionCode` is `major*10000 + minor*100 + patch`.
3. **Changelogs.** Each app has its own `CHANGELOG.md` (Keep a Changelog). Every user-visible change adds a line under
   `[Unreleased]` in the changelog of each app it touches. To release: bump `VERSION`, move `[Unreleased]` entries to a
   `## [x.y.z]` section in **both** changelogs (an app with no change still gets the heading), then tag.
   `make check-version` (`scripts/check-version.sh`) enforces the structure; CI runs it on tags.
4. **Shared profile format.** Profiles are YAML (`version: 1`) shared by both apps and documented in
   `docs/PROFILES.md`; the examples in `docs/profiles/` are tested by the desktop (`internal/profile/examples_test.go`)
   and must stay valid for Android. Changing the format means: update `docs/PROFILES.md`, the Go parser and the
   Kotlin parser (`android/core`, `Profile*`), and their tests, together. Never add a key one app rejects.
   Template syntax (`{var|default}`, `{var:upper|lower|slug|pad4}`) and copy safety (temp file, fsync, SHA-256 read-back,
   no-clobber rename, conflict policies `skip|rename|replace`) behave identically on both.

## Hard constraints

1. **Docker-only toolchain.** Never ask the user to install Go, Java, Gradle, Android Studio or SDK packages on the
   host. Builds, tests and `adb` run through the Makefiles (`docker compose`). CI runs on GitHub's runners and calls
   the same make targets; Android's CI passes `RUN=` to skip the Docker wrapper.
2. **Do not commit secrets**: `*.jks`, `keystore.properties`, `.env*` are git-ignored. Check `git status` before
   committing and never `git add -A` blindly. A released Android keystore cannot be regenerated: never delete it.
3. **Android app name/id/URL live only in `android/app.properties`** (see `android/AGENTS.md`).
4. Native only on Android (no cross-platform frameworks, no WebView). No network access added to either app
   without a discussion: Android has no `INTERNET` permission.
5. `desktop/tasks.csv` and `desktop/tack.yaml` are local, git-ignored tooling files; do not rely on them in docs or CI.

## Commands (run from the repo root)

```bash
make help            # list targets
make image           # build both dev images (once, and after Dockerfile changes)
make check           # version check + desktop checks + android tests and lint  <- definition of done
make desktop-check   # gofmt, vet, tests, build, GUI smoke test
make desktop-run     # run the desktop app (needs a display)
make desktop-release # validated release tarball (desktop/release/)
make android-check   # unit tests and lint
make android-debug   # debug APK
make android-install # needs a device/emulator
make android-release # needs signing (android/README.md)
make check-version   # VERSION + changelog structure only
```
Any target of an app Makefile works as `make desktop-<target>` / `make android-<target>`.

A change is done when `make check` passes. For Android also `make android-check-release`; for desktop releases also
`make desktop-test-race`. Do not chain `git commit` after a check with `&&` through a pipe or `grep`: a failed build can
still let the commit run.

## Layout

```
VERSION  Makefile  README.md  AGENTS.md  LICENSE
docs/            shared: PROFILES.md (format reference), profiles/ (tested examples)
scripts/         check-version.sh
.github/workflows/   ci.yml (checks for both apps), release.yml (builds both on tags, one GitHub Release)
desktop/         Go module, Dockerfile.dev, docker-compose.yml, scripts/, packaging/, docs/, CHANGELOG.md
android/         Gradle project (:app, :core), Dockerfile, docker-compose.yml, app.properties, CHANGELOG.md
```
Both compose files mount the repo root at `/repo` and work in `/repo/<app>`, so `../VERSION` and `../docs` resolve
inside containers. Keep that when editing them.

## Working in the repo

- Change both apps when the task is about behaviour; change one only for platform-specific work (UI toolkit, packaging,
  permissions, signing).
- Keep domain logic out of the UI on both sides (`desktop/internal/*` vs `internal/ui`; `android/core` vs `app/ui`).
- When you change one app's behaviour, look for the matching code in the other first
  (e.g. Go `internal/pathtpl` <-> Kotlin `PathTemplate`, `internal/copier` <-> `Copier`, `internal/profile` <->
  `Profile*`, `internal/vars` <-> `Vars`) and mirror the change and its tests.
