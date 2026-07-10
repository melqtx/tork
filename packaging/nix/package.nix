{
  lib,
  buildGoModule,
  fetchFromGitHub,
  version ? "0.1.3",
  source ? fetchFromGitHub {
    owner = "melqtx";
    repo = "tork";
    tag = "v${version}";
    hash = "sha256-O4eXhAOxnPXjOhXGm0Luuq5IRCWeGJrJlZ+ey7rPhPo=";
  },
}:

buildGoModule rec {
  pname = "tork";
  inherit version;

  src = source;

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
    platforms = lib.platforms.unix;
  };
}
