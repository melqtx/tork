{
  description = "Terminal torrent search and download client";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/34ab99075ac4f7e40cf037eef32cb1c360bb85e9";

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
      pkgsFor = system: import nixpkgs { inherit system; };
      version = "0.4.0";
      vendorHash = "sha256-4/sDvcE6xIV7RQPDrwZsftkSQwzljfM78yDDIg3QMhg=";
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = pkgsFor system;
          tork = pkgs.callPackage ./packaging/nix/package.nix {
            inherit version vendorHash;
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
            packages = [ pkgs.go ];
          };
        }
      );
    };
}
