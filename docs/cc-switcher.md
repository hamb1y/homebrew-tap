# cc-switcher 0.1.0

Named Claude Code accounts with a Gum menu and scripted commands. Each account uses its own stable `CLAUDE_CONFIG_DIR`, so Claude manages login persistence and refreshed credentials. Existing `~/.claude` configuration is left untouched.

Install from the Homebrew tap once published:

```sh
brew install hamb1y/tap/cc-switcher
```

Or install the script locally:

```sh
install -m 755 bin/cc-switcher ~/.local/bin/cc-switcher
cc-switcher login Work
cc-switcher login Personal
cc-switcher switch work
cc-switcher run
cc-switcher run -- --model sonnet "Explain this project"
cc-switcher list
cc-switcher status
cc-switcher delete personal
cc-switcher                 # interactive Gum menu
```

Requires Python 3 on Linux or macOS, and a separately installed Claude Code. Only the interactive menu needs Gum. IDs are lowercase labels with spaces converted to hyphens. `login` uses `claude auth login --claudeai` and does not select the new account. Existing profiles cannot be overwritten. Use `run --` before Claude flags. Claude's exit status is returned.

`switch` selects the default account for subsequent `cc-switcher run` commands. It does not change plain `claude`, an existing session, or your shell environment. Each account has separate settings, MCP configuration, plugins and session history. User settings are not copied automatically. Managed Claude policies continue to apply.

The private store defaults to `~/.cc-switcher`; override with `CC_SWITCHER_DIR`. Profile directories have mode 0700; switcher metadata has mode 0600. Credential storage within a configuration directory remains Claude's responsibility, including macOS Keychain storage. This implementation was tested on Linux with a fake CLI, not with real accounts or macOS Keychain.

Credentials supplied through environment variables can override a selected account. `run` warns about common overrides without printing their values. Login also inherits the environment; unset external authentication variables before login. Existing file credentials are not imported. Log in separately for each profile.

Deleted profiles and failed logins move to unique private backups. They may contain credentials and session history; do not commit or sync the store. Backups are not automatically pruned. Moving a profile does not revoke its server tokens or remove a macOS Keychain entry. Use Claude's logout in the original profile before deletion when revocation is needed. Do not move or rename active profile directories, particularly on macOS, where the configuration path may determine the credential-store identity.

Selection and metadata changes are locked and atomic. Deletion is refused for the selected profile or one launched through this tool that is still running. Processes launched outside the tool do not participate in its locks. Separate configuration directories do not isolate the newer keyless Claude Console sign-in; this tool's login command targets claude.ai subscriptions.

Claude's documented multi-account mechanism: https://code.claude.com/docs/en/authentication#log-in-with-multiple-accounts

Run tests without real credentials:

```sh
python3 -m unittest discover -s tests -v
```
