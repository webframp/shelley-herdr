#!/usr/bin/env bash
# Non-Nix install: what a user does with `herdr plugin install`. herdr clones
# https://github.com/webframp/shelley-herdr.git; a git rewrite points that at the
# snapshot of this working tree mounted at /repo, so uncommitted work is tested.
set -eu
git config --global --add safe.directory '*'
git config --global url."file:///repo".insteadOf "https://github.com/webframp/shelley-herdr.git"
herdr plugin install webframp/shelley-herdr --yes
