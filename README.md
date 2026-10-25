# hamb1y Homebrew tap

```sh
brew tap hamb1y/tap
brew install hamb1y/tap/router hamb1y/tap/codex-switcher hamb1y/tap/regi
```

- [router](docs/router.md): local OpenAI-compatible chat gateway with smart/dumb model classes, streaming and deterministic failover.
- [codex-switcher](docs/codex-switcher.md): private snapshots of file-based Codex credentials, with a Gum menu and scripted operations.
- [aroot](src/aroot/README.md): token-authorized privileged helper, installed and enabled explicitly by an administrator.
- [regi](https://github.com/hamb1y/regi): plaintext named registers.
- numtrack remains unchanged in this update.

## Migrating sudogranter to aroot

The sudogranter formula and Go HTTP runner have been retired. Existing installations and system services are not removed automatically. An administrator should stop the old service before removing its package:

```sh
sudo systemctl disable --now sudogranter.service
brew uninstall sudogranter
```

Inspect `/etc/systemd/system/sudogranter.service` before removing the obsolete unit, then run `sudo systemctl daemon-reload`. Preserve `/etc/sudogranter/runs` if its execution history is needed. The old bearer token is not an aroot token: do not reuse it. Delete old credentials only after migration and any retention needs are addressed.

Install aroot with `brew install hamb1y/tap/aroot`, then follow its [administrator installation instructions](src/aroot/README.md). Homebrew stages the binaries and installer; it does not grant access or start a privileged service. The administrator chooses users, issues fresh tokens and explicitly enables the boot service. Old HTTP scripts must be updated to the `aroot` command; the former HTTP API is not retained.

## Development

```sh
(cd src/router && go test -race ./...)
bash -n bin/codex-switcher
python3 -m unittest discover -s tests -v
```

Aroot has its own build and protocol tests in `src/aroot`. No tests need real Codex credentials, upstream API keys or a running root service.
