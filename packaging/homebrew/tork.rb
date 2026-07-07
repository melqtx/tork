class Tork < Formula
  desc "Terminal torrent search and download client"
  homepage "https://github.com/melqtx/tork"
  url "https://github.com/melqtx/tork/archive/refs/tags/v0.1.1.tar.gz"
  sha256 "554a58be2a323283bf1e82e86e68dd222e0acf69b65136216be3e10c3aa05131"
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
