#!/bin/sh
# Installs Sortie. Default: for the current user (~/.local). Needs no root.
#   ./install.sh                 user install into ~/.local
#   sudo ./install.sh --system   system-wide install into /usr/local
#   ./install.sh --prefix DIR    install into DIR (bin/, share/)
# Your configuration and data are never touched.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
prefix="${HOME:-/tmp}/.local"
while [ $# -gt 0 ]; do
  case "$1" in
    --system) prefix=/usr/local ;;
    --prefix) shift; prefix="${1:?--prefix needs a directory}" ;;
    -h|--help) sed -n '2,7p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done
[ -x "$here/sortie" ] || { echo "sortie binary not found next to install.sh" >&2; exit 1; }
install -d "$prefix/bin" "$prefix/share/applications" "$prefix/share/icons/hicolor/scalable/apps"
install -m 755 "$here/sortie" "$prefix/bin/sortie"
install -m 644 "$here/share/sortie.svg" "$prefix/share/icons/hicolor/scalable/apps/sortie.svg"
# Exec= needs quoting when the path has spaces or shell-reserved characters (Desktop Entry spec):
# inside double quotes ` " $ \ get a backslash, and the key-value syntax doubles every backslash.
exec_path="$prefix/bin/sortie"
case "$exec_path" in
  *[!A-Za-z0-9_./:@%+=,-]*)
    exec_path=$(printf '%s' "$exec_path" | sed -e 's/\\/\\\\\\\\/g' -e 's/"/\\\\"/g' -e 's/`/\\\\`/g' -e 's/\$/\\\\$/g')
    exec_path="\"$exec_path\"" ;;
esac
# substitute in plain shell (no sed/awk: the value may contain | & \ that are special there, and
# minimal systems do not always ship awk); only the first @EXEC@ of a line is replaced
while IFS= read -r line || [ -n "$line" ]; do
  case "$line" in
    *@EXEC@*) printf '%s\n' "${line%%@EXEC@*}$exec_path${line#*@EXEC@}" ;;
    *) printf '%s\n' "$line" ;;
  esac
done < "$here/share/sortie.desktop" > "$prefix/share/applications/sortie.desktop"
chmod 644 "$prefix/share/applications/sortie.desktop"
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$prefix/share/applications" >/dev/null 2>&1 || true
command -v gtk-update-icon-cache >/dev/null 2>&1 && [ -f "$prefix/share/icons/hicolor/index.theme" ] && gtk-update-icon-cache -q -t -f "$prefix/share/icons/hicolor" >/dev/null 2>&1 || true
echo "Installed to $prefix (binary: $prefix/bin/sortie)."
case ":$PATH:" in *":$prefix/bin:"*) ;; *) echo "Note: $prefix/bin is not in your PATH." ;; esac
