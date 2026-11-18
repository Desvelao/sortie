# Release process and support matrix

Everything runs in Docker; the host only needs the `docker` CLI.

## Build a release
    echo <new-version> > ../VERSION   # (repo root, shared with the Android app) single source of the version number (e.g. 0.2.0); bump it first
    make image                # once, or after Dockerfile.dev changes
    make release              # → release/sortie-<version>-linux-amd64.tar.gz (+ .sha256)

`scripts/release.sh` fails the release unless all of these hold:
1. gofmt clean, `go vet` clean, all tests pass (race detector on every logic package; the UI package runs without it because Fyne's *test* driver executes `fyne.Do` callbacks on background goroutines, which the detector reports as races that cannot occur with the real driver);
2. the binary is **reproducible**: a second build with an empty build cache is byte-identical (`-trimpath`, `-buildvcs=false`, fixed ldflags);
3. it is a stripped x86-64 ELF whose linked libraries are exactly `libGL.so.1 libwayland-client.so.0 libm.so.6 libc.so.6` (anything new must be added deliberately here and in the docs), needs no glibc symbol newer than **2.34**, and contains no build-machine paths;
4. `--version` matches `VERSION`; the headless `--self-test` (bundled SQLite, scan, tag, copy, restart, previews, thumbnails) passes; the GUI starts and exits under Xvfb and creates its data under `$HOME`;
5. every compiled-in Go module has a license file and none is copyleft (headers of the license files are checked; the combined texts are shipped as `THIRD_PARTY_LICENSES.txt`);
6. the archive is deterministic (sorted names, fixed owner/mtime, `gzip -n`), with `SHA256SUMS` inside and a `.sha256` next to it.

Archive contents: `sortie`, `README.txt`, `LICENSE`, `THIRD_PARTY_LICENSES.txt`, `install.sh`, `uninstall.sh`, `SHA256SUMS`, `share/sortie.{desktop,svg}`.

## Continuous integration
`.github/workflows/ci.yml` runs `make check` and `make test-race` on every push and pull request, for both apps. On `v*` tags `.github/workflows/release.yml` runs the full `make release` and attaches the archive and its `.sha256` to the single GitHub Release shared with the Android app. `make test-race` (`scripts/test_race.sh`) is the race-detector run `release.sh` also uses; `make check` itself runs plain `go test`. The clean-machine matrix (`make verify-release`) is not part of CI.

## Verify on clean machines
    make verify-release              # all distributions
    ./scripts/verify_release.sh fedora   # only names containing "fedora"
For each distribution a fresh container gets **only** the documented runtime packages (plus Xvfb and Mesa software rendering, which a real desktop provides). It then unpacks the *archive*, verifies checksums, resolves all libraries, runs `--version`, `--self-test` and the GUI start-up test as a fresh user home, checks that data appears under `$HOME` and that the program folder is untouched, and tests `install.sh`/`uninstall.sh` (user data must survive). Logs go to `verify-logs/`.

## Verified distributions (0.1.0, 2026-11-18)
Re-run `make verify-release` for every release and update this table (version, date, results).

| Distribution | glibc | Result |
|---|---|---|
| Debian 12 (full documented package set) | 2.36 | pass |
| Debian 12, **minimal** (only the libraries linked at start-up) | 2.36 | pass |
| Ubuntu 22.04 | 2.35 | pass |
| Ubuntu 24.04 | 2.39 | pass |
| Fedora 41 | 2.40 | pass |
| Arch Linux (rolling) | 2.44 | pass |
| openSUSE Tumbleweed (rolling) | 2.44 | pass |

Not supported: glibc < 2.34 (Debian 11, Ubuntu 20.04) and musl systems (Alpine). Not tested: real GPUs/Wayland compositors, real audio output, aarch64, non-root users inside the containers, rolling distributions after the test date.

## Runtime packages
Hard requirements (linked at start-up): `libGL.so.1`, `libwayland-client.so.0`, `libX11.so.6` (via libGL), glibc. Recommended (loaded on demand by GLFW): Xcursor, Xrandr, Xinerama, Xi, Xxf86vm, xkbcommon, wayland-cursor, wayland-egl.

| Distribution | Packages |
|---|---|
| Debian / Ubuntu | `libgl1 libx11-6 libwayland-client0 libwayland-cursor0 libwayland-egl1 libxkbcommon0 libxcursor1 libxrandr2 libxinerama1 libxi6 libxxf86vm1` |
| Fedora | `mesa-libGL libX11 libwayland-client libwayland-cursor libwayland-egl libxkbcommon libXcursor libXrandr libXinerama libXi libXxf86vm` |
| Arch | `mesa libx11 wayland libxkbcommon libxcursor libxrandr libxinerama libxi libxxf86vm` |
| openSUSE | `Mesa-libGL1 libX11-6 libwayland-client0 libwayland-cursor0 libwayland-egl1 libxkbcommon0 libXcursor1 libXrandr2 libXinerama1 libXi6 libXxf86vm1` |

## Adding a distribution
Add a row to `MATRIX` in `scripts/verify_release.sh` (image + install command), run it, then add the result to the table above.

## Not done / future
AppImage, `.deb` and `.rpm` packages (the archive plus `install.sh` is the supported route), signed releases, aarch64 builds.
