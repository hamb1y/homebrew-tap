import concurrent.futures
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / 'bin/codex-switcher'


class SwitcherTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.codex = self.root / 'custom-codex'
        self.codex.mkdir()
        self.auth = self.codex / 'auth.json'
        self.write_auth('first')
        self.store = self.root / 'profiles'
        self.env = dict(os.environ, HOME=str(self.root), CODEX_HOME=str(self.codex),
                        CODEX_SWITCHER_DIR=str(self.store))
        for name in ('CODEX_DIR', 'CODEX_AUTH_FILE'):
            self.env.pop(name, None)

    def write_auth(self, value):
        self.auth.write_text(json.dumps({'OPENAI_API_KEY': value}))

    def run_cli(self, *args, ok=True):
        result = subprocess.run([str(SCRIPT), *args], env=self.env, text=True,
                                capture_output=True, timeout=15)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)
        return result

    def test_save_switch_backup_permissions_and_delete(self):
        self.run_cli('save', 'Work account')
        self.write_auth('second')
        self.run_cli('save', 'Personal')
        self.run_cli('switch', 'work-account')
        self.assertEqual(json.loads(self.auth.read_text())['OPENAI_API_KEY'], 'first')
        self.run_cli('switch', 'personal')
        self.assertEqual(len(list((self.store / 'backups').glob('auth.*.json'))), 2)
        self.assertIn('yes\tPersonal\tpersonal', self.run_cli('list').stdout)
        for path in (self.auth, self.store / 'accounts/work-account/auth.json'):
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
        self.assertEqual(stat.S_IMODE(self.store.stat().st_mode), 0o700)
        self.run_cli('delete', 'personal')
        self.assertFalse((self.store / 'accounts/personal').exists())
        self.assertEqual(json.loads(self.auth.read_text())['OPENAI_API_KEY'], 'second')

    def test_replacement_requires_flag(self):
        self.run_cli('save', 'Work')
        self.write_auth('second')
        self.run_cli('save', 'Work', ok=False)
        self.run_cli('save', 'Work', '--replace')
        self.assertEqual(len(list((self.store/'backups').iterdir())), 1)

    def test_bad_paths_labels_json_and_unknown_commands(self):
        for value in ('../other', '..', '/tmp', 'bad/name'):
            self.run_cli('switch', value, ok=False)
            self.run_cli('delete', value, ok=False)
        self.run_cli('save', '..', ok=False)
        self.run_cli('save', 'bad|label', ok=False)
        self.auth.write_text('{secret-invalid')
        result = self.run_cli('save', 'bad', ok=False)
        self.assertNotIn('secret-invalid', result.stderr)
        self.run_cli('unknown', ok=False)

    def test_symlink_does_not_overwrite_target(self):
        self.run_cli('save', 'Work')
        original = self.auth.read_bytes()
        self.auth.unlink()
        target = self.root/'do-not-touch'
        target.write_bytes(original)
        self.auth.symlink_to(target)
        self.run_cli('switch', 'work', ok=False)
        self.assertEqual(target.read_bytes(), original)
        (self.store/'accounts/evil').symlink_to(self.codex, target_is_directory=True)
        self.run_cli('switch', 'evil', ok=False)

    def test_switch_recovers_from_malformed_active_credentials(self):
        self.run_cli('save', 'Work')
        self.auth.write_text('malformed')
        self.run_cli('switch', 'work')
        self.assertEqual(json.loads(self.auth.read_text())['OPENAI_API_KEY'], 'first')
        self.assertEqual(next((self.store/'backups').iterdir()).read_text(), 'malformed')

    def test_non_string_access_token_is_rejected(self):
        self.auth.write_text(json.dumps({'tokens': {'access_token': {'bad': 'value'}}}))
        self.run_cli('save', 'bad', ok=False)

    def test_parallel_save_is_serialized(self):
        with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
            calls = list(pool.map(lambda _: subprocess.run([str(SCRIPT), 'save', 'race'], env=self.env,
                                      capture_output=True, timeout=15).returncode, range(8)))
        self.assertEqual(calls.count(0), 1)
        self.assertTrue(json.loads((self.store/'accounts/race/auth.json').read_text()))

    def mock_login(self, fail=False, cancel=False):
        bindir = self.root/'bin'; bindir.mkdir()
        gum = bindir/'gum'
        gum.write_text('#!/bin/sh\n' + ('exit 130\n' if cancel else "printf 'Browser\\n'\n"))
        codex = bindir/'codex'
        codex.write_text('''#!/usr/bin/env python3
import json, os, pathlib, sys
assert sys.argv[1:3] == ['-c', 'cli_auth_credentials_store="file"']
assert pathlib.Path(os.environ['CODEX_HOME']).name.startswith('login.')
(pathlib.Path(os.environ['CODEX_HOME'])/'auth.json').write_text(json.dumps({'OPENAI_API_KEY':'new-login'}))
''' + ('sys.exit(1)\n' if fail else ''))
        gum.chmod(0o755); codex.chmod(0o755)
        self.env['PATH'] = str(bindir)+os.pathsep+self.env['PATH']

    def test_failed_login_keeps_active_auth(self):
        self.mock_login(fail=True)
        original = self.auth.read_bytes()
        self.run_cli('login', 'new', ok=False)
        self.assertEqual(self.auth.read_bytes(), original)
        self.assertEqual(list(self.store.glob('login.*')), [])
        self.assertFalse((self.store/'accounts/new').exists())

    def test_cancel_keeps_active_auth(self):
        self.mock_login(cancel=True)
        original = self.auth.read_bytes()
        self.run_cli('login', 'new')
        self.assertEqual(self.auth.read_bytes(), original)
        self.assertFalse(self.store.exists())

    def test_successful_login_is_saved_but_not_activated(self):
        self.mock_login()
        original = self.auth.read_bytes()
        self.run_cli('login', 'new')
        self.assertEqual(self.auth.read_bytes(), original)
        self.assertEqual(list(self.store.glob('login.*')), [])
        self.assertEqual(json.loads((self.store/'accounts/new/auth.json').read_text())['OPENAI_API_KEY'], 'new-login')


if __name__ == '__main__':
    unittest.main()
