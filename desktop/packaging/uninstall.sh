#!/bin/sh
# Removes what install.sh installed (same options). Your configuration, database,
# logs and caches are NOT removed; the paths are printed so you can delete them yourself.
set -eu
prefix="${HOME:-/tmp}/.local"
while [ $# -gt 0 ]; do
  case "$1" in
    --system) prefix=/usr/local ;;
    --prefix) shift; prefix="${1:?--prefix needs a directory}" ;;
    -h|--help) sed -n '2,4p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done
rm -f "$prefix/bin/sortie" \
      "$prefix/share/applications/sortie.desktop" \
      "$prefix/share/icons/hicolor/scalable/apps/sortie.svg"
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$prefix/share/applications" >/dev/null 2>&1 || true
command -v gtk-update-icon-cache >/dev/null 2>&1 && [ -f "$prefix/share/icons/hicolor/index.theme" ] && gtk-update-icon-cache -q -t -f "$prefix/share/icons/hicolor" >/dev/null 2>&1 || true
echo "Removed Sortie from $prefix."
echo "Left in place (delete manually if you want them gone):"
echo "  ${XDG_CONFIG_HOME:-$HOME/.config}/sortie"
echo "  ${XDG_DATA_HOME:-$HOME/.local/share}/sortie"
echo "  ${XDG_STATE_HOME:-$HOME/.local/state}/sortie"
echo "  ${XDG_CACHE_HOME:-$HOME/.cache}/sortie"
