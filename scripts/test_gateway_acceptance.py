#!/usr/bin/env python3
"""Evidence failure boundaries independent of real NGINX integration."""

import copy
import datetime
import unittest

from gateway_acceptance import confirms_gateway, summarize_samples


class EvidenceTests(unittest.TestCase):
    def setUp(self):
        self.after = 1000
        self.gateway = {
            'enabled': True, 'status': 'READY', 'desired_config_revision': 7,
            'applied_config_revision': 7, 'model_digest': 'a' * 64, 'rendered_digest': 'b' * 64,
            'evidence': {'observed_at': datetime.datetime.fromtimestamp(1001, datetime.timezone.utc).isoformat(),
                         'confirmed_revision': 7, 'last_confirmed_revision': 7,
                         'loading_confirmed': True, 'startup_state': 'CONFIRMED',
                         'configuration_state': 'CONFIRMED', 'model_digest': 'a' * 64,
                         'rendered_digest': 'b' * 64, 'disk_digest': 'b' * 64}}

    def test_accepts_fresh_matching_loaded_and_next_start_configuration(self):
        self.assertTrue(confirms_gateway(self.gateway, 7, self.after))

    def test_ready_is_insufficient_for_every_evidence_boundary(self):
        changes = [
            ('enabled', False), ('status', 'OFFLINE'), ('desired_config_revision', 8),
            ('applied_config_revision', 6), ('model_digest', 'c' * 64),
            ('rendered_digest', 'c' * 64),
            ('evidence.loading_confirmed', False), ('evidence.startup_state', 'RISK'),
            ('evidence.configuration_state', 'UNKNOWN'), ('evidence.confirmed_revision', 6),
            ('evidence.last_confirmed_revision', 6), ('evidence.disk_digest', 'c' * 64),
            ('evidence.rendered_digest', 'no digest'), ('evidence.observed_at', 'not a date'),
            ('evidence.observed_at', datetime.datetime.fromtimestamp(999, datetime.timezone.utc).isoformat()),
        ]
        for path, value in changes:
            with self.subTest(path=path, value=value):
                gateway = copy.deepcopy(self.gateway)
                target = gateway
                keys = path.split('.')
                for key in keys[:-1]:
                    target = target[key]
                target[keys[-1]] = value
                self.assertFalse(confirms_gateway(gateway, 7, self.after))

    def test_missing_evidence_is_unknown(self):
        del self.gateway['evidence']
        self.assertFalse(confirms_gateway(self.gateway, 7, self.after))


class TrafficTests(unittest.TestCase):
    def samples(self):
        return [{'scheme': scheme, 'at': 1 + index / 10, 'result': 'PASS', 'duration_seconds': 0.01}
                for scheme in ('http', 'https') for index in range(10)]

    def test_counts_each_protocol_and_all_failed_samples(self):
        samples = self.samples()
        samples[0]['result'] = 'FAIL'
        result = summarize_samples(samples, 0, 3, fault=True)
        self.assertEqual(result['requests']['http'], {'total': 10, 'failed': 1})
        self.assertEqual(result['requests']['https'], {'total': 10, 'failed': 0})
        self.assertTrue(result['fault_injection'])

    def test_one_success_cannot_hide_normal_update_errors(self):
        samples = self.samples()
        samples[1]['result'] = 'FAIL'
        with self.assertRaisesRegex(AssertionError, 'interrupted'):
            summarize_samples(samples, 0, 3)

    def test_fault_window_requires_actual_business_recovery(self):
        samples = self.samples()
        samples[-1]['result'] = 'FAIL'
        with self.assertRaisesRegex(AssertionError, 'did not recover'):
            summarize_samples(samples, 0, 3, fault=True)

    def test_missing_or_out_of_window_samples_do_not_pass(self):
        for samples in ([], self.samples()[:-1], self.samples()):
            with self.subTest(samples=len(samples)):
                with self.assertRaisesRegex(AssertionError, 'insufficient'):
                    summarize_samples(samples, 10, 20)


if __name__ == '__main__':
    unittest.main()
