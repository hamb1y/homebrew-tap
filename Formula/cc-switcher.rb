class CcSwitcher < Formula
  desc "Save and switch the login used by your existing Claude Code installation"
  homepage "https://github.com/hamb1y/cc-switcher"
  url "https://raw.githubusercontent.com/hamb1y/cc-switcher/9f81aebbd84a4b2a7e4a6d98e01ff64a83e26c75/bin/cc-switcher"
  version "0.2.0"
  sha256 "292b63e06f50055fa617773f9163ecf0d7776816e0a1c65d6822f536edc9276b"
  license :cannot_represent

  depends_on "gum"
  depends_on "python@3.14"

  def install
    bin.install "cc-switcher"
    inreplace bin/"cc-switcher", "#!/usr/bin/env python3", "#!#{formula_opt_bin("python@3.14")}/python3.14"
  end

  def caveats
    <<~EOS
      Save the current login: cc-switcher save Work
      Activate a saved login: cc-switcher switch work
      Close existing Claude sessions before switching, then launch claude normally.
      Settings, sessions and MCP credentials are preserved.
      Install Claude Code separately for login. Isolated login capture is Linux only;
      on macOS use claude auth login, then cc-switcher save LABEL.
      Profiles and backups contain credentials; keep ~/.cc-switcher private.
    EOS
  end

  test do
    ENV["HOME"] = testpath
    ENV["CLAUDE_CONFIG_DIR"] = testpath/"claude"
    ENV["CLAUDE_SECURESTORAGE_CONFIG_DIR"] = testpath/"claude"
    ENV["CC_SWITCHER_DIR"] = testpath/"profiles"
    assert_match "cc-switcher 0.2.0", shell_output("#{bin}/cc-switcher --version")
    assert_match "Active", shell_output("#{bin}/cc-switcher status")
    assert_equal 0700, (testpath/"profiles").stat.mode & 0777
    assert_match "profile does not exist", shell_output("#{bin}/cc-switcher switch missing 2>&1", 1)
    if OS.linux?
      (testpath/"claude").mkpath
      auth = testpath/"claude/.credentials.json"
      auth.write('{"claudeAiOauth":{"accessToken":"work-test"},"mcpOAuth":{"keep":true}}')
      system bin/"cc-switcher", "save", "Work"
      auth.unlink
      auth.write('{"claudeAiOauth":{"accessToken":"other-test"},"mcpOAuth":{"keep":true}}')
      system bin/"cc-switcher", "switch", "work"
      assert_match "work-test", auth.read
      assert_match "mcpOAuth", auth.read
      assert_equal 0600, auth.stat.mode & 0777
    end
  end
end
