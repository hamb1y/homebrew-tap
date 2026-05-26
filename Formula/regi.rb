class Regi < Formula
  desc "Tiny newline-delimited plaintext register CLI"
  homepage "https://github.com/hamb1y/regi"
  url "https://github.com/hamb1y/regi/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "c9da727a03cb3d7d37dcbd5f9508501d70addd77e34b18bf4c7b756200f6a55d"
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
