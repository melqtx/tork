{
  lib,
  buildGoModule,
  fetchFromGitHub,
  version ? "0.1.4",
  source ? fetchFromGitHub {
    owner = "melqtx";
    repo = "tork";
    tag = "v${version}";
    hash = "sha256-8rqwig+iNNn6X8W7jqQ8oQ+ucARDTE3JSTBnMudJ3+M=";
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

  # Nix builds with HOME=/homeless-shelter. A few integration-style tests create
  # an isolated tork config, which derives its default download directory from
  # HOME, so give the check phase a writable private home instead.
  preCheck = ''
    export HOME="$TMPDIR/home"
    mkdir -p "$HOME"
  '';

  meta = {
    description = "Terminal torrent search and download client";
    homepage = "https://github.com/melqtx/tork";
    license = lib.licenses.mit;
    mainProgram = "tork";
    platforms = lib.platforms.unix;
  };
}
