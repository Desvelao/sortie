#!/usr/bin/env bash
# Builds, validates and packages a release. Run inside the dev container (make release).
# Fails on: formatting/vet/test failures, non-reproducible builds, unexpected shared
# libraries, a glibc requirement above the documented minimum, a failing self-test or
# GUI start-up test, or a dependency without a (permissive) license.
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION=$(tr -d '[:space:]' < ../VERSION)
NAME="sortie-$VERSION-linux-amd64"
OUT="release/$NAME"
MAX_GLIBC="2.34"
ALLOWED_NEEDED="libwayland-client.so.0 libGL.so.1 libm.so.6 libc.so.6"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

step() { printf '\n==> %s\n' "$*"; }
die()  { printf 'RELEASE FAILED: %s\n' "$*" >&2; exit 1; }
ldflags="-s -w -X main.version=$VERSION"
build() { CGO_ENABLED=1 go build -trimpath -buildvcs=false -ldflags="$ldflags" -o "$1" ./cmd/sortie; }

step "format, vet, tests (race detector on all logic packages)"
test -z "$(gofmt -l .)" || { gofmt -l .; die "gofmt issues"; }
go vet ./...
./scripts/test_race.sh

step "build $NAME"
build "$WORK/sortie"
if [ "${REPRO_CHECK:-1}" = 1 ]; then
  step "reproducibility: rebuild with an empty build cache and compare"
  GOCACHE="$WORK/gocache" build "$WORK/sortie.2"
  cmp "$WORK/sortie" "$WORK/sortie.2" || die "two builds differ (not reproducible)"
  echo "identical: $(sha256sum "$WORK/sortie" | cut -d' ' -f1)"
fi
BIN="$WORK/sortie"

step "validate binary"
file "$BIN" | grep -q "ELF 64-bit LSB.*x86-64" || die "not an x86-64 ELF binary"
file "$BIN" | grep -q "stripped" || die "binary is not stripped"
if ldd "$BIN" | grep -q "not found"; then ldd "$BIN"; die "unresolved shared libraries on the build machine"; fi
needed=$(readelf -d "$BIN" | sed -n 's/.*Shared library: \[\(.*\)\]/\1/p' | sort | tr '\n' ' ')
expected=$(echo $ALLOWED_NEEDED | tr ' ' '\n' | sort | tr '\n' ' ')
[ "$needed" = "$expected" ] || die "linked libraries changed: [$needed] expected [$expected] — update docs and ALLOWED_NEEDED deliberately"
echo "linked libraries: $needed"
maxg=$(objdump -T "$BIN" | grep -o 'GLIBC_[0-9.]*' | sed 's/GLIBC_//' | sort -uV | tail -1)
[ "$(printf '%s\n%s\n' "$maxg" "$MAX_GLIBC" | sort -V | tail -1)" = "$MAX_GLIBC" ] || die "needs glibc $maxg, documented minimum is $MAX_GLIBC"
echo "highest glibc symbol version: $maxg (documented minimum: $MAX_GLIBC)"
if strings -a "$BIN" | grep -qE '/home/|/go/pkg/mod|/repo/|/src/internal'; then die "binary contains build-machine paths"; fi
[ "$("$BIN" --version)" = "sortie $VERSION" ] || die "--version output wrong: $("$BIN" --version)"
echo "size: $(du -h "$BIN" | cut -f1)"

step "headless self-test"
"$BIN" --self-test | tail -n 4
"$BIN" --self-test >/dev/null || die "self-test failed"

step "GUI start-up test under Xvfb"
# Capture the exit status separately from the (filtered) output: piping into grep would hide a
# crash or non-zero exit of the GUI.
smoke_rc=0
smoke_out=$(HOME="$WORK/home" xvfb-run -a "$BIN" --smoke-test 2>&1) || smoke_rc=$?
printf '%s\n' "$smoke_out" | grep -v -E 'Fyne error|Cause:|At:|XDG_RUNTIME_DIR' || true
[ "$smoke_rc" = 0 ] || die "GUI smoke test exited with status $smoke_rc"
[ -f "$WORK/home/.local/share/sortie/database.sqlite" ] || die "GUI start did not create the database under \$HOME"

step "third-party licenses"
go list -deps -f '{{if not .Standard}}{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}} {{.Dir}}{{end}}{{end}}{{end}}' ./cmd/sortie | sort -u > "$WORK/mods"
LIC="$WORK/THIRD_PARTY_LICENSES.txt"
{
  echo "Sortie $VERSION bundles the following third-party software."
  echo "The Go standard library and runtime are covered by the Go license below."
  echo
} > "$LIC"
bad=0
while read -r path ver dir; do
  files=$(find "$dir" -maxdepth 1 -type f \( -iname 'LICEN[CS]E*' -o -iname 'COPYING*' -o -iname 'UNLICENSE*' \) | sort)
  if [ -z "$files" ]; then echo "  MISSING license file: $path"; bad=1; continue; fi
  # classify by the header of the real license files only: aggregate notices (*.md) quote other
  # licenses' wording (e.g. MPL's "Secondary License" definition) without being licensed under them
  head_text=$(for f in $files; do case "$f" in *.md) ;; *) head -n 20 "$f" ;; esac; done)
  kind=permissive
  if printf '%s' "$head_text" | grep -qiE 'GNU (Affero |Lesser )?General Public License|Mozilla Public License|Eclipse Public License|Common Development and Distribution'; then kind=COPYLEFT; bad=1; fi
  printf '  %-45s %-32s %s\n' "$path" "$ver" "$kind"
  { echo "================================================================"; echo "$path $ver"; echo "================================================================"; cat $files; echo; } >> "$LIC"
done < "$WORK/mods"
{ echo "================================================================"; echo "Go standard library and runtime"; echo "================================================================"; cat "$(go env GOROOT)/LICENSE"; } >> "$LIC"
[ $bad = 0 ] || die "a dependency has no license file or a copyleft license; review before releasing"
echo "$(wc -l < "$WORK/mods") modules, all with permissive license files"

step "package"
rm -rf "$OUT" "release/$NAME.tar.gz" "release/$NAME.tar.gz.sha256"
mkdir -p "$OUT/share" "$OUT/docs/profiles"
install -m 755 "$BIN" "$OUT/sortie"
sed "s/@VERSION@/$VERSION/g" packaging/README.txt.in > "$OUT/README.txt"
cp ../LICENSE "$OUT/LICENSE"
cp "$LIC" "$OUT/THIRD_PARTY_LICENSES.txt"
install -m 755 packaging/install.sh packaging/uninstall.sh "$OUT/"
install -m 644 packaging/sortie.desktop packaging/sortie.svg "$OUT/share/"
install -m 644 ../docs/PROFILES.md "$OUT/docs/"
install -m 644 ../docs/profiles/*.yaml "$OUT/docs/profiles/"
( cd "$OUT" && find . -type f ! -name SHA256SUMS | sort | sed 's|^\./||' | xargs sha256sum > SHA256SUMS )
# deterministic archive: sorted names, fixed owner and time, no gzip timestamp
tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' -C release -cf - "$NAME" | gzip -9n > "release/$NAME.tar.gz"
( cd release && sha256sum "$NAME.tar.gz" > "$NAME.tar.gz.sha256" )
# keep the plain binary used by `make build` in sync
cp "$BIN" release/sortie

step "result"
ls -l "release/$NAME.tar.gz"
cat "release/$NAME.tar.gz.sha256"
echo "RELEASE OK: $NAME"
