{
  description = "Terminal torrent search and download client";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/d592aacecd3575d7e11de963d9b20dd2ef4db603";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "riscv64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      pkgsFor = system: import nixpkgs {
        inherit system;
        overlays = [
          (_final: prev: {
            # Keep Intel macOS support while applying the Go security update.
            go_1_26 = prev.go_1_26.overrideAttrs (_old: {
              version = "1.26.10";
              src = prev.fetchurl {
                url = "https://go.dev/dl/go1.26.10.src.tar.gz";
                hash = "sha256-/WPog7ovK/ypPBZGsx6+9j4rHk0rMXyXidl2po2B828=";
              };
            });
          })
        ];
      };
      version = "0.5.0";
      vendorHash = "sha256-gQLqY3trLGP/2teQlR+px+/iIKyzEs90pi52ufRXufc=";
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = pkgsFor system;
          tork = pkgs.callPackage ./packaging/nix/package.nix {
            inherit version vendorHash;
            buildGoModule = pkgs.buildGo126Module;
            source = self;
          };
        in
        {
          inherit tork;
          default = tork;
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/tork";
        };
      });

      devShells = forAllSystems (
        system:
        let
          pkgs = pkgsFor system;
        in
        {
          default = pkgs.mkShell {
            packages = [ pkgs.go_1_26 ];
          };
        }
      );
    };
}
