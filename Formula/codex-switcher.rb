class CodexSwitcher < Formula
  desc "TUI for saving and switching multiple Codex logins"
  homepage "https://github.com/hamb1y/homebrew-tap"
  url "https://raw.githubusercontent.com/hamb1y/homebrew-tap/e7a3134a4ce2f30423fcea79e84f0c2e7f31760c/bin/codex-switcher"
  version "0.1.0"
  sha256 "553086b2f42b4bdbe5b635c81ece7873a76f1a79f135e1e8a2f85211458cb08d"
  license :cannot_represent

  depends_on "gum"

  def install
    bin.install "codex-switcher"
  end

  test do
    assert_match "Usage: codex-switcher", shell_output("#{bin}/codex-switcher --help")
  end
end
