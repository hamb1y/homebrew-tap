class CcSwitcher < Formula
  desc "Switch between isolated Claude Code accounts"
  homepage "https://github.com/hamb1y/cc-switcher"
  url "https://raw.githubusercontent.com/hamb1y/cc-switcher/0b37d18cccc411355cfaaa596528311b57fd7e31/bin/cc-switcher"
  version "0.1.0"
  sha256 "a357625cb44fdfde8a45f08aeec6e1538fcfcd30eb32b9d13dc72f9ce3a8273e"
  license :cannot_represent

  depends_on "gum"
  depends_on "python@3.14"

  def install
    bin.install "cc-switcher"
    inreplace bin/"cc-switcher", "#!/usr/bin/env python3", "#!#{formula_opt_bin("python@3.14")}/python3.14"
  end

  def caveats
    <<~EOS
      Install Claude Code separately, then run:
        cc-switcher login Work
        cc-switcher switch work
        cc-switcher run
      Selection affects cc-switcher run; plain claude is unchanged.
      Each account has separate settings and session history.
      Keep ~/.cc-switcher private; profiles and backups may contain credentials.
    EOS
  end

  test do
    ENV["CC_SWITCHER_DIR"] = testpath/"profiles"
    assert_match "cc-switcher 0.1.0", shell_output("#{bin}/cc-switcher --version")
    assert_match "Selected: (none)", shell_output("#{bin}/cc-switcher status")
    assert_equal 0700, (testpath/"profiles").stat.mode & 0777
    assert_match "select a profile first", shell_output("#{bin}/cc-switcher run 2>&1", 1)
  end
end
