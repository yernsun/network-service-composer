import copy
import hashlib
import json
import os
from pathlib import Path
import tempfile
import time
import unittest
from types import SimpleNamespace
from unittest.mock import patch

import swarm_rollout as rollout


OLD = 'example.test/nsc@sha256:' + 'a' * 64
NEW = 'example.test/nsc@sha256:' + 'b' * 64


def service(component):
    policy = {'Parallelism': 1, 'Order': 'stop-first', 'FailureAction': 'pause',
              'Monitor': 30_000_000_000, 'Delay': 30_000_000_000}
    health = (['CMD', '/usr/local/bin/nsc-agent', '--healthcheck', '--config', '/etc/nsc/agent.yaml'] if component == 'gateway'
              else ['CMD', 'wget', '-q', '-O', '/dev/null', 'http://127.0.0.1:8080/readyz'])
    return {'ID': component, 'Version': {'Index': 10}, 'Spec': {
        'Name': component, 'Mode': {'Global': {}}, 'UpdateConfig': policy, 'RollbackConfig': copy.deepcopy(policy),
        'TaskTemplate': {'Placement': {'Constraints': ['node.labels.nsc.' + component + ' == true']},
                         'ContainerSpec': {'Image': OLD, 'StopGracePeriod': 30_000_000_000,
                                           'Healthcheck': {'Test': health}, 'Env': ['SECRET=not-for-reports'],
                                           'Mounts': [{'Source': '/node/state', 'Target': '/var/lib/nsc'}]}},
        'EndpointSpec': {'Ports': [{'TargetPort': port, 'Protocol': protocol, 'PublishMode': 'ingress'}
                                   for port, protocol in ((80, 'tcp'), (443, 'tcp'), (443, 'udp'))]}}}


class Engine:
    def __init__(self):
        self.services = {c: service(c) for c in ('controller', 'gateway')}
        self.updates = []
        self.paused = False
        self.ambiguous = False
        self.failed_nodes = set()
        self.concurrent = False

    def call(self, *args):
        return {'Swarm': {'ControlAvailable': True}}

    def service(self, name):
        return copy.deepcopy(self.services[name])

    def nodes(self):
        return [{'ID': n, 'Spec': {'Role': 'manager', 'Availability': 'active',
                                 'Labels': {'nsc.controller': 'true', 'nsc.gateway': 'true'}},
                 'Status': {'State': 'ready'}} for n in ('n1', 'n2', 'n3')]

    def tasks(self, name):
        return [{'ID': name + n, 'NodeID': n, 'DesiredState': 'running',
                 'Status': {'State': 'failed' if n in self.failed_nodes and name == 'gateway' else 'running'},
                 'Spec': {'ContainerSpec': {'Image': rollout.image_of(self.services[name])}}} for n in ('n1', 'n2', 'n3')]

    def update(self, before, spec, rollback=False):
        value = self.services[before['ID']]
        if self.concurrent:
            value['Version']['Index'] += 1
        if before['Version'] != value['Version']:
            raise rollout.Failure('rollout.concurrent_update')
        target = copy.deepcopy(value['PreviousSpec'] if rollback else spec)
        value['PreviousSpec'] = value['Spec']
        value['Spec'] = target
        value['Version']['Index'] += 1
        value['UpdateStatus'] = {'State': 'paused' if self.paused else 'rollback_completed' if rollback else 'completed'}
        self.updates.append((before['ID'], copy.deepcopy(target)))
        if self.ambiguous:
            raise rollout.Failure('rollout.engine_unavailable', unknown=True)


class RolloutTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        root = Path(self.temporary.name)
        token = root / 'token'
        token.write_text('secret-token')
        self.cfg = {'controller_service': 'controller', 'gateway_service': 'gateway', 'api_url': 'https://controller.test',
                    'token_file': str(token), 'gateways': [{'node_id': n, 'gateway_id': 'gw-' + n, 'pool_id': 'pool'}
                                                         for n in ('n1', 'n2', 'n3')],
                    'probes': [{'node_id': n, 'url': scheme + '://business.test/', 'body_sha256': hashlib.sha256(b'ok').hexdigest()}
                               for n in ('n1', 'n2', 'n3') for scheme in ('http', 'https')]}
        self.journal = rollout.Journal(root / 'report')
        self.addCleanup(self.journal.close)
        self.engine = Engine()
        self.runner = rollout.Rollout(self.cfg, self.journal, timeout=0.01, engine=self.engine)
        self.fetch = patch.object(rollout, 'fetch', return_value=(200, b'ok')).start()
        self.addCleanup(patch.stopall)
        self.runner.api = self.api

    def api(self, path):
        identity = path.rsplit('/', 1)[1]
        return {'id': identity, 'pool_id': 'pool', 'enabled': True, 'status': 'READY', 'desired_config_revision': 7,
                'applied_config_revision': 7, 'model_digest': 'a' * 64, 'rendered_digest': 'b' * 64,
                'evidence': {'confirmed_revision': 7, 'last_confirmed_revision': 7, 'loading_confirmed': True,
                             'startup_state': 'CONFIRMED', 'configuration_state': 'CONFIRMED',
                             'model_digest': 'a' * 64, 'rendered_digest': 'b' * 64, 'disk_digest': 'b' * 64,
                             'observed_at': rollout.stamp()}}

    def fails(self, code, action):
        with self.assertRaisesRegex(rollout.Failure, code):
            action()

    def test_image_only_order_and_evidence(self):
        before = copy.deepcopy(self.engine.services)
        self.runner.upgrade(NEW, NEW)
        self.assertEqual([c for c, _ in self.engine.updates], ['controller', 'gateway'])
        for component, spec in self.engine.updates:
            expected = before[component]['Spec']
            expected['TaskTemplate']['ContainerSpec']['Image'] = NEW
            self.assertEqual(spec, expected)
        self.assertTrue(self.runner.status())
        self.assertTrue(all(op['status'] == 'SUCCEEDED' and len(op['evidence']) == 3 for op in self.journal.data['operations']))
        report = self.journal.path.read_text()
        self.assertNotIn('secret-token', report)
        self.assertNotIn('not-for-reports', report)
        self.assertEqual(self.journal.path.stat().st_mode & 0o777, 0o600)

    def test_failure_pauses_without_advancing_gateway(self):
        self.engine.paused = True
        self.fails('rollout.paused', lambda: self.runner.upgrade(NEW, NEW))
        self.assertEqual([c for c, _ in self.engine.updates], ['controller'])
        self.assertEqual(self.journal.data['operations'][0]['status'], 'FAILED')

    def test_ambiguous_write_can_be_reconciled(self):
        self.engine.ambiguous = True
        self.fails('rollout.engine_unavailable', lambda: self.runner.upgrade(NEW, NEW))
        self.assertEqual(self.journal.data['operations'][0]['status'], 'UNKNOWN')
        self.assertFalse(self.runner.status())  # Gateway phase was never submitted.
        self.assertEqual(self.journal.data['operations'][0]['status'], 'SUCCEEDED')
        self.assertEqual(len(self.engine.updates), 1)

    def test_stale_version_cannot_write(self):
        self.engine.concurrent = True
        self.fails('rollout.concurrent_update', lambda: self.runner.upgrade(NEW, NEW))
        self.assertEqual(self.engine.updates, [])

    def test_preflight_rejects_unready_capacity_and_no_mutations(self):
        self.engine.failed_nodes = {'n1', 'n2'}
        self.fails('rollout.tasks_not_ready', self.runner.check)
        self.fails('rollout.insufficient_healthy_gateways', lambda: self.runner.check(rollback=True))
        self.assertEqual(self.engine.updates, [])

    def test_rollback_matches_previous_spec_and_retains_history(self):
        self.runner.upgrade(NEW, NEW)
        self.engine.failed_nodes = {'n1'}
        self.engine.services['gateway']['UpdateStatus'] = {'State': 'paused'}
        self.runner.wait = lambda op: None  # Failed-node recovery is asynchronous.
        self.runner.rollback('gateway')
        self.assertEqual(rollout.image_of(self.engine.services['gateway']), OLD)
        self.assertEqual(len(self.journal.data['operations']), 3)
        self.assertEqual(self.journal.data['operations'][-1]['action'], 'rollback')

    def test_rollback_refuses_foreign_changes(self):
        self.runner.upgrade(NEW, NEW)
        self.engine.services['gateway']['PreviousSpec']['TaskTemplate']['ContainerSpec']['Env'] = ['foreign=true']
        self.fails('rollout.rollback_conflict', lambda: self.runner.rollback('gateway'))
        self.assertEqual(len(self.engine.updates), 2)

    def test_status_does_not_accept_replaced_service_or_changed_spec(self):
        self.runner.upgrade(NEW, NEW)
        self.engine.services['gateway']['Spec']['TaskTemplate']['ContainerSpec']['Env'].append('foreign=true')
        self.fails('rollout.concurrent_update', self.runner.status)

    def test_timeout_is_unknown(self):
        self.runner.observe = lambda op: False
        self.runner.timeout = 0
        self.fails('rollout.deadline_exceeded', lambda: self.runner.change('gateway', self.engine.service('gateway'), NEW))
        self.assertEqual(self.journal.data['operations'][0]['status'], 'UNKNOWN')

    def test_policy_and_topology_fail_closed(self):
        for field, value in [('Order', 'start-first'), ('Parallelism', 0), ('FailureAction', 'rollback'), ('Monitor', 0)]:
            invalid = service('gateway')
            invalid['Spec']['UpdateConfig'][field] = value
            self.fails('rollout.policy_unsafe', lambda: rollout.validate_policy(invalid, 'gateway'))
        invalid = service('gateway')
        invalid['Spec']['TaskTemplate']['Placement']['Constraints'] = ['engine.labels.region == west']
        self.fails('rollout.constraint_unsupported', lambda: rollout.selected_nodes(invalid, self.engine.nodes()))
        invalid['Spec']['TaskTemplate']['Placement']['Constraints'] = ['node.id == n1']
        self.assertEqual(rollout.selected_nodes(invalid, self.engine.nodes()), {'n1'})

    def test_health_evidence_requires_fresh_complete_matching_tuple(self):
        value = self.api('/gw-n1')
        binding = self.cfg['gateways'][0]
        self.assertTrue(rollout.gateway_healthy(value, binding))
        for field, invalid in [('disk_digest', 'c' * 64), ('loading_confirmed', False), ('observed_at', '2000-01-01T00:00:00Z'),
                               ('confirmed_revision', 6), ('startup_state', 'UNKNOWN')]:
            changed = copy.deepcopy(value)
            changed['evidence'][field] = invalid
            self.assertFalse(rollout.gateway_healthy(changed, binding))
        value['pool_id'] = 'foreign'
        self.assertFalse(rollout.gateway_healthy(value, binding))

    def test_probe_failure_is_retained_without_body_or_url(self):
        self.fetch.return_value = (503, b'sensitive backend detail')
        self.fails('rollout.business_probe_failed', self.runner.check)
        log = (self.journal.directory / 'requests.jsonl').read_text()
        self.assertIn('"ok": false', log)
        self.assertNotIn('sensitive', log)
        self.assertNotIn('business.test', log)

    def test_report_lock_and_configuration_binding(self):
        self.fails('rollout.report_busy', lambda: rollout.Journal(self.journal.directory))
        changed = copy.deepcopy(self.cfg)
        changed['gateway_service'] = 'foreign'
        self.fails('rollout.report_configuration_mismatch', lambda: rollout.Rollout(changed, self.journal, engine=self.engine))

    def test_unknown_is_not_retried(self):
        self.engine.ambiguous = True
        self.fails('rollout.engine_unavailable', lambda: self.runner.upgrade(NEW, NEW))
        self.assertEqual(len(self.engine.updates), 1)
        self.assertEqual(self.journal.data['operations'][0]['reason'], 'rollout.engine_unavailable')

    def test_noop_upgrade_status_still_verifies_requested_images(self):
        self.runner.upgrade(OLD, OLD)
        self.assertEqual(self.engine.updates, [])
        self.assertTrue(self.runner.status())
        self.engine.services['gateway']['Spec']['TaskTemplate']['ContainerSpec']['Image'] = NEW
        self.assertFalse(self.runner.status())

    def test_unsubmitted_write_stays_unknown(self):
        def unavailable(*args):
            raise rollout.Failure('rollout.engine_unavailable', unknown=True)
        self.engine.update = unavailable
        self.fails('rollout.engine_unavailable', lambda: self.runner.upgrade(NEW, NEW))
        with self.assertRaises(rollout.Failure) as caught:
            self.runner.status()
        self.assertTrue(caught.exception.unknown)
        self.assertEqual(caught.exception.code, 'rollout.write_unconfirmed')

    def test_ambiguous_rollback_reconciles_requested_target(self):
        self.runner.upgrade(NEW, NEW)
        self.engine.ambiguous = True
        self.fails('rollout.engine_unavailable', lambda: self.runner.rollback('gateway'))
        self.assertEqual(self.journal.data['requested_images']['gateway'], OLD)
        self.assertTrue(self.runner.status())



