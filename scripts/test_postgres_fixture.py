"""Reject temporary socket-only PostgreSQL readiness before controller startup."""

import unittest

from postgres_fixture import wait_for_postgres, start_migrated_controllers


class PostgresReadinessTests(unittest.TestCase):
    def test_socket_ready_does_not_admit_controller_until_tcp_is_ready(self):
        probes = []
        sleeps = []

        def inner(node, *command):
            self.assertEqual(node, 'fixture-node')
            self.assertEqual(command[:3], ('exec', 'fixture-db', 'pg_isready'))
            probes.append(command)
            # A socket-only probe would incorrectly report success immediately.
            if '-h' not in command:
                return 'accepting connections'
            self.assertEqual(command[command.index('-h') + 1], '127.0.0.1')
            if len(probes) < 3:
                raise RuntimeError('temporary init server has no TCP listener')
            return 'accepting connections'

        wait_for_postgres(inner, 'fixture-node', attempts=3, sleep=sleeps.append)
        self.assertEqual(len(probes), 3)
        self.assertEqual(sleeps, [1, 1])

    def test_exhausted_readiness_fails_without_leaking_private_diagnostics(self):
        probes = []
        sleeps = []

        def unavailable(*command):
            probes.append(command)
            raise RuntimeError('private-fixture-diagnostic')

        with self.assertRaisesRegex(RuntimeError, 'TCP readiness timed out') as raised:
            wait_for_postgres(unavailable, 'fixture-node', attempts=2, sleep=sleeps.append)
        self.assertNotIn('private-fixture-diagnostic', str(raised.exception))
        self.assertEqual(len(probes), 2)
        self.assertEqual(sleeps, [1])


class ControllerStartupTests(unittest.TestCase):
    def test_migration_finishes_before_any_controller_starts(self):
        calls = []
        start_migrated_controllers(lambda *args: calls.append(args), ['manager', 'worker'], 'fixture-image')
        self.assertEqual(len(calls), 3)
        self.assertEqual(calls[0][0], 'manager')
        self.assertEqual(calls[0][-4:], ('fixture-image', 'migrate', '--config', '/etc/nsc/controller.yaml'))
        self.assertIn('--rm', calls[0])
        self.assertEqual([call[0] for call in calls[1:]], ['manager', 'worker'])
        self.assertTrue(all('fixture-controller' in call for call in calls[1:]))
        self.assertTrue(all('migrate' not in call for call in calls[1:]))

    def test_failed_migration_prevents_all_controller_startups(self):
        calls = []

        def fail(*args):
            calls.append(args)
            raise RuntimeError('fixture migration failed')

        with self.assertRaisesRegex(RuntimeError, 'migration failed'):
            start_migrated_controllers(fail, ['manager', 'worker'], 'fixture-image')
        self.assertEqual(len(calls), 1)
        self.assertIn('migrate', calls[0])


if __name__ == '__main__':
    unittest.main()
