class Tork < Formula
  desc "Terminal torrent search and download client"
  homepage "https://github.com/melqtx/tork"
  url "https://github.com/melqtx/tork/archive/refs/tags/v0.1.2.tar.gz"
  sha256 "350337c073c721952f2ae078aa8ea4a1e3060e0261dfd1d9b30da401d0b9c27f"
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
