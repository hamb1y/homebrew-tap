# cc-switcher 0.2.0

Save and switch the Claude subscription login used by your existing `claude` command. Uses the same save/switch/login/list/status/delete workflow as codex-switcher. Settings, project trust, sessions, plugins and MCP credentials stay in place.

```sh
brew install hamb1y/tap/cc-switcher
cc-switcher save Work          # save the login already used by claude
cc-switcher login Personal     # capture another login without changing the active one (Linux)
cc-switcher list
cc-switcher switch personal    # install the saved login in Claude's normal credential store
claude                        # restart Claude after switching; no wrapper required
cc-switcher save Personal --replace
cc-switcher status
cc-switcher delete work        # move saved profile to private backups
cc-switcher                   # Gum menu
```

Requires Python 3. Only the interactive menu and pickers need Gum. Install Claude Code separately for `login`. IDs are lowercase labels, with spaces replaced by hyphens. Explicit save replacement requires `--replace`; login never replaces an existing profile.

On Linux, switches the `claudeAiOauth` entry in `~/.claude/.credentials.json`. On macOS, switches the corresponding entry in Claude's Keychain document. Matching `oauthAccount` metadata is merged into Claude's global config. Other credential entries and settings are preserved. `CLAUDE_CONFIG_DIR` and `CLAUDE_SECURESTORAGE_CONFIG_DIR` are honored. Legacy `.config.json` is recognized. Keychain behavior has mocked tests; real account switching has not been tested on macOS.

`login LABEL` uses a temporary configuration and secure-storage directory, captures a subscription login and removes the temporary directory on success, failure or cancellation. This isolated login capture currently supports Linux. On macOS, sign in with `claude auth login` and use `cc-switcher save LABEL` to capture it. Saved subscription logins can then be switched through Keychain. API-key, cloud-provider and keyless Console account switching are not supported.

Close Claude sessions before switching: already-running sessions retain credentials in memory and can rewrite them. Environment tokens, API keys and configured authentication helpers can override the saved subscription login. Check the active login with `claude auth status` after switching. The tool does not change a running session.

The profile store is `~/.cc-switcher`, or `CC_SWITCHER_DIR`. Directories are mode 0700; snapshots and backups are mode 0600. Replacements, deletions and overwritten active documents receive unique private backups. These contain credentials: do not sync or commit the store. Backups are not automatically pruned. Operations use a store lock and atomic file replacement. Metadata is restored if writing the credential document fails; the global config and credential store are separate writes, so keep other Claude processes closed during switching.

Profiles are snapshots. Claude can refresh active tokens: use `save LABEL --replace` before switching away to retain refreshed credentials. Existing 0.1.0 file-based profiles are read directly without deleting or moving them; `switch ID` now activates them for plain `claude`. The obsolete selected-profile marker is ignored. The `run` wrapper was removed.

```sh
python3 -m unittest discover -s tests -v
```

To install from source:

```sh
install -m 755 bin/cc-switcher ~/.local/bin/cc-switcher
```
