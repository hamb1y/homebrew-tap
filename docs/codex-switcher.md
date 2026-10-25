# codex-switcher 0.2.0

```sh
codex-switcher                 # Gum menu
codex-switcher save Work       # snapshot active file credentials
codex-switcher save Work --replace
codex-switcher list
codex-switcher switch work
codex-switcher login Personal  # isolated browser/device/API-key login
codex-switcher delete work     # move profile to private backups
```

Scripted save/switch/list/status/delete operations require Python 3 and Bash; only the interactive pickers need Gum. Login additionally needs the Codex CLI. IDs are lowercase forms of labels (spaces become hyphens). Empty labels, traversal IDs, control characters and symlink paths are rejected. Replacing an existing profile requires `--replace`; login never replaces an existing profile.

Codex home follows `CODEX_HOME`, defaulting to `~/.codex`. Legacy `CODEX_DIR` overrides that path; `CODEX_AUTH_FILE` can override the active auth file. `CODEX_SWITCHER_DIR` overrides the private profile store at `~/.codex-switcher`.

Use `cli_auth_credentials_store = "file"` in Codex `config.toml`. [Official OpenAI authentication documentation](https://learn.chatgpt.com/docs/auth) describes file, keyring, auto and ephemeral credential stores. This tool switches `auth.json` snapshots. OS credential stores, environment-supplied credentials and running Codex sessions can take precedence; restart Codex after switching and check `codex login status`. Managed authentication requirements still apply.

A new login runs in a temporary Codex home, with a copy of `config.toml` and file storage explicitly selected. It is saved as a profile without activating it. Failure, cancellation and normal completion remove that temporary home, leaving the active file untouched. Use `switch` to activate a saved login.

Profile files and backups are credentials. They are created with mode 0600 under directories with mode 0700, validated before use, and replaced atomically under a store lock. Unique backups retain overwritten active credentials and replaced/deleted profiles. Backups are not auto-pruned: remove old backups deliberately when no longer needed. Do not sync or commit the store.

Profiles are snapshots: Codex can refresh the active credentials during use. Save that account again with `--replace` before switching away if you want the refreshed snapshot retained. Close other Codex processes while switching; they do not share the switcher's lock and may rewrite active credentials.
