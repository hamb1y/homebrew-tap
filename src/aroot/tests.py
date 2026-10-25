#!/usr/bin/env python3
"""Real unprivileged Unix-socket tests; the test daemon cannot acquire root."""
import array
import concurrent.futures
import json
import os
from pathlib import Path
import signal
import socket
import stat
import struct
import subprocess
import tempfile
import time
import unittest

HERE = Path(__file__).resolve().parent
UID = os.getuid()
MAGIC = 0x41525431


def private_write(path, data):
    path.write_bytes(data)
    path.chmod(0o600)


@unittest.skipIf(UID == 0, "run integration tests as an unprivileged user")
class Integration(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="aroot-")
        self.root = Path(self.temp.name)
        self.policy = self.root / "users"
        self.policy.mkdir(mode=0o700)
        self.sock = self.root / "control.sock"
        self.log = self.root / "audit.jsonl"
        self.token = os.urandom(32)
        self.token_file = self.root / "token"
        private_write(self.token_file, self.token)
        private_write(self.policy / str(UID), self.token)
        self.daemon = subprocess.Popen(
            [str(HERE / "arootd-test"), "--test-paths", str(self.sock), str(self.policy), str(self.log)],
            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
        )
        deadline = time.monotonic() + 3
        while not self.sock.exists() and self.daemon.poll() is None and time.monotonic() < deadline:
            time.sleep(0.01)
        self.assertTrue(self.sock.exists(), self.daemon.stderr.read() if self.daemon.poll() is not None else "socket startup timeout")

    def tearDown(self):
        self.daemon.terminate()
        self.daemon.communicate(timeout=5)
        self.temp.cleanup()

    def command(self, *args, token_file=None, **kwargs):
        return subprocess.run(
            [str(HERE / "aroot-client-test"), "--test-socket", str(self.sock),
             "--token-file", str(token_file or self.token_file), "--", *args],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=5, **kwargs,
        )

    def raw(self, token=None, payload=b"/bin/true\0", argc=1, magic=MAGIC, descriptors=True):
        with socket.socket(socket.AF_UNIX) as s:
            s.settimeout(3)
            s.connect(str(self.sock))
            header = struct.pack("!IIII32s", magic, 1, argc, len(payload), self.token if token is None else token)
            with open(os.devnull, "r+b", buffering=0) as null:
                ancillary = [(socket.SOL_SOCKET, socket.SCM_RIGHTS, array.array("i", [null.fileno()] * 3))] if descriptors else []
                s.sendmsg([header], ancillary)
                response = s.recv(8)
                self.assertEqual(len(response), 8)
                magic_reply, status = struct.unpack("!II", response)
                self.assertEqual(magic_reply, MAGIC)
                if status:
                    return status
                s.sendall(payload)
                response = s.recv(8)
                self.assertEqual(len(response), 8)
                magic_reply, status = struct.unpack("!II", response)
                self.assertEqual(magic_reply, MAGIC)
                return status

    def records(self):
        return [json.loads(line) for line in self.log.read_text().splitlines()]

    def test_execution_stdio_and_literal_arguments(self):
        result = self.command("/usr/bin/printf", "%s|%s|%s", "a b", "$(touch /tmp/aroot-no-eval)", "x; exit 1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, b"a b|$(touch /tmp/aroot-no-eval)|x; exit 1")
        result = self.command("/bin/cat", input=b"\x00\xffbinary\n")
        self.assertEqual(result.stdout, b"\x00\xffbinary\n")
        result = self.command("/bin/sh", "-c", "printf stderr >&2; exit 23")
        self.assertEqual(result.returncode, 23)
        self.assertEqual(result.stderr, b"stderr")

    def test_executable_text_does_not_implicitly_start_a_shell(self):
        marker = self.root / "shell-fallback"
        script = self.root / "without-shebang"
        script.write_text(f"touch '{marker}'\n")
        script.chmod(0o700)
        self.assertEqual(self.command(str(script)).returncode, 126)
        self.assertFalse(marker.exists())

    def test_environment_and_cwd(self):
        env = dict(os.environ, AROOT_SENTINEL="secret", PATH="/nonexistent", HOME="/tmp/untrusted", LD_LIBRARY_PATH="/tmp/untrusted")
        result = self.command("/bin/sh", "-c", 'printf "%s|%s|%s|%s" "$PWD" "$HOME" "$PATH" "${AROOT_SENTINEL-unset}"', env=env)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, b"/|/root|/usr/sbin:/usr/bin:/sbin:/bin|unset")
        result = self.command("true")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.command("/bin/sh", "-c", "test ! -e /proc/self/fd/3").returncode, 0)

    def test_missing_wrong_revoked_and_wrong_uid_policy(self):
        self.assertEqual(self.command("true", token_file=self.root / "missing").returncode, 126)
        wrong = self.root / "wrong"
        private_write(wrong, os.urandom(32))
        self.assertEqual(self.command("true", token_file=wrong).returncode, 126)
        (self.policy / str(UID)).rename(self.policy / str(UID + 1))
        self.assertEqual(self.command("true").returncode, 126)
        (self.policy / str(UID + 1)).unlink()
        self.assertEqual(self.command("true").returncode, 126)
        self.assertEqual(self.raw(token=bytes(32)), 126)
        self.assertGreaterEqual(sum(r["event"] == "deny" for r in self.records()), 4)

    def test_unsafe_token_and_policy(self):
        self.token_file.chmod(0o644)
        self.assertEqual(self.command("true").returncode, 126)
        self.token_file.chmod(0o600)
        link = self.root / "token-link"
        link.symlink_to(self.token_file)
        self.assertEqual(self.command("true", token_file=link).returncode, 126)
        (self.policy / str(UID)).chmod(0o644)
        self.assertEqual(self.command("true").returncode, 126)
        (self.policy / str(UID)).unlink()
        (self.policy / str(UID)).symlink_to(self.token_file)
        self.assertEqual(self.command("true").returncode, 126)

    def test_token_fd(self):
        read_fd, write_fd = os.pipe()
        try:
            os.write(write_fd, self.token)
            os.close(write_fd)
            result = subprocess.run([str(HERE / "aroot-client-test"), "--test-socket", str(self.sock), "--token-fd", str(read_fd), "true"], pass_fds=(read_fd,), capture_output=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stderr)
        finally:
            os.close(read_fd)

    def test_protocol_rejections(self):
        self.assertEqual(self.raw(descriptors=False), 126)
        self.assertEqual(self.raw(magic=0), 126)
        self.assertEqual(self.raw(payload=b"/bin/true", argc=1), 126)
        self.assertEqual(self.raw(payload=b"/bin/true\0extra\0", argc=1), 126)
        self.assertEqual(self.raw(payload=b"x\0", argc=129), 126)
        self.assertEqual(self.raw(payload=b"x" * 4097 + b"\0"), 126)
        self.assertEqual(self.command("/bin/echo", "x" * 4097).returncode, 125)

    def test_request_payload_has_an_absolute_deadline(self):
        with socket.socket(socket.AF_UNIX) as s:
            s.settimeout(3)
            s.connect(str(self.sock))
            payload = b"/bin/true\0"
            header = struct.pack("!IIII32s", MAGIC, 1, 1, len(payload), self.token)
            with open(os.devnull, "r+b", buffering=0) as null:
                s.sendmsg([header], [(socket.SOL_SOCKET, socket.SCM_RIGHTS, array.array("i", [null.fileno()] * 3))])
                self.assertEqual(struct.unpack("!II", s.recv(8)), (MAGIC, 0))
                time.sleep(2)
                s.sendall(payload[:1])
                time.sleep(2)
                s.sendall(payload[1:2])
                # Per-read timeouts would allow another five seconds here.
                self.assertEqual(struct.unpack("!II", s.recv(8)), (MAGIC, 126))
        self.assertFalse(any(r["event"] == "start" for r in self.records()))

    def test_duplicate_daemon_does_not_replace_live_socket(self):
        duplicate = subprocess.run([str(HERE / "arootd-test"), "--test-paths", str(self.sock), str(self.policy), str(self.log)], capture_output=True, timeout=3)
        self.assertEqual(duplicate.returncode, 125)
        self.assertIn(b"already running", duplicate.stderr)
        self.assertEqual(self.command("true").returncode, 0)

    def test_no_command_on_audit_failure(self):
        marker = self.root / "must-not-exist"
        self.log.chmod(0o400)
        result = self.command("/bin/touch", str(marker))
        self.assertEqual(result.returncode, 125, result.stderr)
        self.assertFalse(marker.exists())
        self.log.chmod(0o600)

    def test_audit_events_no_secret_or_arguments(self):
        marker = "ARGUMENT_SECRET_658bc049"
        self.assertEqual(self.command("/bin/echo", marker).returncode, 0)
        self.assertEqual(self.command("not-a-real-aroot-command").returncode, 127)
        self.assertEqual(self.raw(token=bytes(32)), 126)
        records = self.records()
        self.assertTrue({"daemon_start", "allow", "deny", "start", "finish"}.issubset({r["event"] for r in records}))
        self.assertTrue(all(set(r) == {"time", "event", "uid", "request", "status", "requested_executable", "resolved_executable", "requested_executable_truncated", "resolved_executable_truncated"} for r in records))
        echo = [r for r in records if r["event"] == "finish" and r["requested_executable"] == "/bin/echo"]
        self.assertEqual(len(echo), 1)
        self.assertEqual(echo[0]["resolved_executable"], os.path.realpath("/bin/echo"))
        self.assertFalse(echo[0]["requested_executable_truncated"])
        denied = [r for r in records if r["event"] == "deny"]
        self.assertTrue(all(r["requested_executable"] is None for r in denied))
        raw = self.log.read_bytes()
        self.assertNotIn(self.token, raw)
        self.assertNotIn(self.token.hex().encode(), raw)
        self.assertNotIn(marker.encode(), raw)
        self.assertEqual(stat.S_IMODE(self.log.stat().st_mode), 0o600)

    def test_executable_identity_is_json_escaped(self):
        script = self.root / 'quote"backslash\\newline\n'
        script.write_text("#!/bin/sh\nexit 0\n")
        script.chmod(0o700)
        self.assertEqual(self.command(str(script)).returncode, 0)
        record = [r for r in self.records() if r["event"] == "finish"][0]
        self.assertEqual(record["requested_executable"], str(script))
        self.assertEqual(record["resolved_executable"], str(script.resolve()))
        self.assertFalse(record["resolved_executable_truncated"])

    def test_concurrent_commands_and_audit(self):
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            results = list(pool.map(lambda _: self.command("/bin/true"), range(20)))
        self.assertTrue(all(r.returncode == 0 for r in results))
        self.assertEqual(sum(r["event"] == "finish" for r in self.records()), 20)

    def test_signal_forwarding_and_exit(self):
        marker = self.root / "running"
        proc = subprocess.Popen([str(HERE / "aroot-client-test"), "--test-socket", str(self.sock), "--token-file", str(self.token_file), "/bin/sh", "-c", f"touch '{marker}'; exec sleep 20"], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            deadline = time.monotonic() + 3
            while not marker.exists() and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertTrue(marker.exists())
            proc.send_signal(signal.SIGINT)
            proc.communicate(timeout=3)
            self.assertEqual(proc.returncode, 128 + signal.SIGINT)
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.communicate(timeout=3)

    def test_disconnect_kills_command_group(self):
        marker = self.root / "pid"
        proc = subprocess.Popen([str(HERE / "aroot-client-test"), "--test-socket", str(self.sock), "--token-file", str(self.token_file), "/bin/sh", "-c", f"echo $$ > '{marker}'; exec sleep 20"], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        try:
            deadline = time.monotonic() + 3
            while not marker.exists() and time.monotonic() < deadline:
                time.sleep(0.01)
            self.assertTrue(marker.exists())
            command_pid = int(marker.read_text())
            proc.kill()
            proc.wait(timeout=3)
            deadline = time.monotonic() + 3
            while time.monotonic() < deadline:
                try:
                    os.kill(command_pid, 0)
                except ProcessLookupError:
                    break
                time.sleep(0.02)
            else:
                self.fail("command survived client disconnect")
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.wait(timeout=3)


@unittest.skipIf(UID == 0, "run validation as nonroot")
class Installation(unittest.TestCase):
    def test_production_daemon_and_admin_require_root(self):
        for binary in ("arootd", "aroot-admin"):
            result = subprocess.run([str(HERE / binary)], capture_output=True)
            self.assertEqual(result.returncode, 125)
            self.assertIn(b"root", result.stderr)

    def test_version(self):
        result = subprocess.run([str(HERE / "aroot-client"), "--version"], capture_output=True)
        self.assertEqual(result.stdout, b"aroot 1.0.0\n")

    def test_staged_install_and_uninstall(self):
        with tempfile.TemporaryDirectory(prefix="aroot-stage-") as d:
            env = dict(os.environ, DESTDIR=d)
            subprocess.run(["bash", str(HERE / "aroot-install")], env=env, check=True, capture_output=True)
            root = Path(d)
            for name in ("usr/bin/aroot", "usr/libexec/arootd", "usr/libexec/aroot-client", "usr/sbin/aroot-admin"):
                self.assertEqual(stat.S_IMODE((root / name).stat().st_mode), 0o755)
                self.assertFalse((root / name).stat().st_mode & (stat.S_ISUID | stat.S_ISGID))
            self.assertIn(b"/usr/libexec/aroot-client", (root / "usr/bin/aroot").read_bytes())
            self.assertIn(b"KillMode=control-group", (root / "etc/systemd/system/aroot.service").read_bytes())
            for name in ("etc/aroot", "etc/aroot/users", "var/log/aroot"):
                self.assertEqual(stat.S_IMODE((root / name).stat().st_mode), 0o700)
            subprocess.run(["bash", str(HERE / "aroot-uninstall")], env=env, check=True, capture_output=True)
            self.assertFalse((root / "usr/bin/aroot").exists())
            self.assertTrue((root / "var/log/aroot").exists())

    def test_staging_refuses_real_root_aliases(self):
        for script in ("aroot-install", "aroot-uninstall"):
            for destination in ("/", "/tmp/.."):
                result = subprocess.run(["bash", str(HERE / script)], env=dict(os.environ, DESTDIR=destination), capture_output=True, timeout=3)
                self.assertEqual(result.returncode, 2)
                self.assertIn(b"unset DESTDIR", result.stderr)

    def test_make_staging(self):
        with tempfile.TemporaryDirectory(prefix="aroot make stage ") as d:
            subprocess.run(["make", "-C", str(HERE), f"DESTDIR={d}", "install"], capture_output=True, check=True)
            self.assertTrue((Path(d) / "usr/bin/aroot").exists())
            subprocess.run(["make", "-C", str(HERE), f"DESTDIR={d}", "uninstall"], capture_output=True, check=True)
            self.assertFalse((Path(d) / "usr/bin/aroot").exists())
            self.assertTrue((Path(d) / "var/log/aroot").exists())

    def test_test_daemon_needs_explicit_paths(self):
        result = subprocess.run([str(HERE / "arootd-test")], capture_output=True)
        self.assertEqual(result.returncode, 125)

    def test_unsafe_audit_prevents_startup(self):
        with tempfile.TemporaryDirectory(prefix="aroot-unsafe-") as d:
            root = Path(d)
            policy = root / "users"
            policy.mkdir(mode=0o700)
            audit = root / "audit"
            audit.symlink_to(root / "target")
            result = subprocess.run([str(HERE / "arootd-test"), "--test-paths", str(root / "sock"), str(policy), str(audit)], capture_output=True, timeout=3)
            self.assertEqual(result.returncode, 125)
            self.assertFalse((root / "sock").exists())
            self.assertFalse((root / "target").exists())


if __name__ == "__main__":
    unittest.main(verbosity=2)
