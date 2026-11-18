# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the project uses
[Semantic Versioning](https://semver.org/). Add every user-visible change under `Unreleased`; the release
workflow publishes the matching section as the GitHub Release notes.

## [Unreleased]

### Fixed
- A destination is no longer shown as ready when the destination folder is missing or is not a folder; the check now gives the same message the copy would.
- The Copy button for the current file is disabled while none of its destinations can be copied: the destination folder does not exist, the path is invalid, or a variable of the template has no value.

## [0.1.0] - 2026-11-18

### Added
- First version of the app, with the features of the desktop Sortie.
- Sort the files of a folder one by one: swipe or use the keyboard, tap tags, see each tag's destination and
  whether it is ready, already copied or in conflict.
- Profiles as desktop-compatible YAML: switch, create, duplicate, rename, delete, import and export; edit them in
  forms (general, tags, variables) or as raw YAML with line-numbered errors.
- Destination templates with variables from the file name, path, metadata, modification time, constants or a
  value typed by hand.
- Filters for untagged, tagged, unresolved, ready, already copied, conflicting and failed files.
- Write-safe copying (verified with SHA-256, never overwrites unless asked) with skip, rename and replace
  policies, progress, cancellation and a foreground notification.
- Copy history per file, interrupted copies and leftover temporary files reported after a crash.
- Previews for images, text, PDF, audio, video and other files; thumbnail strip with a size-limited cache.
- Folder browser over real storage paths, including SD cards and USB drives.
- English and Spanish.
- Desktop and Android release together: one tag publishes one GitHub Release with the signed APK/AAB, the desktop tarball and checksums.
