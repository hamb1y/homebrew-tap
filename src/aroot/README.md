# aroot 1.0

aroot is a Linux administrator-installed C service with a Bash command at `/usr/bin/aroot`. It runs authorized commands as root without asking sudo for each command. The administrator must first grant a local UID unrestricted root command authority and provision that UID's private token. The systemd service runs visibly on boot only after the administrator explicitly enables it. There are no setuid files, network listeners, sudo exploits, or automatic service activation.

A grant is equivalent to full root access: the user can run a shell, change the machine and its configuration, and remove local audit history. Grant only trusted administrators. The audit trail is local operational history, not tamper-proof evidence against an authorized root user.

## Build and stage

Linux, a C11 compiler, glibc with `getrandom`/`close_range` declarations, Bash and systemd are required for installation. Python 3 is used only by tests. On older kernels the descriptor closing code falls back when `close_range` is unavailable.

```sh
make
make test                     # run as a nonroot user on a host allowing Unix sockets
DESTDIR="$PWD/stage" ./aroot-install
# or: make DESTDIR="$PWD/stage" install
```

The staged tree contains ordinary 0755 programs and a systemd unit. Staging does not create groups, reload systemd, start services, or alter the running host. The test daemon is separately compiled, insists on a nonroot caller with explicit private temporary paths, and is never installed. The production daemon rejects nonroot execution and has no configurable alternate socket/policy/audit paths.

The Homebrew formula builds and packages these artifacts under its `share/aroot` directory. `brew install` alone does not install `/usr/bin/aroot` or change systemd.

## Explicit administrator installation

From a reviewed, built source checkout:

```sh
sudo ./aroot-install
```

Or after installing the Homebrew formula, run the exact `aroot-setup` path shown by `brew info aroot`. The installer copies root-owned programs into `/usr/libexec`, `/usr/sbin` and `/usr/bin`, installs `/etc/systemd/system/aroot.service`, creates private policy/audit directories and the `aroot` system group, and reloads systemd. It does **not** enable or start the service.

Provision a trusted local administrator (replace `alice` and her home path):

```sh
sudo -u alice mkdir -p /home/alice/.config/aroot
sudo -u alice chmod 700 /home/alice/.config/aroot
sudo /usr/sbin/aroot-admin grant "$(id -u alice)" /home/alice/.config/aroot/token
sudo usermod -aG aroot alice
sudo systemctl enable --now aroot.service
sudo systemctl --no-pager status aroot.service
```

Every directory component of the token path must be a real directory owned by root or the target UID and not writable by group/others. The grant creates a **new** file with `O_EXCL` and mode 0600 and transfers ownership to the user. It refuses symlinks and existing token/grant files. It generates 32 cryptographically random binary bytes with `getrandom`; the token is never printed. The root policy is `/etc/aroot/users/UID`, also 0600. Give each UID its own grant. A group member without a matching per-UID token is denied. Group membership takes effect after a new login.

Stop and remove an existing sudogranter service deliberately before migrating. Old sudogranter tokens/configuration are never accepted or imported by aroot.

## Run commands

```sh
aroot id
aroot /usr/bin/printf '%s\n' 'a literal argument with spaces'
printf 'input\n' | aroot /bin/cat >output.txt
aroot --token-file /home/alice/.config/aroot/token /bin/ls /root
# Read exactly 32 bytes from a private descriptor, never a token argument/environment variable:
aroot --token-fd 3 /usr/bin/id 3</home/alice/.config/aroot/token
```

The default token path comes from the invoking UID's account home directory, not `$HOME`. Token files must be regular, owned by the invoking UID, mode 0600, exactly 32 bytes, and have no hard links. `--token-fd` accepts descriptors 3 and above, closes the descriptor after reading and does not pass it into the command.

Arguments are passed directly to `execve`, with an explicit search of the fixed system PATH and no interpolation or implicit shell fallback for executable text files. To request a shell, explicitly invoke `/bin/sh -c ...` and quote it as you would any shell command. The caller's shell still handles redirects before `aroot`; use an explicit shell command for root-owned redirections.

Commands start in `/`, with umask 022, root UID/GID and no supplementary groups. The environment contains only `PATH=/usr/sbin:/usr/bin:/sbin:/bin`, `HOME=/root`, `USER=root`, `LOGNAME=root`, `LANG=C.UTF-8` and `TERM=dumb`. Relative paths resolve from `/`. Use absolute paths or explicitly request a shell `cd`. No current-directory or environment preservation is implied.

stdin/stdout/stderr are transferred as file descriptors over the Unix socket, preserving binary I/O, pipes, redirects and terminal reads. INT, QUIT, TERM, HUP and WINCH are forwarded to the command process group; exit status is returned, with `128 + signal` for a signal exit. Disconnecting the client terminates its command group (TERM followed by KILL after two seconds). Full-screen tools needing `/dev/tty`, a dedicated controlling PTY and interactive shell job control are not supported by this release.

