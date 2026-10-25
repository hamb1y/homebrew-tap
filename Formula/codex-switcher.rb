class CodexSwitcher < Formula
  desc "Save and atomically switch file-based Codex logins"
  homepage "https://github.com/hamb1y/homebrew-tap"
  url "https://raw.githubusercontent.com/hamb1y/homebrew-tap/12302c0226c58d2f238d9253aeaebac15ec39165/bin/codex-switcher"
  version "0.2.0"
  sha256 "a8cab6b6ad685e7c7c939230692e3c4a99a74012ccd68cdd396a206e573724e5"
  license :cannot_represent

  depends_on "gum"
  depends_on "python@3.14"

  def install
    bin.install "codex-switcher"
    inreplace bin/"codex-switcher", "SWITCHER_PYTHON=python3", "SWITCHER_PYTHON=#{Formula["python@3.14"].opt_bin}/python3.14"
  end

  def caveats
    <<~EOS
      Set cli_auth_credentials_store = "file" in Codex config.toml.
      Keyring credentials and running Codex sessions are not switched.
      Profiles and backups contain credentials; keep ~/.codex-switcher private.
      Install Codex separately to use the interactive login command.
    EOS
  end

  test do
    ENV["HOME"] = testpath
    ENV["CODEX_HOME"] = testpath/"codex"
    ENV["CODEX_SWITCHER_DIR"] = testpath/"profiles"
    (testpath/"codex").mkpath
    (testpath/"codex/auth.json").write('{"OPENAI_API_KEY":"test-value"}')
    system bin/"codex-switcher", "save", "test"
    system bin/"codex-switcher", "switch", "test"
    assert_match "yes\ttest\ttest", shell_output("#{bin}/codex-switcher list")
    assert_equal 0600, (testpath/"codex/auth.json").stat.mode & 0777
  end
end
