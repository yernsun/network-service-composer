#!/usr/bin/env python3
"""Backup trust and protected-configuration boundaries without Docker access."""

import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('recovery_drill', Path(__file__).with_name('recovery-drill.py'))
drill = importlib.util.module_from_spec(spec)
spec.loader.exec_module(drill)


class BackupTests(unittest.TestCase):
    def test_archive_checksum_and_symlink_fail_before_restore(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / 'backup'
            archive.write_bytes(b'consistent fixture archive')
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            drill.validate_archive(archive, digest)
            archive.write_bytes(b'truncated archive')
            with self.assertRaisesRegex(ValueError, 'checksum'):
                drill.validate_archive(archive, digest)
            link = Path(directory) / 'link'
            link.symlink_to(archive)
            with self.assertRaisesRegex(ValueError, 'regular file'):
                drill.validate_archive(link, digest)

    def test_malformed_or_missing_checksum_cannot_start_restore(self):
        with tempfile.TemporaryDirectory() as directory:
            archive = Path(directory) / 'backup'
            archive.write_bytes(b'archive')
            for checksum in ('', 'not-a-checksum', 'A' * 64):
                with self.subTest(checksum=checksum):
                    with self.assertRaisesRegex(ValueError, 'SHA-256'):
                        drill.validate_archive(archive, checksum)

    def test_original_configuration_is_preserved_and_target_cannot_auto_run(self):
        source = {'storage': {'driver': 'postgres', 'dsn': 'source', 'auto_migrate': True},
                  'controller': {'recovery_mode': False}, 'management': {'enabled': True},
                  'security': {'admin_token': 'fixture-original', 'admin_token_file': '/run/secrets/original'}}
        for driver in ('postgres', 'mysql', 'sqlite'):
            with self.subTest(driver=driver):
                target = drill.recovery_config(source, driver, 'isolated-database', 'fixture-only')
                self.assertTrue(target['controller']['recovery_mode'])
                self.assertFalse(target['storage']['auto_migrate'])
                self.assertFalse(target['management']['enabled'])
                self.assertNotEqual(target['security']['admin_token'], 'fixture-original')
                self.assertEqual(target['security']['admin_token_file'], '')
                self.assertNotEqual(target['storage']['dsn'], 'source')
        self.assertTrue(source['storage']['auto_migrate'])
        self.assertTrue(source['management']['enabled'])
        self.assertEqual(source['security']['admin_token'], 'fixture-original')


if __name__ == '__main__':
    unittest.main()
