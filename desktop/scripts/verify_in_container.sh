#!/bin/sh
# Runs INSIDE a clean distribution container (see verify_release.sh). Only the documented
# runtime libraries (+ Xvfb for the GUI test) are installed; the packaged release archive is
# what gets tested.
set -eu
fail() { echo "FAIL  $*"; exit 1; }
ok()   { echo "ok    $*"; }

. /etc/os-release
echo "--- ${PRETTY_NAME}, glibc $(ldd --version | head -n1 | sed 's/.* //')"

if command -v go >/dev/null 2>&1; then fail "Go is installed: not a clean environment"; fi
if command -v sqlite3 >/dev/null 2>&1; then
  echo "note  the sqlite3 tool is part of this base image; the program does not use it (checked below)"
else
  ok "no Go and no sqlite3 on this machine"
fi
if command -v python3 >/dev/null 2>&1; then echo "note  python3 is part of this base image (the program does not use it)"; fi

# the one archive under test (release/ may hold older versions too)
: "${VERIFY_NAME:?VERIFY_NAME (archive base name) must be set by verify_release.sh}"
cd /release
sha256sum -c "$VERIFY_NAME.tar.gz.sha256" >/dev/null || fail "archive checksum"
ok "archive checksum"
rm -rf /opt/fs; mkdir -p /opt/fs && tar -xzf "/release/$VERIFY_NAME.tar.gz" -C /opt/fs
D=/opt/fs/$VERIFY_NAME
test -d "$D" || fail "archive does not contain $VERIFY_NAME/"
( cd "$D" && sha256sum -c SHA256SUMS >/dev/null ) || fail "file checksums inside the archive"
ok "extracted; file checksums match"

if ldd "$D/sortie" | grep -q "not found"; then ldd "$D/sortie"; fail "missing shared libraries"; fi
ok "all linked libraries resolve"
if ldd "$D/sortie" | grep -qi sqlite; then fail "binary links a system SQLite library"; fi
ok "no system SQLite library is used (database engine is built in)"
"$D/sortie" --version | grep -q '^sortie ' || fail "--version"
ok "$("$D/sortie" --version)"

export HOME=/tmp/fs-home
rm -rf "$HOME"; mkdir -p "$HOME"
unset XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME XDG_CACHE_HOME
before=$(cd "$D" && ls -lR)

out=$("$D/sortie" --self-test) || { echo "$out"; fail "self-test"; }
echo "$out" | grep -q 'SELF-TEST OK' || { echo "$out"; fail "self-test"; }
ok "headless self-test (bundled SQLite, scan, tag, copy, previews)"

if [ "${SKIP_GUI:-0}" != 1 ]; then
  LIBGL_ALWAYS_SOFTWARE=1 xvfb-run -a "$D/sortie" --smoke-test >/tmp/gui.log 2>&1 || { tail -n 20 /tmp/gui.log; fail "GUI did not start under Xvfb"; }
  ok "GUI starts and exits cleanly (Xvfb + Mesa software rendering)"
  test -f "$HOME/.local/share/sortie/database.sqlite" || fail "database not created under \$HOME"
  test -f "$HOME/.local/state/sortie/sortie.log"  || fail "log not created under \$HOME"
  ok "data created under \$HOME, not in the program folder"
fi
after=$(cd "$D" && ls -lR)
[ "$before" = "$after" ] || fail "program folder was modified while running"
ok "program folder untouched"

"$D/install.sh" --prefix /tmp/prefix >/dev/null
test -x /tmp/prefix/bin/sortie && grep -q '^Exec=/tmp/prefix/bin/sortie$' /tmp/prefix/share/applications/sortie.desktop || fail "install.sh"
/tmp/prefix/bin/sortie --version >/dev/null || fail "installed binary does not run"
"$D/uninstall.sh" --prefix /tmp/prefix >/dev/null
test ! -e /tmp/prefix/bin/sortie || fail "uninstall.sh left the binary"
if [ "${SKIP_GUI:-0}" != 1 ]; then test -d "$HOME/.local/share/sortie" || fail "uninstall removed user data"; fi
ok "install.sh / uninstall.sh (user data kept)"
# --- the same user-facing steps as an unprivileged user: the program folder is root-owned and
# read-only for them, and the default per-user install into ~/.local must work.
if command -v useradd >/dev/null 2>&1 && command -v su >/dev/null 2>&1; then
  useradd -m -s /bin/sh tester 2>/dev/null || fail "cannot create the unprivileged test user"
  cat > /tmp/nonroot.sh <<'EOS'
set -eu
fail() { echo "FAIL  (unprivileged) $*"; exit 1; }
[ "$(id -u)" != 0 ] || fail "still root"
export HOME=/home/tester
unset XDG_CONFIG_HOME XDG_DATA_HOME XDG_STATE_HOME XDG_CACHE_HOME
if ( : > "$D/.write-test" ) 2>/dev/null; then rm -f "$D/.write-test"; fail "program folder is writable for a normal user"; fi
"$D/sortie" --version | grep -q '^sortie ' || fail "--version"
out=$("$D/sortie" --self-test) || { echo "$out"; fail "self-test"; }
echo "$out" | grep -q 'SELF-TEST OK' || { echo "$out"; fail "self-test"; }
if [ "${SKIP_GUI:-0}" != 1 ]; then
  LIBGL_ALWAYS_SOFTWARE=1 xvfb-run -a "$D/sortie" --smoke-test >/tmp/gui-nonroot.log 2>&1 || { tail -n 20 /tmp/gui-nonroot.log; fail "GUI did not start"; }
  test -f "$HOME/.local/share/sortie/database.sqlite" || fail "database not created under \$HOME"
fi
"$D/install.sh" >/dev/null || fail "install.sh (default user prefix)"
test -x "$HOME/.local/bin/sortie" || fail "binary not installed into ~/.local/bin"
grep -q '^Exec=/home/tester/.local/bin/sortie$' "$HOME/.local/share/applications/sortie.desktop" || fail "desktop entry Exec="
"$HOME/.local/bin/sortie" --version >/dev/null || fail "installed binary does not run"
"$D/uninstall.sh" >/dev/null
test ! -e "$HOME/.local/bin/sortie" || fail "uninstall.sh left the binary"
test -d "$HOME/.local/share/sortie" || { [ "${SKIP_GUI:-0}" = 1 ] || fail "uninstall removed user data"; }
EOS
  su -s /bin/sh tester -c "SKIP_GUI=${SKIP_GUI:-0} D='$D' sh /tmp/nonroot.sh" || fail "run as an unprivileged user"
  ok "unprivileged user: read-only program folder, self-test, GUI, default ~/.local install/uninstall"
else
  echo "note  useradd/su not available here: the unprivileged-user run was skipped"
fi
echo "VERIFIED ${PRETTY_NAME}"
