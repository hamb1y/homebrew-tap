class Regi < Formula
  desc "Tiny newline-delimited plaintext register CLI"
  homepage "https://github.com/hamb1y/regi"
  url "https://github.com/hamb1y/regi.git", revision: "7a9b7f1bb4deab01195152f5a21177812befe900"
  version "0.3.0"
  head "https://github.com/hamb1y/regi.git", branch: "main"
  license "BSD-3-Clause"

  depends_on "go" => :build

  def install
    system "go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin/"regi", "."
  end

  test do
    ENV["HOME"] = testpath
    ENV["XDG_CONFIG_HOME"] = testpath/".config"
    assert_match "regi 0.3.0", shell_output("#{bin}/regi --version")
    system bin/"regi", "add", "test", "hello"
    assert_equal "hello\n", shell_output("#{bin}/regi test")
    assert_path_exists testpath/".config/regi/registers/test.regi"
    assert_equal "hello\n", shell_output("#{bin}/regi del --dry-run test hello")
    assert_equal "hello\n", shell_output("#{bin}/regi test")
    system bin/"regi", "del", "test", "hello"
    assert_equal "", shell_output("#{bin}/regi test")
  end
end
