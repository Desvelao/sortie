# Contributing

1. Build and test with Docker only: `make image`, then `make check`. No local Go, Java or Android SDK is needed.
2. Keep both apps in step. A user-visible change goes into `desktop/` and `android/` together, with tests in both,
   and a line under `[Unreleased]` in each app's `CHANGELOG.md`. If a platform cannot support it, list it under
   "Known differences" in that app's README.
3. Profile format changes touch `docs/PROFILES.md`, the Go parser, the Kotlin parser and their tests together.
4. Releasing: bump `VERSION`, move `[Unreleased]` to `## [x.y.z]` in both changelogs, run `make check` and
   `make check-version`, then tag `vX.Y.Z`.
5. Never commit keystores, `.env` files or other secrets.

More detail for automated agents: [AGENTS.md](AGENTS.md).
