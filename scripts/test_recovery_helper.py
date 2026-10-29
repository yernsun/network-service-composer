"""Recovery helpers must be portable to the controller container's libc."""

import importlib.util
import os
from pathlib import Path
import tempfile
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('recovery_e2e', Path(__file__).with_name('e2e-recovery.py'))
recovery = importlib.util.module_from_spec(spec)
spec.loader.exec_module(recovery)


class HelperBuildTests(unittest.TestCase):
    def test_build_overrides_host_cgo_and_preserves_toolchain_environment(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            compiler = root / 'go-fixture'
            compiler.write_text('#!/bin/sh\nset -eu\ntest "$1" = build\ntest "$CGO_ENABLED" = 0\n'
                                'test "$GOPROXY" = off\nwhile test "$1" != -o; do shift; done\n'
                                'shift\nprintf portable > "$1"\n')
            compiler.chmod(0o700)
            fixture = recovery.RecoveryFixture(SimpleNamespace(output=str(root / 'output'), helper=None))
            with patch.dict(os.environ, {'GO': str(compiler), 'CGO_ENABLED': '1', 'GOPROXY': 'off'}):
                fixture.build_helper()
                self.assertEqual(os.environ['CGO_ENABLED'], '1')
            self.assertEqual(fixture.helper.read_text(), 'portable')

    def test_build_failure_does_not_publish_private_compiler_output(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            compiler = root / 'go-fixture'
            compiler.write_text('#!/bin/sh\nprintf private-build-diagnostic >&2\nexit 2\n')
            compiler.chmod(0o700)
            fixture = recovery.RecoveryFixture(SimpleNamespace(output=str(root / 'output'), helper=None))
            with patch.dict(os.environ, {'GO': str(compiler)}):
                with self.assertRaisesRegex(RuntimeError, 'exit 2') as raised:
                    fixture.build_helper()
            self.assertNotIn('private-build-diagnostic', str(raised.exception))
            self.assertFalse(fixture.helper.exists())
            diagnostic = fixture.output / 'command-1-error.log'
            self.assertEqual(diagnostic.read_text(), 'private-build-diagnostic')
            self.assertEqual(diagnostic.stat().st_mode & 0o777, 0o600)


if __name__ == '__main__':
    unittest.main()
