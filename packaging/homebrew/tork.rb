class Tork < Formula
  desc "Terminal torrent search and download client"
  homepage "https://github.com/melqtx/tork"
  url "https://github.com/melqtx/tork/archive/refs/tags/v0.5.1.tar.gz"
  sha256 "970402a43cae9d542893de07aa1ef41b3207749cacd6fe7d0cbc8740f6a83978"
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
