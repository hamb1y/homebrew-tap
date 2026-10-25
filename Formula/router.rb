class Router < Formula
  desc "OpenAI-compatible gateway with deterministic model routing and streaming"
  homepage "https://github.com/hamb1y/homebrew-tap"
  url "https://raw.githubusercontent.com/hamb1y/homebrew-tap/12302c0226c58d2f238d9253aeaebac15ec39165/src/router/router.go"
  version "0.2.0"
  sha256 "3ca5a91fb55bb4ca6a78637b6c96625e86d532b27055aced2578dca494905372"
  license :cannot_represent

  depends_on "go" => :build

  def install
    system "go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin/"router", "router.go"
  end

  def caveats
    <<~EOS
      Initialize private templates with:
        router -init
      Edit the generated router.json and keys.json, then check and run:
        router -check
        router

      The default listener is 127.0.0.1:8080. For remote access, place router behind
      an authenticated TLS proxy. No user configuration is changed during install.
    EOS
  end

  test do
    ENV["HOME"] = testpath
    ENV["XDG_CONFIG_HOME"] = testpath/".config"
    assert_match "router 0.2.0", shell_output("#{bin}/router -version")
    system bin/"router", "-config", testpath/"router.json", "-keys", testpath/"keys.json", "-init"
    assert_match "router config valid", shell_output("#{bin}/router -config #{testpath}/router.json -keys #{testpath}/keys.json -check")
    assert_equal 0600, (testpath/"keys.json").stat.mode & 0777
  end
end
