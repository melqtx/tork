{
  lib,
  buildGoModule,
  fetchFromGitHub,
  writableTmpDirAsHomeHook,
  version ? "0.5.0",
  vendorHash ? "sha256-gQLqY3trLGP/2teQlR+px+/iIKyzEs90pi52ufRXufc=",
  source ? fetchFromGitHub {
    owner = "melqtx";
    repo = "tork";
    tag = "v${version}";
    hash = "sha256-l8U7h8X9NhMIdnNYXgJIfd/b03GZzbvIGLo0mrcJucU=";
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

  nativeBuildInputs = [ writableTmpDirAsHomeHook ];

  meta = {
    description = "Terminal torrent search and download client";
    homepage = "https://github.com/melqtx/tork";
    license = lib.licenses.mit;
    mainProgram = "tork";
    platforms = lib.platforms.unix;
  };
}
