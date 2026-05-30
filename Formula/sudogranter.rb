class Sudogranter < Formula
  desc "Local bearer-authenticated HTTP runner for sudo commands"
  homepage "https://github.com/hamb1y/homebrew-tap"
  url "https://github.com/hamb1y/homebrew-tap.git", branch: "main"
  version "0.1.0"
  license :cannot_represent
  revision 1

  depends_on "go" => :build

  def install
    cd "src/sudogranter" do
      system "go", "build", "-trimpath", "-ldflags=-s -w", "-o", bin/"sudogranter", "."
    end

    (pkgshare/"sudogranter.service").write <<~EOS
      [Unit]
      Description=sudogranter root command runner
      After=network-online.target
      Wants=network-online.target

      [Service]
      Type=simple
      ExecStart=#{opt_bin}/sudogranter
      Restart=on-failure
      RestartSec=2

      [Install]
      WantedBy=multi-user.target
    EOS

    (bin/"sudogranter-setup").write <<~EOS
      #!/usr/bin/env bash
      set -euo pipefail

      if [[ "${EUID}" -ne 0 ]]; then
        echo "sudogranter-setup must be run as root. Try:" >&2
        echo "  sudo #{opt_bin}/sudogranter-setup" >&2
        exit 1
      fi

      install -d -m 700 /etc/sudogranter /etc/sudogranter/runs

      if [[ ! -f /etc/sudogranter/config.json ]]; then
        token="$(openssl rand -hex 32)"
        printf '{\\n  "bearer_token": "%s",\\n  "addr": "127.0.0.1:64420"\\n}\\n' "$token" > /etc/sudogranter/config.json
        chmod 600 /etc/sudogranter/config.json
        echo "Created /etc/sudogranter/config.json"
        echo "Bearer token: $token"
      else
        chmod 600 /etc/sudogranter/config.json
        echo "Using existing /etc/sudogranter/config.json"
      fi

      install -m 644 "#{opt_pkgshare}/sudogranter.service" /etc/systemd/system/sudogranter.service
      systemctl daemon-reload
      systemctl enable --now sudogranter.service
      systemctl --no-pager status sudogranter.service
    EOS
    chmod 0755, bin/"sudogranter-setup"
  end

  def post_install
    ohai "sudogranter systemd setup", <<~EOS
      To create /etc/sudogranter/config.json and enable the root systemd service:

        sudo #{opt_bin}/sudogranter-setup

      Manual systemd steps are also available in `brew info hamb1y/tap/sudogranter`.
    EOS
  end

  def caveats
    systemd_instructions
  end

  test do
    assert_match "sudogranter 0.1.0", shell_output("#{bin}/sudogranter --version")
  end

  def systemd_instructions
    <<~EOS
      sudogranter is intended to run as a root-owned systemd service.

      Easiest setup:

        sudo #{opt_bin}/sudogranter-setup

      Manual setup:

      Create its config and log directory:

        sudo install -d -m 700 /etc/sudogranter /etc/sudogranter/runs
        TOKEN="$(openssl rand -hex 32)"
        printf '{\\n  "bearer_token": "%s",\\n  "addr": "127.0.0.1:64420"\\n}\\n' "$TOKEN" | sudo tee /etc/sudogranter/config.json >/dev/null
        sudo chmod 600 /etc/sudogranter/config.json

      Install and enable the systemd service:

        sudo cp #{opt_pkgshare}/sudogranter.service /etc/systemd/system/sudogranter.service
        sudo systemctl daemon-reload
        sudo systemctl enable --now sudogranter.service

      Submit a command:

        curl -X POST http://localhost:64420/run \\
          -H "Authorization: Bearer $TOKEN" \\
          -d '/bin/bash <- id'

      Stream a run:

        curl http://localhost:64420/<uuid>/ \\
          -H "Authorization: Bearer $TOKEN"
    EOS
  end
end
