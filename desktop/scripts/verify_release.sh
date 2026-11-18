#!/usr/bin/env bash
# Clean-environment test of the packaged release on several Linux distributions.
# Only the `docker` CLI is used on the host; everything else happens in throw-away containers
# that have just the documented runtime packages installed.
#   scripts/verify_release.sh            all distributions
#   scripts/verify_release.sh debian12   only those whose name contains the argument
set -uo pipefail
cd "$(dirname "$0")/.."
# Verify exactly the archive of the current VERSION: release/ also keeps older versions.
VERSION=$(tr -d '[:space:]' < ../VERSION)
NAME="sortie-$VERSION-linux-amd64"
test -f "release/$NAME.tar.gz" && test -f "release/$NAME.tar.gz.sha256" || { echo "release/$NAME.tar.gz not found: run 'make release' first" >&2; exit 1; }

DEB_RUNTIME="libgl1 libx11-6 libwayland-client0 libwayland-cursor0 libwayland-egl1 libxkbcommon0 libxcursor1 libxrandr2 libxinerama1 libxi6 libxxf86vm1"
DEB_TEST="libgl1-mesa-dri xvfb xauth"
deb() { echo "apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends $1 >/dev/null"; }

# name | image | install command (documented runtime packages + test-only Xvfb/Mesa) | env
MATRIX=(
  "debian12|debian:12-slim|$(deb "$DEB_RUNTIME $DEB_TEST")|"
  "debian12-minimal|debian:12-slim|$(deb "libgl1 libx11-6 libwayland-client0 $DEB_TEST")|"
  "ubuntu2204|ubuntu:22.04|$(deb "$DEB_RUNTIME $DEB_TEST")|"
  "ubuntu2404|ubuntu:24.04|$(deb "$DEB_RUNTIME $DEB_TEST")|"
  "fedora41|fedora:41|dnf install -y -q mesa-libGL libX11 libwayland-client libwayland-cursor libwayland-egl libxkbcommon libXcursor libXrandr libXinerama libXi libXxf86vm mesa-dri-drivers xorg-x11-server-Xvfb xorg-x11-xauth tar gzip findutils >/dev/null|"
  "arch|archlinux:latest|pacman -Sy --noconfirm --needed mesa libx11 wayland libxkbcommon libxcursor libxrandr libxinerama libxi libxxf86vm xorg-server-xvfb xorg-xauth >/dev/null|"
  "opensuse|opensuse/tumbleweed|zypper -q -n install --no-recommends Mesa-libGL1 libX11-6 libwayland-client0 libwayland-cursor0 libwayland-egl1 libxkbcommon0 libXcursor1 libXrandr2 libXinerama1 libXi6 libXxf86vm1 Mesa-dri xorg-x11-server-Xvfb xvfb-run xauth tar gzip >/dev/null|"
)

mkdir -p verify-logs
declare -a results
status=0
for row in "${MATRIX[@]}"; do
  IFS='|' read -r name image install envs <<<"$row"
  [ -n "${1:-}" ] && [[ "$name" != *"$1"* ]] && continue
  echo "=== $name ($image)"
  log="verify-logs/$name.log"
  if docker run --rm -e "VERIFY_NAME=$NAME" -v "$PWD/release:/release:ro" -v "$PWD/scripts/verify_in_container.sh:/verify.sh:ro" \
       "$image" sh -c "set -e; $install; sh /verify.sh" >"$log" 2>&1; then
    results+=("PASS  $name  $(grep '^---' "$log" | head -n1 | sed 's/^--- //')")
  else
    results+=("FAIL  $name  (see $log)"); status=1
    tail -n 15 "$log"
  fi
done
echo; echo "=== summary"; printf '%s\n' "${results[@]}"
exit $status