Exit statuses 125 and 126 report service/audit/protocol failure and authorization denial respectively (executed programs can also return those codes). A missing executable returns 127. Service diagnostics go to systemd's journal; command output goes directly to the caller.

## Authentication, limits and audit

The socket is `/run/aroot/control.sock`, root-owned mode 0660 and group `aroot`, inside a root-owned 0750 runtime directory. The client verifies the server's peer UID is root before sending its credential. The service takes the caller UID from kernel `SO_PEERCRED`, never from a claimed UID in the request. It reads that UID's protected policy and compares all 32 token bytes. A token copied to a different local UID does not grant that UID access.

The binary protocol has a fixed versioned header, 32-byte token, exactly three `SCM_RIGHTS` descriptors, and a NUL-delimited argv payload limited to 128 arguments, 4096 bytes per argument, and 65536 bytes total. At most 32 request workers run concurrently; incomplete authentication/payload reads time out after five seconds. A root-owned lock prevents duplicate service instances. Unsupported/malformed requests deny execution.

Required JSON-lines audit is `/var/log/aroot/audit.jsonl`, a root-owned 0600 regular single-link file inside a private 0700 directory. Events include daemon start/stop, deny, allow, command start/finish and administrator grant/revoke. Each record contains time, UID, request/worker PID, event and result status, plus explicitly labeled `requested_executable` and `resolved_executable` fields. The requested field is argv[0]; the resolved field is a best-effort canonical executable path observed before execution using the fixed cwd/PATH. Resolution does not pin a file against subsequent filesystem changes. Identity is `null` when unavailable; unauthenticated requests never supply argv. Each executable field is bounded to 512 source bytes with an explicit truncation flag. Quotes, backslashes, control characters and non-ASCII filename bytes are JSON-escaped (non-ASCII bytes use `\u00xx`). Each record is serialized and fsynced. Arguments after argv[0], authentication token paths/bytes, environment and command output are excluded. The service does not guess which executable names might be secret; executable identity is always recorded when decoded, including malformed requests whose argv[0] was valid. A missing token rejected by the client never reaches the daemon and therefore has no server audit event.

Audit is opened/validated at startup and for each request. Unsafe/unwritable/unavailable required audit fails closed before a command is created. An audit failure after an already running command finishes cannot undo its effects; the client receives 125. A successful command audit start means execution was attempted; an exec error is recorded by its finish status. For rotation, stop the service, move the audit file, and restart. Keep the directory mode 0700; the new file is created mode 0600. Do not log tokens with shell tracing, command arguments or environment variables.

## Revoke, rotate, stop and uninstall

```sh
sudo /usr/sbin/aroot-admin revoke "$(id -u alice)"
```

Revocation removes the policy immediately for new requests. Requests already authenticated and running commands continue. To stop active service commands as well, run `sudo systemctl stop aroot.service`; its `KillMode=control-group` terminates the daemon and its command descendants. An administrator with unrestricted root authority can create independent services, so revocation is not a way to contain a hostile root user.

For rotation: revoke, remove the user's old token file, and grant again to a new private file. To withdraw socket access too, remove the user from `aroot` with `gpasswd -d alice aroot`; existing login sessions retain their supplementary groups until logout, while policy revocation already blocks new commands.

```sh
sudo ./aroot-uninstall
# Homebrew package: use the exact aroot-uninstall path from its bin directory.
# Reversible staging check:
DESTDIR="$PWD/stage" ./aroot-uninstall
```

Uninstall disables/stops the service, removes installed programs/unit and reloads systemd. It preserves policy files, audit history, per-user token files and the system group for explicit administrator cleanup. Do not delete the Homebrew package before using its uninstaller. Remove retained `/etc/aroot`, `/var/log/aroot`, tokens and group memberships only when no longer needed. Reinstalling with retained grants re-enables those grants when the administrator starts the service; revoke them first if that is not intended.

## Validation status

`make test` contains native parsing/auth-file tests plus real Unix-socket integration tests and staged installation checks. It exercises correct/wrong/revoked tokens, peer-UID policy selection, malformed requests, binary stdio, clean environment/cwd, signal and exit forwarding, disconnect cleanup, concurrent audit, audit secrecy and fail-closed audit failures. It requires Unix sockets and an unprivileged user. Running as root is rejected by the test daemon; actual root installation/boot activation requires a separate administrator acceptance test on a disposable Linux system.


The Linux CI job also invokes `tests-root.sh` on a disposable GitHub-hosted Ubuntu VM after the unprivileged suite. This separate production acceptance step installs and explicitly enables/starts the real root service, provisions the original runner UID, runs the installed Bash command as that UID with a fresh group list, verifies root identity/groups, binary stdio, exit status, wrong-token denial, revocation and executable audit, then stops/uninstalls and removes only its test-created state in an EXIT trap. It requires explicit environment opt-in, GitHub-hosted runner metadata, Ubuntu/systemd and a fresh host with no aroot installation/group. It is never called by `make test` and must never run on a developer workstation. Run this only through the checked-in GitHub workflow; do not spoof its environment gates to run it elsewhere.
