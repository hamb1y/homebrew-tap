class FoxhandHelper < Formula
  desc "Native messaging and MCP helper for the Foxhand Firefox add-on"
  homepage "https://foxhand.pages.dev/"
  version "0.6.1"
  license "MPL-2.0"

  on_macos do
    on_arm do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-darwin-arm64"
      sha256 "19ae327cb1f0de64128561148ac53bb70f29c1fdd828fc58b669c63fda6362a4"
    end
    on_intel do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-darwin-x64"
      sha256 "16de925dc83af37ce3225ca87411e2904ff4360f198122407b3b307514987767"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-linux-arm64"
      sha256 "30cc82ac85805431399d2a17a0eedb70a1dc90e47e9dfbece839a77bbbc5f996"
    end
    on_intel do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-linux-x64"
      sha256 "023e1e4093ede63e81e2638f585cee4a2fa8fa0fc8c7801c5ca8cab526e9a506"
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
