class CodexSwitcher < Formula
  desc "TUI for saving and switching multiple Codex logins"
  homepage "https://github.com/hamb1y/homebrew-tap"
  url "https://raw.githubusercontent.com/hamb1y/homebrew-tap/main/bin/codex-switcher"
  version "0.1.0"
  sha256 "52e07f8f70edc0a0ee2d622d2d46241409b089840ce2a5cabf2be77f42e370c2"
  license :cannot_represent

  depends_on "gum"

  def install
    bin.install "codex-switcher"
  end

  test do
    assert_match "Usage: codex-switcher", shell_output("#{bin}/codex-switcher --help")
  end
end
