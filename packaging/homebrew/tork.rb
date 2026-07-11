class Tork < Formula
  desc "Terminal torrent search and download client"
  homepage "https://github.com/melqtx/tork"
  url "https://github.com/melqtx/tork/archive/refs/tags/v0.2.0.tar.gz"
  sha256 "3d9b1440e6dbc8156546157326f452b91786ac0782bffb9c28576eaee3f2674d"
  license "MIT"
  head "https://github.com/melqtx/tork.git", branch: "main"

  depends_on "go" => :build

  def install
    ldflags = "-s -w -X main.version=#{version}"
    system "go", "build", *std_go_args(ldflags: ldflags), "./cmd/tork"
  end

  test do
    assert_match "tork #{version}", shell_output("#{bin}/tork --version")
  end
end
