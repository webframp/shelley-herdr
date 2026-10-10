#!/usr/bin/env bash
# Run the container tests for both install methods.
#
#   test/run.sh            both methods
#   test/run.sh github     `herdr plugin install` (no Nix)
#   test/run.sh nix        Home Manager module
#   test/run.sh clean      remove the cached Nix store volume
#
# Needs podman or docker and network access. No live VM is ever contacted:
# `ssh` and `shelley` are replaced by the fakes in test/fakes. The working tree
# is snapshotted first, so uncommitted changes are what gets tested.
set -euo pipefail

HERDR_VERSION=${HERDR_VERSION:-v0.9.3}
HERDR_SHA256=${HERDR_SHA256:-18a8dc65f1c2fa485884344356dea1cfd911c6f06cf46fa78e193f4087f4dba7}   # linux-x86_64 for v0.9.3
GO_IMAGE=${GO_IMAGE:-docker.io/library/golang:1.22-bookworm}   # also proves Go 1.22 builds it
NIX_IMAGE=${NIX_IMAGE:-docker.io/nixos/nix:latest}
NIX_VOLUME=shelley-herdr-test-nix

here=$(cd "$(dirname "$0")" && pwd)
repo=$(dirname "$here")
rt=$(command -v podman || command -v docker || { echo "need podman or docker" >&2; exit 2; })
want=${1:-all}

if [ "$want" = clean ]; then $rt volume rm -f "$NIX_VOLUME" >/dev/null; echo "removed $NIX_VOLUME"; exit 0; fi
case "$want" in all|github|nix) ;; *) echo "usage: $0 [all|github|nix|clean]" >&2; exit 2 ;; esac

cache=${XDG_CACHE_HOME:-$HOME/.cache}/shelley-herdr-test
mkdir -p "$cache"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# herdr: a static release binary, pinned by checksum.
herdr="$cache/herdr-$HERDR_VERSION-linux-x86_64"
if [ ! -x "$herdr" ]; then
  echo "# downloading herdr $HERDR_VERSION"
  curl -fsSL -o "$herdr.tmp" "https://github.com/herdrdev/herdr/releases/download/$HERDR_VERSION/herdr-linux-x86_64"
  echo "$HERDR_SHA256  $herdr.tmp" | sha256sum -c - >/dev/null || { echo "herdr checksum mismatch" >&2; rm -f "$herdr.tmp"; exit 1; }
  chmod +x "$herdr.tmp"; mv "$herdr.tmp" "$herdr"
fi

# Snapshot of the working tree (tracked + untracked, minus ignored) as a git repo.
mkdir "$work/repo"
(cd "$repo" && git ls-files -z --cached --others --exclude-standard | tar --null --ignore-failed-read -T - -cf -) | tar -C "$work/repo" -xf -
git -C "$work/repo" init -q -b main
git -C "$work/repo" add -A
git -C "$work/repo" -c user.name=test -c user.email=test@example.invalid commit -q -m snapshot

# Fakes, built with the same Go toolchain the github flavour uses.
mkdir "$work/fakes"
$rt run --rm -v "$work/repo:/src:ro" -v "$work/fakes:/out" -w /src -e CGO_ENABLED=0 "$GO_IMAGE" \
  sh -c 'go vet ./... && go build -o /out/ssh ./test/fakes/ssh && go build -o /out/shelley ./test/fakes/shelley'

run_flavor() {
  local flavor=$1 image=$2; shift 2
  $rt pull -q "$image" >/dev/null
  echo "# ===== $flavor ====="
  $rt run --rm "$@" \
    -v "$herdr:/usr/local/bin/herdr:ro" -v "$work/repo:/repo:ro" -v "$work/fakes:/fakes:ro" \
    -e FLAVOR="$flavor" -e HOME=/home/tester "$image" bash -c '
      set -e
      mkdir -p "$HOME"
      export PATH=/fakes:$HOME/.nix-profile/bin:/usr/local/bin:$PATH
      echo "# setup"; /repo/test/setup-$FLAVOR.sh
      echo "# suite"; /repo/test/suite.sh'
}

status=0
if [ "$want" = all ] || [ "$want" = github ]; then run_flavor github "$GO_IMAGE" || status=1; fi
if [ "$want" = all ] || [ "$want" = nix ]; then run_flavor nix "$NIX_IMAGE" -v "$NIX_VOLUME:/nix" || status=1; fi
exit $status
