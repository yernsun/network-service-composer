"""Issuance and stale configuration ACKs cannot prove certificate distribution."""

import hashlib
import ssl
import unittest
from unittest.mock import MagicMock, patch

from wait_gateway_certificate import wait_for_certificate


class GatewayCertificateWaitTests(unittest.TestCase):
    def probe(self, certificates, expected, timeout=4):
        clock = [0.0]
        context = MagicMock()
        tls = context.wrap_socket.return_value.__enter__.return_value
        tls.getpeercert.side_effect = certificates
        with patch('wait_gateway_certificate.ssl.create_default_context', return_value=context) as create_context, \
             patch('wait_gateway_certificate.socket.create_connection') as connect, \
             patch('wait_gateway_certificate.time.monotonic', side_effect=lambda: clock[0]), \
             patch('wait_gateway_certificate.time.sleep', side_effect=lambda seconds: clock.__setitem__(0, clock[0] + seconds)):
            wait_for_certificate('fixture-ca.pem', '127.0.0.1', 443, 'app.example.test', expected, timeout=timeout)
            create_context.assert_called_once_with(cafile='fixture-ca.pem')
            connect.assert_called_with(('127.0.0.1', 443), timeout=2)
            self.assertTrue(all(call.kwargs['server_hostname'] == 'app.example.test' for call in context.wrap_socket.call_args_list))
        return tls.getpeercert.call_count

    def test_waits_through_rejected_sni_and_old_certificate_until_new_certificate_is_served(self):
        current = b'new certificate DER fixture'
        expected = hashlib.sha256(current).hexdigest()
        self.assertEqual(self.probe([ssl.SSLError('rejected SNI'), b'old certificate DER fixture', current], expected), 3)

    def test_stale_certificate_never_counts_as_distribution(self):
        with self.assertRaisesRegex(TimeoutError, 'expected trusted certificate'):
            self.probe([b'old certificate'] * 3, hashlib.sha256(b'new certificate').hexdigest(), timeout=3)

    def test_untrusted_certificate_never_counts_as_ready(self):
        with self.assertRaises(TimeoutError):
            self.probe([ssl.SSLCertVerificationError('untrusted fixture')] * 2,
                       hashlib.sha256(b'certificate').hexdigest(), timeout=2)

    def test_invalid_fingerprint_fails_before_network_access(self):
        with patch('wait_gateway_certificate.socket.create_connection') as connect:
            with self.assertRaises(ValueError):
                wait_for_certificate('fixture-ca.pem', '127.0.0.1', 443, 'app.example.test', '')
            connect.assert_not_called()


if __name__ == '__main__':
    unittest.main()
