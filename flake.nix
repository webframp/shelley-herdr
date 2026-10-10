{
  description = "Shelley (exe.dev) as a native agent in herdr";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = {
    self,
    nixpkgs,
  }: let
    systems = ["x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin"];
    forEachSystem = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
  in {
    # Consume like any other flake input:
    #   inputs.shelley-herdr.url = "github:webframp/shelley-herdr";
    #   home.packages = [inputs.shelley-herdr.packages.${system}.default];
    packages = forEachSystem (pkgs: {
      default = pkgs.buildGoModule {
        pname = "shelley-herdr";
        version = "0.2.0";
        src = self;
        vendorHash = null; # stdlib only
        # Only the bridge: test/fakes also contain main packages, and building
        # them would install fake ssh and shelley binaries over the real ones.
        subPackages = ["."];

        postInstall = ''
          # herdr plugin manifest; its pane runs `shelley-herdr` from PATH.
          mkdir -p $out/share/herdr/plugins/shelley
          cp herdr-plugin.toml $out/share/herdr/plugins/shelley/
        '';

        meta = {
          description = "Run exe.dev Shelley conversations as native herdr agents";
          mainProgram = "shelley-herdr";
          license = pkgs.lib.licenses.asl20;
        };
      };
    });

    overlays.default = final: _prev: {
      shelley-herdr = self.packages.${final.stdenv.hostPlatform.system}.default;
    };

    # Home Manager: installs the package and registers the herdr plugin on every
    # switch, so the link never goes stale after an upgrade.
    #   imports = [inputs.shelley-herdr.homeManagerModules.default];
    #   programs.shelley-herdr.enable = true;
    homeManagerModules.default = {
      config,
      lib,
      pkgs,
      ...
    }: let
      cfg = config.programs.shelley-herdr;
    in {
      options.programs.shelley-herdr = {
        enable = lib.mkEnableOption "Shelley (exe.dev) as a native herdr agent";
        package = lib.mkOption {
          type = lib.types.package;
          default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
          defaultText = lib.literalExpression "shelley-herdr.packages.\${system}.default";
          description = "The shelley-herdr package, which also carries the herdr plugin.";
        };
      };

      config = lib.mkIf cfg.enable {
        home.packages = [cfg.package];

        # `herdr plugin link` edits the plugin registry directly, needs no running
        # server, and re-linking the same plugin id is idempotent.
        home.activation.shelleyHerdrPlugin = lib.hm.dag.entryAfter ["writeBoundary" "installPackages"] ''
          herdr=$(PATH="${config.home.profileDirectory}/bin:$PATH" command -v herdr || true)
          if [ -n "$herdr" ]; then
            $DRY_RUN_CMD "$herdr" plugin link "${cfg.package}/share/herdr/plugins/shelley" >/dev/null \
              || echo "shelley-herdr: could not link the herdr plugin; run 'herdr plugin link ${cfg.package}/share/herdr/plugins/shelley'" >&2
          else
            echo "shelley-herdr: herdr not found; install herdr, then run 'herdr plugin link ${cfg.package}/share/herdr/plugins/shelley'" >&2
          fi
        '';
      };
    };

    devShells = forEachSystem (pkgs: {
      default = pkgs.mkShell {packages = [pkgs.go pkgs.gopls];};
    });

    formatter = forEachSystem (pkgs: pkgs.alejandra);
  };
}
