{
  lib,
  buildGoModule,
  fetchFromGitHub,
  version ? "0.4.1",
  vendorHash ? "sha256-4/sDvcE6xIV7RQPDrwZsftkSQwzljfM78yDDIg3QMhg=",
  source ? fetchFromGitHub {
    owner = "melqtx";
    repo = "tork";
    tag = "v${version}";
    hash = "sha256-mTNvtUncCxi1M1OdnNo5LHo5HB+DWJcNQuqRDrl8LSA=";
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
