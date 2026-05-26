class Regi < Formula
  desc "Tiny newline-delimited plaintext register CLI"
  homepage "https://github.com/hamb1y/regi"
  url "https://github.com/hamb1y/regi/archive/refs/tags/v0.2.0.tar.gz"
  sha256 "0b39e6835807cb814aef9877103db1d355ef6af5060cce6c6d55150a83dc543d"
  head "https://github.com/hamb1y/regi.git", branch: "main"
  license "BSD-3-Clause"

  depends_on "go" => :build

  def install
    system "go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin/"regi", "."
  end

  test do
    ENV["HOME"] = testpath
    system bin/"regi", "add", "test", "hello"
    assert_equal "hello\n", shell_output("#{bin}/regi test")
    assert_path_exists testpath/".config/regi/registers/test.regi"
  end
end
