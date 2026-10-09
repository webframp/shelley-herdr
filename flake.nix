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
        version = "0.1.0";
        src = self;
        vendorHash = null; # stdlib only

        postInstall = ''
          # herdr plugin: manifest plus a relative link to the binary, since the
          # manifest launches ./shelly-herdr from the plugin root.
          plugin=$out/share/herdr/plugins/shelly
          mkdir -p $plugin
          cp herdr-plugin.toml $plugin/
          ln -s ../../../../bin/shelly-herdr $plugin/shelly-herdr
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

    devShells = forEachSystem (pkgs: {
      default = pkgs.mkShell {packages = [pkgs.go pkgs.gopls];};
    });

    formatter = forEachSystem (pkgs: pkgs.alejandra);
  };
}
