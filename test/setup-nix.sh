#!/usr/bin/env bash
# Home Manager install: builds a throwaway Home Manager configuration that
# enables programs.shelley-herdr from the snapshot of this working tree, then runs
# its real activation script, exactly as `home-manager switch` would.
set -eu
export NIX_CONFIG='experimental-features = nix-command flakes
sandbox = false
warn-dirty = false'
git config --global --add safe.directory '*'
[ -d /tmp/repo ] || cp -a /repo /tmp/repo

mkdir -p /tmp/hm
cat >/tmp/hm/flake.nix <<'NIX'
{
  inputs.shelley-herdr.url = "git+file:///tmp/repo";
  inputs.nixpkgs.follows = "shelley-herdr/nixpkgs";
  inputs.home-manager.url = "github:nix-community/home-manager";
  inputs.home-manager.inputs.nixpkgs.follows = "nixpkgs";
  outputs = {nixpkgs, home-manager, shelley-herdr, ...}: {
    homeConfigurations.tester = home-manager.lib.homeManagerConfiguration {
      pkgs = nixpkgs.legacyPackages.x86_64-linux;
      modules = [
        shelley-herdr.homeManagerModules.default
        {
          home = {username = "tester"; homeDirectory = "/home/tester"; stateVersion = "24.11";};
          programs.shelley-herdr.enable = true;
          # On a real machine herdr is in the same profile (the module looks for
          # it there); the test mounts the release binary, so expose it the same way.
          home.packages = [(nixpkgs.legacyPackages.x86_64-linux.writeShellScriptBin "herdr" ''exec /usr/local/bin/herdr "$@"'')];
          # Man pages are irrelevant to what is being tested.
          programs.man.enable = false;
          manual.manpages.enable = false;
        }
      ];
    };
  };
}
NIX

# A real machine has this once nix has been used by the user; Home Manager
# refuses to activate without it.
mkdir -p /home/tester/.local/state/nix/profiles

gen=$(nix build "path:/tmp/hm#homeConfigurations.tester.activationPackage" --no-link --print-out-paths)
# Activation must find herdr, as it would on a real machine.
USER=tester HOME=/home/tester "$gen/activate"
