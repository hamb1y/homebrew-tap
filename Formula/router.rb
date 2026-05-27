class Router < Formula
  desc "OpenAI-compatible provider abstractor with smart/dumb model routing"
  homepage "https://github.com/hamb1y/homebrew-tap"
  url "https://github.com/hamb1y/homebrew-tap.git", branch: "main"
  version "0.1.0"
  license :cannot_represent

  depends_on "go" => :build

  def install
    system "go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin/"router", "src/router/router.go"
  end

  def caveats
    <<~EOS
      Create config/router.json and config/keys.json before running:
        router -config config/router.json -keys config/keys.json

      OAuth2 auth files are read from config/auth/ by default.
    EOS
  end

  test do
    assert_match "router config file", shell_output("#{bin}/router -h 2>&1")
  end
end
