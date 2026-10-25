class Aroot < Formula
  desc "Explicitly installed, token-authorized Linux root command service"
  homepage "https://github.com/hamb1y/homebrew-tap/tree/main/src/aroot"
  url "https://github.com/hamb1y/homebrew-tap.git", revision: "456f34c735e76fb7c1b35bf4f789beb08d7c1daf"
  version "1.0.0"
  license :cannot_represent

  depends_on :linux

  def install
    cd "src/aroot" do
      system "make", "CC=#{ENV.cc}", "all"
      pkgshare.install "arootd", "aroot-client", "aroot-admin", "aroot",
                       "aroot-install", "aroot-uninstall", "aroot.service", "README.md"
    end
    (bin/"aroot-setup").write <<~EOS
      #!/bin/bash
      exec "#{opt_pkgshare}/aroot-install" "$@"
    EOS
    (bin/"aroot-uninstall").write <<~EOS
      #!/bin/bash
      exec "#{opt_pkgshare}/aroot-uninstall" "$@"
    EOS
    chmod 0755, bin/"aroot-setup"
    chmod 0755, bin/"aroot-uninstall"
  end

  def caveats
    <<~EOS
      aroot grants unrestricted root command authority only to administrator-provisioned
      local UIDs with private tokens. Homebrew does not install or start a root service.

      Explicit administrator installation (programs, unit and group only):
        sudo #{opt_bin}/aroot-setup

      Then provision a UID/token, add group membership, and explicitly enable the service.
      Read #{opt_pkgshare}/README.md for the complete grant/revoke and uninstall procedure.
      The installed Bash command is /usr/bin/aroot. Staging supports DESTDIR.
      macOS and full interactive shell job control are not supported.
    EOS
  end

  test do
    assert_match "aroot 1.0.0", shell_output("#{pkgshare}/aroot-client --version")
    system "bash", "-n", pkgshare/"aroot-install"
    system "bash", "-n", pkgshare/"aroot-uninstall"
    system "env", "DESTDIR=#{testpath}/stage", pkgshare/"aroot-install"
    assert_path_exists testpath/"stage/usr/bin/aroot"
    refute_path_exists testpath/"stage/etc/systemd/system/multi-user.target.wants/aroot.service"
  end
end
