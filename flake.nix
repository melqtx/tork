{
  description = "Terminal torrent search and download client";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/b5aa0fbd538984f6e3d201be0005b4463d8b09f8";

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
      version = "0.3.0";
      vendorHash = "sha256-mpuvGJEygfcfsGftK1oPjPCfkko28VE22MmSRL35Tdo=";
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
