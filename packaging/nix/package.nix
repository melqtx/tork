{
  lib,
  buildGoModule,
  fetchFromGitHub,
}:

buildGoModule rec {
  pname = "tork";
  version = "0.1.0";

  src = fetchFromGitHub {
    owner = "melqtx";
    repo = "tork";
    tag = "v${version}";
    hash = "sha256-IV/bACY4YU69cudyZHuCt5jvJ6etHYXlXUtpnZc7JCw=";
  };

  vendorHash = "sha256-Vk3lmPUDvuhOzha8GlH0anRLgVhhyFjz9y328T2gycs=";

  subPackages = [ "cmd/tork" ];

  ldflags = [
    "-s"
    "-w"
    "-X main.version=${version}"
  ];

  meta = {
    description = "Terminal torrent search and download client";
    homepage = "https://github.com/melqtx/tork";
    license = lib.licenses.mit;
    mainProgram = "tork";
  };
}
