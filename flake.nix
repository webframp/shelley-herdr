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
    #   inputs.shelly-herdr.url = "github:webframp/shelly-herdr";
    #   home.packages = [inputs.shelly-herdr.packages.${system}.default];
    packages = forEachSystem (pkgs: {
      default = pkgs.buildGoModule {
        pname = "shelly-herdr";
        version = "0.0.1";
        src = self;
        vendorHash = null; # stdlib only

        postInstall = ''
          # herdr plugin manifest; its pane runs `shelly-herdr` from PATH.
          mkdir -p $out/share/herdr/plugins/shelly
          cp herdr-plugin.toml $out/share/herdr/plugins/shelly/
        '';

        meta = {
          description = "Run exe.dev Shelley conversations as native herdr agents";
          mainProgram = "shelly-herdr";
          license = pkgs.lib.licenses.asl20;
        };
      };
    });

    overlays.default = final: _prev: {
      shelly-herdr = self.packages.${final.stdenv.hostPlatform.system}.default;
    };

    # Home Manager: installs the package and registers the herdr plugin on every
    # switch, so the link never goes stale after an upgrade.
    #   imports = [inputs.shelly-herdr.homeManagerModules.default];
    #   programs.shelly-herdr.enable = true;
    homeManagerModules.default = {
      config,
      lib,
      pkgs,
      ...
    }: let
      cfg = config.programs.shelly-herdr;
    in {
      options.programs.shelly-herdr = {
        enable = lib.mkEnableOption "Shelley (exe.dev) as a native herdr agent";
        package = lib.mkOption {
          type = lib.types.package;
          default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
          defaultText = lib.literalExpression "shelly-herdr.packages.\${system}.default";
          description = "The shelly-herdr package, which also carries the herdr plugin.";
        };
      };

      config = lib.mkIf cfg.enable {
        home.packages = [cfg.package];

        # `herdr plugin link` edits the plugin registry directly, needs no running
        # server, and re-linking the same plugin id is idempotent.
        home.activation.shellyHerdrPlugin = lib.hm.dag.entryAfter ["writeBoundary" "installPackages"] ''
          herdr=$(PATH="${config.home.profileDirectory}/bin:$PATH" command -v herdr || true)
          if [ -n "$herdr" ]; then
            $DRY_RUN_CMD "$herdr" plugin link "${cfg.package}/share/herdr/plugins/shelly" >/dev/null \
              || echo "shelly-herdr: could not link the herdr plugin; run 'herdr plugin link ${cfg.package}/share/herdr/plugins/shelly'" >&2
          else
            echo "shelly-herdr: herdr not found; install herdr, then run 'herdr plugin link ${cfg.package}/share/herdr/plugins/shelly'" >&2
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
