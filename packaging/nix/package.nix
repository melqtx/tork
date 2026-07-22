{
  lib,
  buildGoModule,
  fetchFromGitHub,
  version ? "0.3.2",
  vendorHash ? "sha256-mpuvGJEygfcfsGftK1oPjPCfkko28VE22MmSRL35Tdo=",
  source ? fetchFromGitHub {
    owner = "melqtx";
    repo = "tork";
    tag = "v${version}";
    hash = "sha256-7LnFbFv9I0b2tC611Ot6QpfAR8R0SGE1GzJ5I2Orvq4=";
  },
}:

buildGoModule rec {
  pname = "tork";
  inherit version;

  src = source;

  inherit vendorHash;

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
