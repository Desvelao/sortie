# Changelog

All notable changes to the desktop app are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/). The version is shared with the Android app (`../VERSION`): a user-visible
change that also applies to Android must be recorded in `../android/CHANGELOG.md` too.

## [Unreleased]

### Fixed
- A destination is no longer shown as ready when the destination folder is missing or is not a folder; the check now gives the same message the copy would.
- The Copy button for the current file is disabled while none of its destinations can be copied: the destination folder does not exist, the path is invalid, or a variable of the template has no value.

## [0.1.0] - 2026-11-18

### Added
- Keyboard-driven sorting of the files of a folder with tags, copying them into folders built from templates.
- Profiles as YAML files: variables extracted from the file name, folder, metadata or modification time, manual
  values, per-tag destination templates, conflict policies.
- Write-safe copying (temp file, fsync, SHA-256 read-back, atomic no-clobber rename) with resumable batches.
- Previews for images, text, PDF, audio and video; thumbnail filmstrip with a size-limited cache.
- Validated release tarball for Linux amd64 (see `docs/RELEASE.md`).
- Desktop and Android release together: one tag publishes one GitHub Release with the tarball, the signed APK/AAB and checksums.
