class Tork < Formula
  desc "Terminal torrent search and download client"
  homepage "https://github.com/melqtx/tork"
  url "https://github.com/melqtx/tork/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "a9c060742dd2919e897b822d6f9a292cee9d02b564631f876f3cbf0d5dc03c3f"
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
