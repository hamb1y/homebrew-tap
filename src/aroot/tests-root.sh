#!/bin/bash
# Production acceptance ONLY on a fresh disposable GitHub-hosted Ubuntu runner.
# Never call this from make test or on a developer workstation.
set -euo pipefail
umask 077

refuse() { printf 'aroot root acceptance refused: %s\n' "$1" >&2; exit 2; }
[[ $# == 0 && $EUID == 0 ]] || refuse 'requires an explicit root invocation with no arguments'
[[ ${AROOT_EPHEMERAL_ROOT_TEST:-} == 1 && ${GITHUB_ACTIONS:-} == true && ${RUNNER_OS:-} == Linux && ${RUNNER_ENVIRONMENT:-} == github-hosted ]] || refuse 'requires explicit opt-in on a GitHub-hosted Linux runner'
# shellcheck disable=SC1091
source /etc/os-release
[[ $ID == ubuntu && -d /run/systemd/system && $(ps -p 1 -o comm=) == systemd ]] || refuse 'requires Ubuntu with systemd as PID 1'
[[ ${SUDO_UID:-} =~ ^[0-9]+$ && ${SUDO_UID:-0} != 0 && -n ${SUDO_USER:-} && $SUDO_USER != root ]] || refuse 'invoke using sudo from the original unprivileged runner account'
test_uid=$SUDO_UID
test_user=$SUDO_USER
[[ $(id -u "$test_user") == "$test_uid" ]] || refuse 'original runner UID does not match its account'
test_gid=$(id -g "$test_user")
test_home=$(getent passwd "$test_uid" | cut -d: -f6)
[[ $test_home == /home/* && -d $test_home && ! -L $test_home ]] || refuse 'requires a regular runner home below /home'
source_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
for binary in arootd aroot-client aroot-admin; do
  [[ -x "$source_dir/$binary" ]] || refuse 'run make test as the runner account first'
done
for path in /usr/bin/aroot /usr/libexec/arootd /usr/libexec/aroot-client /usr/sbin/aroot-admin /etc/systemd/system/aroot.service /etc/aroot /var/log/aroot /run/aroot; do
  [[ ! -e $path && ! -L $path ]] || refuse "pre-existing aroot path: $path"
done
if getent group aroot >/dev/null; then refuse 'pre-existing aroot group'; fi
if systemctl cat aroot.service >/dev/null 2>&1; then refuse 'pre-existing aroot service unit'; fi
[[ -z ${DESTDIR:-} ]] || refuse 'DESTDIR must be unset'

install_started=0
token_dir=
cleanup() {
  local result=$? cleanup_failed=0
  trap - EXIT INT TERM
  set +e
  if (( result != 0 && install_started )); then journalctl -u aroot.service --no-pager -n 40; fi
  if (( install_started )); then
    if [[ -f /etc/systemd/system/aroot.service ]]; then
      timeout --kill-after=2s 20s bash "$source_dir/aroot-uninstall" || cleanup_failed=1
    fi
    # Report uninstall defects and still stop any remaining service on this VM.
    if systemctl is-active --quiet aroot.service; then
      cleanup_failed=1
      timeout --kill-after=2s 15s systemctl stop aroot.service
    fi
    if systemctl is-active --quiet aroot.service || systemctl is-enabled --quiet aroot.service 2>/dev/null; then cleanup_failed=1; fi
    for path in /usr/bin/aroot /usr/libexec/arootd /usr/libexec/aroot-client /usr/sbin/aroot-admin /etc/systemd/system/aroot.service; do
      [[ ! -e $path && ! -L $path ]] || cleanup_failed=1
    done
    if ! systemctl is-active --quiet aroot.service; then
      # Every path/group below was absent before this test. Never remove pre-existing state.
      rm -f -- /usr/bin/aroot /usr/libexec/arootd /usr/libexec/aroot-client /usr/sbin/aroot-admin /etc/systemd/system/aroot.service
      rm -rf -- /etc/aroot /var/log/aroot /run/aroot
      systemctl daemon-reload || cleanup_failed=1
      if getent group aroot >/dev/null; then
        gpasswd -d "$test_user" aroot >/dev/null 2>&1 || true
        groupdel aroot || cleanup_failed=1
      fi
    fi
  fi
  [[ -z $token_dir ]] || rm -rf -- "$token_dir"
  if (( cleanup_failed )); then printf 'aroot production cleanup failed\n' >&2; result=1; fi
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
as_user() { timeout --kill-after=2s 10s runuser -u "$test_user" -- "$@"; }
expect_status() {
  local expected=$1 actual
  shift
  if "$@"; then actual=0; else actual=$?; fi
  [[ $actual == "$expected" ]] || { printf 'Expected status %s, received %s\n' "$expected" "$actual" >&2; return 1; }
}

token_dir=$(mktemp -d "$test_home/.aroot-root-ci.XXXXXX")
chown "$test_uid:$test_gid" "$token_dir"
token_file="$token_dir/token"
wrong_token="$token_dir/wrong-token"
install_started=1
bash "$source_dir/aroot-install"
/usr/sbin/aroot-admin grant "$test_uid" "$token_file"
usermod -aG aroot "$test_user"
# runuser initializes a fresh group list instead of reusing the runner's old login.
[[ $(as_user /usr/bin/id -u) == "$test_uid" ]]
as_user /usr/bin/id -nG | tr ' ' '\n' | /usr/bin/grep -qx aroot
systemctl enable --now aroot.service
systemctl is-enabled --quiet aroot.service
systemctl is-active --quiet aroot.service
for ((attempt = 0; attempt < 100; attempt++)); do
  [[ ! -S /run/aroot/control.sock ]] || break
  sleep 0.05
done
[[ -S /run/aroot/control.sock ]]
# Type=simple reports process startup; allow the just-created socket to enter listen().
sleep 0.05
[[ $(as_user /usr/bin/aroot --token-file "$token_file" /usr/bin/id -u) == 0 ]]
[[ $(as_user /usr/bin/aroot --token-file "$token_file" /usr/bin/id -g) == 0 ]]
[[ $(as_user /usr/bin/aroot --token-file "$token_file" /usr/bin/id -G) == 0 ]]
[[ $(as_user /usr/bin/aroot --token-file "$token_file" /bin/pwd) == / ]]
printf 'production stdin\000\377\n' >"$token_dir/input"
cat "$token_dir/input" | as_user /usr/bin/aroot --token-file "$token_file" /bin/cat >"$token_dir/output"
cmp "$token_dir/input" "$token_dir/output"
as_user /usr/bin/aroot --token-file "$token_file" /bin/sh -c 'printf production-stderr >&2' 2>"$token_dir/stderr"
[[ $(cat "$token_dir/stderr") == production-stderr ]]
expect_status 37 as_user /usr/bin/aroot --token-file "$token_file" /bin/sh -c 'exit 37'

dd if=/dev/urandom of="$wrong_token" bs=32 count=1 status=none
chown "$test_uid:$test_gid" "$wrong_token"
[[ $(stat -c %a "$wrong_token") == 600 ]]
if cmp -s "$token_file" "$wrong_token"; then printf 'Unexpected random token collision\n' >&2; exit 1; fi
expect_status 126 as_user /usr/bin/aroot --token-file "$wrong_token" /bin/touch "$token_dir/wrong-must-not-run"
[[ ! -e $token_dir/wrong-must-not-run ]]
/usr/sbin/aroot-admin revoke "$test_uid"
[[ ! -e /etc/aroot/users/$test_uid ]]
expect_status 126 as_user /usr/bin/aroot --token-file "$token_file" /bin/touch "$token_dir/revoked-must-not-run"
[[ ! -e $token_dir/revoked-must-not-run ]]
systemctl is-active --quiet aroot.service
python3 - "$test_uid" "$token_file" <<'PY'
import json
import os
from pathlib import Path
import sys
uid = int(sys.argv[1])
raw = Path('/var/log/aroot/audit.jsonl').read_bytes()
token = Path(sys.argv[2]).read_bytes()
assert token not in raw and token.hex().encode() not in raw
records = [json.loads(line) for line in raw.splitlines()]
user = [record for record in records if record['uid'] == uid]
assert {'grant_request', 'grant', 'allow', 'start', 'finish', 'deny', 'revoke'} <= {record['event'] for record in user}
assert sum(record['event'] == 'deny' for record in user) >= 2
identity = [record for record in user if record['event'] == 'finish' and record['requested_executable'] == '/usr/bin/id']
assert len(identity) == 3
assert all(record['resolved_executable'] == os.path.realpath('/usr/bin/id') and record['status'] == 0 for record in identity)
assert any(record['event'] == 'finish' and record['requested_executable'] == '/bin/sh' and record['status'] == 37 for record in user)
assert all(record['requested_executable'] is None for record in user if record['event'] == 'deny')
assert all('args' not in record and 'token' not in record for record in records)
print('Production audit identity, denial and secret-exclusion checks passed')
PY
printf 'Production root acceptance passed: UID/GID/groups, cwd, binary stdio, stderr, exit, wrong token, revocation and audit.\n'
# EXIT trap tests the explicit uninstaller, verifies stopped/disabled/removed state,
# then removes only the test-created retained grants/logs/token directory/group.
