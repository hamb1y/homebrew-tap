class Numtrack < Formula
  include Language::Python::Virtualenv

  desc "NumLock-toggled numpad mouse for Linux Wayland desktops"
  homepage "https://github.com/hamb1y/numtrack"
  url "https://github.com/hamb1y/numtrack/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "1de8968468a96cfce1d75286d58e6ead977f1e44c95d371fdcd5de3e3789b42b"
  license :cannot_represent
  head "https://github.com/hamb1y/numtrack.git", branch: "main"

  depends_on :linux
  depends_on "python@3.14"

  resource "evdev" do
    url "https://files.pythonhosted.org/packages/a5/f5/397b61091120a9ca5001041dd7bf76c385b3bfd67a0e5bcb74b852bd22a4/evdev-1.9.3.tar.gz"
    sha256 "2c140e01ac8437758fa23fe5c871397412461f42d421aa20241dc8fe8cfccbc9"
  end

  def install
    virtualenv_install_with_resources
    pkgshare.install "contrib/numtrack.service"
  end

  service do
    run opt_bin/"numtrack"
    keep_alive true
    log_path var/"log/numtrack.log"
    error_log_path var/"log/numtrack.log"
  end

  def caveats
    <<~EOS
      Numtrack needs read/write access to your keypad's /dev/input/event* node
      and /dev/uinput. See the README for input-group and udev setup.

      Run `numtrack` to try it, or `brew services start numtrack` to autostart.
      At startup, mouse mode follows the physical NumLock LED by default.
      Configuration: ~/.config/numtrack/config.toml
      Print defaults: numtrack --print-config
    EOS
  end

  test do
    assert_match "numtrack #{version}", shell_output("#{bin}/numtrack --version")
    (testpath/"config.toml").write <<~TOML
      enabled = false
      acceleration = 0
      [bindings]
      KEY_KP5 = "passthrough"
    TOML
    output = shell_output("#{bin}/numtrack --config #{testpath}/config.toml --check-config")
    assert_match "Configuration valid", output
    assert_match 'enabled = "numlock"', shell_output("#{bin}/numtrack --print-config")
  end
end