class EngineTransportTests(unittest.TestCase):
    def test_update_uses_exact_version_and_does_not_retry_or_expose_error_body(self):
        requests = []
        response = SimpleNamespace(status=200, read=lambda: b'{}')

        class Connection:
            def __init__(self, path):
                self.path = path
            def request(self, method, target, body, headers):
                requests.append((method, target, json.loads(body), headers))
            def getresponse(self):
                return response
            def close(self):
                pass

        with patch.object(rollout, 'UnixConnection', Connection):
            engine = rollout.Engine('/tmp/fixture.sock')
            current = service('gateway')
            spec = copy.deepcopy(current['Spec'])
            spec['TaskTemplate']['ContainerSpec']['Image'] = NEW
            engine.update(current, spec)
            self.assertEqual(requests[0][:3], ('POST', '/v1.47/services/gateway/update?version=10', spec))
            response.status = 409
            response.read = lambda: b'{"message":"secret-token private diagnostic"}'
            with self.assertRaises(rollout.Failure) as caught:
                engine.update(current, spec)
            self.assertEqual(str(caught.exception), 'rollout.concurrent_update')
            self.assertEqual(len(requests), 2)
            response.status = 500
            with self.assertRaises(rollout.Failure) as caught:
                engine.update(current, spec)
            self.assertTrue(caught.exception.unknown)
            self.assertEqual(len(requests), 3)

if __name__ == '__main__':
    unittest.main()
