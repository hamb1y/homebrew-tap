class FoxhandHelper < Formula
  desc "Native messaging and MCP helper for the Foxhand Firefox add-on"
  homepage "https://foxhand.pages.dev/"
  version "0.5.9"
  license "MPL-2.0"

  on_macos do
    on_arm do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-darwin-arm64"
      sha256 "06cfdf4c60077a96439b97430001f0e2cbdbe69845b903d0b24500d1e90ee34d"
    end
    on_intel do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-darwin-x64"
      sha256 "4b14db7926378f0a5030da8b364751629e20aae1a81b2db06e713172e31ef6a2"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-linux-arm64"
      sha256 "a0d6bbb7d349ab57c96253880c17d0795b0174c6ff05d2a50a0df4614bc103e3"
    end
    on_intel do
      url "https://github.com/hamb1y/foxhand/releases/download/v#{version}/foxhand-host-linux-x64"
      sha256 "641f692492923dd3e7cbd16a2758a9bd7f33fe38180f4cbd33a539f8c339e5da"
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
