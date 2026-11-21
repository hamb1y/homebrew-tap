class FoxhandHelper < Formula
  desc "Native messaging and MCP helper for the Foxhand Firefox add-on"
  homepage "https://foxhand.pages.dev/"
  version "0.6.2"
  license "MPL-2.0"

  on_macos do
    on_arm do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-darwin-arm64"
      sha256 "3c944b0a9fb69e89612851273aaafc1f69b04e0f8f9fe7204a4d08b53ce30a8b"
    end
    on_intel do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-darwin-x64"
      sha256 "5e83e2a2ed50cf6f765e03305694ce2da4522baa1cb1b5debe7cef391fb59839"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-linux-arm64"
      sha256 "3c35a02145b1224eb7c0b2ec34765767d887715c19ee5fcd813c34783d1ccc17"
    end
    on_intel do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-linux-x64"
      sha256 "44ea0c69ecd9f5d8f070cdc27ad09e3b574fd3391fa666b393b09714531c3469"
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
