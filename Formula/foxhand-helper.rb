class FoxhandHelper < Formula
  desc "Native messaging and MCP helper for the Foxhand Firefox add-on"
  homepage "https://foxhand.pages.dev/"
  version "0.6.3"
  license "MPL-2.0"

  on_macos do
    on_arm do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-darwin-arm64"
      sha256 "aa905322111b0a4601b1f3c3301bba14476e191fa788a3709d70fd4727cabf53"
    end
    on_intel do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-darwin-x64"
      sha256 "6f3f234aad740e966caa30813b800b73b02b532ef63d8599eab7caf6d5f1b114"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-linux-arm64"
      sha256 "e5a117c6917b2d5f7440c71b66b77c509550d4fe8e6721f48603fb4f71073693"
    end
    on_intel do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-linux-x64"
      sha256 "5de0acbb48b70d23a83698b8356d2eef0e9cf3570f2240646902916618803144"
    end
  end

  def install
    bin.install Dir["foxhand-host-*"].fetch(0) => "foxhand-helper"
    chmod 0755, bin/"foxhand-helper"
  end

  def caveats
    <<~EOS
      Register the helper with Firefox (also after upgrading):
        foxhand-helper install

      Restart Firefox after registering. To unregister the helper before
      removing this formula, run:
        foxhand-helper uninstall
    EOS
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/foxhand-helper --version 2>&1").strip
  end
end
