import importlib.util
import json
import socket
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location('access_tool', Path(__file__).with_name('verify-access.py'))
tool = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tool)


class AccessVerificationTest(unittest.TestCase):
    def snapshot(self, digest='b' * 64):
        return {'operation_id': 'fixture-operation', 'baseline_digest': digest,
                'access_targets': [{'host': 'localhost', 'scheme': 'http', 'port': 80, 'path': '/'}],
                'stages': [{'name': name, 'state': 'UNKNOWN' if name == 'client_access' else 'CONFIRMED'} for name in ['accepted', 'workload', 'certificate', 'gateway', 'client_access']]}

    def test_changed_baseline_cannot_confirm_access(self):
        states = iter([self.snapshot(), self.snapshot('c' * 64)])
        report = tool.verify(lambda: next(states), lambda _: {'status': 'CONFIRMED'})
        self.assertEqual(report['status'], 'UNKNOWN')
        self.assertFalse(report['baseline_unchanged'])

    def test_partial_endpoint_failure_cannot_confirm_access(self):
        snapshot = self.snapshot()
        snapshot['access_targets'].append(snapshot['access_targets'][0])
        responses = iter([{'status': 'CONFIRMED'}, {'status': 'FAILED'}])
        report = tool.verify(lambda: snapshot, lambda _: next(responses))
        self.assertEqual(report['status'], 'FAILED')

    def test_loaded_evidence_remains_separate_from_actual_access(self):
        snapshot = self.snapshot()
        snapshot['stages'][0]['state'] = 'UNKNOWN'
        report = tool.verify(lambda: snapshot, lambda _: {'status': 'CONFIRMED'})
        self.assertEqual(report['status'], 'UNKNOWN')
        self.assertEqual(report['access_status'], 'CONFIRMED')
        self.assertFalse(report['readiness_prerequisites_confirmed'])

    def test_no_target_is_not_a_success(self):
        snapshot = self.snapshot(); snapshot['access_targets'] = []
        with self.assertRaises(tool.VerificationError):
            tool.verify(lambda: snapshot, lambda _: {'status': 'CONFIRMED'})



    def test_observation_loss_after_probe_preserves_checks_as_unknown(self):
        calls = iter([self.snapshot(), None])
        def load():
            value = next(calls)
            if value is None: raise OSError('fixture unavailable')
            return value
        report = tool.verify(load, lambda _: {'status': 'CONFIRMED'})
        self.assertEqual(report['status'], 'UNKNOWN')
        self.assertEqual(report['access'][0]['status'], 'CONFIRMED')

    def test_missing_prerequisite_cannot_confirm_readiness(self):
        snapshot = self.snapshot(); snapshot['stages'].pop()
        self.assertEqual(tool.verify(lambda: snapshot, lambda _: {'status': 'CONFIRMED'})['status'], 'UNKNOWN')

    def test_later_dns_failure_preserves_an_already_successful_target(self):
        snapshot = self.snapshot()
        snapshot['access_targets'].append({'host': 'missing.example.test', 'scheme': 'http', 'port': 80, 'path': '/'})
        def access_probe(target):
            if target['host'] == 'missing.example.test':
                raise tool.VerificationError('access.dns_unavailable')
            return {'host': target['host'], 'status': 'CONFIRMED'}
        report = tool.verify(lambda: snapshot, access_probe)
        self.assertEqual(report['status'], 'FAILED')
        self.assertEqual([check['status'] for check in report['access']], ['CONFIRMED', 'FAILED'])
        self.assertEqual(report['access'][1]['error_code'], 'access.dns_unavailable')
        self.assertTrue(report['baseline_unchanged'])

    def test_malformed_final_observations_preserve_completed_access_checks(self):
        malformed = [None, [], {}, {**self.snapshot(), 'stages': 'invalid'},
                     {**self.snapshot(), 'stages': [None]},
                     {**self.snapshot(), 'stages': [{'name': [], 'state': 'CONFIRMED'}]}]
        missing_stages = self.snapshot(); del missing_stages['stages']; malformed.append(missing_stages)
        for after in malformed:
            with self.subTest(after=type(after).__name__):
                snapshots = iter([self.snapshot(), after])
                report = tool.verify(lambda: next(snapshots), lambda _: {'status': 'CONFIRMED'})
                self.assertEqual(report['status'], 'UNKNOWN')
                self.assertEqual(report['access'][0]['status'], 'CONFIRMED')
                self.assertFalse(report['readiness_prerequisites_confirmed'])
                self.assertEqual(report['readiness_error_code'], 'readiness.observation_invalid')

    def test_final_json_data_failure_retains_checks_without_raw_diagnostics(self):
        first = True
        def load():
            nonlocal first
            if first:
                first = False
                return self.snapshot()
            raise KeyError('private-final-response-diagnostic')
        report = tool.verify(load, lambda _: {'status': 'CONFIRMED'})
        self.assertEqual(report['status'], 'UNKNOWN')
        self.assertEqual(report['access'][0]['status'], 'CONFIRMED')
        self.assertEqual(report['readiness_error_code'], 'readiness.observation_unavailable')
        self.assertNotIn('private-final-response-diagnostic', json.dumps(report))

    def test_mismatched_operation_rejected(self):
        with self.assertRaises(tool.VerificationError):
            tool.verify(self.snapshot, lambda _: {'status': 'CONFIRMED'}, 'another-operation')

    def test_resolver_stall_has_a_bounded_child(self):
        from unittest.mock import patch
        import subprocess
        with patch.object(tool.subprocess, 'run', side_effect=subprocess.TimeoutExpired('resolver', 1)) as run:
            with self.assertRaises(tool.VerificationError): tool.resolve_addresses('localhost', 80, 1)
            self.assertEqual(run.call_args.kwargs['timeout'], 1)


class ActualClientBoundaryTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        import http.server
        import secrets
        import ssl
        import subprocess
        import tempfile
        import threading
        cls.private = tempfile.TemporaryDirectory(prefix='nsc-access-')
        directory = Path(cls.private.name)
        cls.key, cls.cert = directory / 'key.pem', directory / 'cert.pem'
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1',
                        '-subj', '/CN=localhost', '-addext', 'subjectAltName=DNS:localhost,IP:127.0.0.1',
                        '-keyout', str(cls.key), '-out', str(cls.cert)], check=True, capture_output=True)
        cls.fingerprint = tool.hashlib.sha256(ssl.PEM_cert_to_DER_cert(cls.cert.read_text())).hexdigest()
        cls.requests = []
        class Business(http.server.BaseHTTPRequestHandler):
            def do_GET(self):
                cls.requests.append((self.path, self.headers.get('Host'), self.headers.get('Authorization')))
                self.send_response(200)
                self.send_header('Content-Length', '0')
                self.end_headers()
            def log_message(self, *_): pass
        class IPv6LoopbackServer(http.server.ThreadingHTTPServer):
            address_family = socket.AF_INET6
            def server_bind(self):
                self.socket.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
                super().server_bind()
        cls.business = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Business)
        cls.business_ipv6 = IPv6LoopbackServer(('::1', cls.business.server_port), Business)
        cls.sni = []
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(cls.cert, cls.key)
        context.set_servername_callback(lambda connection, name, context: cls.sni.append(name))
        cls.business.socket = context.wrap_socket(cls.business.socket, server_side=True)
        cls.business_ipv6.socket = context.wrap_socket(cls.business_ipv6.socket, server_side=True)
        cls.thread = threading.Thread(target=cls.business.serve_forever, daemon=True)
        cls.thread_ipv6 = threading.Thread(target=cls.business_ipv6.serve_forever, daemon=True)
        cls.thread.start()
        cls.thread_ipv6.start()
        cls.token = secrets.token_urlsafe(32)
        cls.token_file = directory / 'token'
        cls.token_file.write_text(cls.token)
        cls.token_file.chmod(0o600)

    @classmethod
    def tearDownClass(cls):
        cls.business.shutdown(); cls.business.server_close(); cls.thread.join()
        cls.business_ipv6.shutdown(); cls.business_ipv6.server_close(); cls.thread_ipv6.join()
        cls.private.cleanup()

    def snapshot(self):
        return {'operation_id': 'fixture-operation', 'baseline_digest': 'b' * 64,
                'access_targets': [{'host': '127.0.0.1', 'scheme': 'https', 'port': self.business.server_port,
                                   'path': '/health', 'certificate_fingerprint': self.fingerprint}],
                'stages': [{'name': name, 'state': 'UNKNOWN' if name == 'client_access' else 'CONFIRMED'}
                           for name in ['accepted', 'workload', 'certificate', 'gateway', 'client_access']]}

    def test_actual_system_dns_tls_chain_fingerprint_and_http(self):
        result = tool.probe(self.snapshot()['access_targets'][0], str(self.cert), 2)
        self.assertEqual(result['status'], 'CONFIRMED')
        self.assertEqual(result['addresses'][0]['certificate_fingerprint'], self.fingerprint)
        self.assertEqual(self.requests[-1], ('/health', '127.0.0.1:' + str(self.business.server_port), None))

    def test_actual_hostname_resolver_and_original_host_sni(self):
        target = self.snapshot()['access_targets'][0]; target['host'] = 'localhost'
        result = tool.probe(target, str(self.cert), 2)
        self.assertEqual(result['status'], 'CONFIRMED')
        self.assertTrue(result['addresses'])
        self.assertEqual(self.requests[-1][1], 'localhost:' + str(self.business.server_port))
        self.assertEqual(self.sni[-1], 'localhost')

    def test_valid_chain_with_wrong_expected_certificate_cannot_confirm(self):
        target = self.snapshot()['access_targets'][0]; target['certificate_fingerprint'] = '0' * 64
        result = tool.probe(target, str(self.cert), 2)
        self.assertEqual(result['status'], 'FAILED')
        self.assertEqual(result['addresses'][0]['error_code'], 'access.certificate_fingerprint_mismatch')

    def test_unknown_ca_cannot_confirm(self):
        self.assertEqual(tool.probe(self.snapshot()['access_targets'][0], None, 2)['status'], 'FAILED')

    def test_explicit_edge_certificate_does_not_claim_origin_certificate_verification(self):
        target = self.snapshot()['access_targets'][0]
        target['certificate_fingerprint'] = '0' * 64
        result = tool.probe(target, str(self.cert), 2, self.fingerprint)
        self.assertEqual(result['status'], 'CONFIRMED')
        self.assertEqual(result['addresses'][0]['certificate_scope'], 'edge')
        self.assertEqual(result['addresses'][0]['origin_certificate'], 'NOT_VERIFIED')
        self.assertIsNone(self.requests[-1][2])

    def test_real_tls_success_remains_visible_when_another_target_has_no_dns(self):
        from unittest.mock import patch
        snapshot = self.snapshot()
        snapshot['access_targets'].append({**snapshot['access_targets'][0], 'host': 'missing.example.test'})
        actual_resolver = tool.resolve_addresses
        def resolver(host, port, timeout):
            if host == 'missing.example.test':
                raise tool.VerificationError('access.dns_unavailable')
            return actual_resolver(host, port, timeout)
        with patch.object(tool, 'resolve_addresses', side_effect=resolver):
            report = tool.verify(lambda: snapshot, lambda target: tool.probe(target, str(self.cert), 2))
        self.assertEqual(report['status'], 'FAILED')
        self.assertEqual(report['access'][0]['status'], 'CONFIRMED')
        self.assertEqual(report['access'][1]['error_code'], 'access.dns_unavailable')
        self.assertIsNone(self.requests[-1][2])

    def test_slow_business_headers_obey_the_transport_wall_clock_deadline(self):
        import socketserver
        import threading
        import time
        stop = threading.Event()
        received = threading.Event()
        class Slow(socketserver.BaseRequestHandler):
            def handle(inner):
                try:
                    inner.request.recv(8192)
                    received.set()
                    for value in b'HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n':
                        if stop.is_set(): break
                        inner.request.sendall(bytes([value])); time.sleep(.05)
                except OSError: pass
        server = socketserver.ThreadingTCPServer(('127.0.0.1', 0), Slow)
        server.daemon_threads = True
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        try:
            started = time.monotonic()
            result = tool.probe({'host': '127.0.0.1', 'scheme': 'http', 'port': server.server_address[1], 'path': '/'}, None, .4)
            self.assertLess(time.monotonic() - started, 3)
            self.assertTrue(received.is_set(), 'transport did not reach the slow-header fixture')
            self.assertEqual(result['status'], 'FAILED')
            self.assertEqual(result['addresses'][0]['error_code'], 'access.deadline_exceeded')
        finally:
            stop.set(); server.shutdown(); server.server_close(); thread.join()

    def test_api_slow_headers_and_body_have_deadlines_from_a_worker_thread(self):
        import http.server
        import threading
        import time
        for phase in ['headers', 'body']:
            with self.subTest(phase=phase):
                stop = threading.Event()
                received = threading.Event()
                snapshot = self.snapshot()
                body = json.dumps({'data': snapshot}).encode()
                class SlowAPI(http.server.BaseHTTPRequestHandler):
                    def do_GET(inner):
                        try:
                            received.set()
                            if phase == 'headers':
                                payload = b'HTTP/1.1 200 OK\r\nContent-Length: ' + str(len(body)).encode() + b'\r\n\r\n'
                            else:
                                inner.send_response(200); inner.send_header('Content-Length', str(len(body))); inner.end_headers()
                                payload = body
                            for value in payload:
                                if stop.is_set(): break
                                inner.wfile.write(bytes([value])); inner.wfile.flush(); time.sleep(.03)
                        except OSError: pass
                    def log_message(inner, *_): pass
                server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), SlowAPI)
                server_thread = threading.Thread(target=server.serve_forever, daemon=True); server_thread.start()
                outcomes = []
                def check():
                    try:
                        tool.load_readiness('http://127.0.0.1:' + str(server.server_port), 'fixture-operation', self.token, None, True, .4)
                    except Exception as error:
                        outcomes.append(error)
                worker = threading.Thread(target=check, daemon=True)
                try:
                    started = time.monotonic(); worker.start(); worker.join(timeout=3)
                    self.assertFalse(worker.is_alive(), 'deadline depends on main-thread signals')
                    self.assertLess(time.monotonic() - started, 3)
                    self.assertTrue(received.is_set(), 'API deadline did not exercise the dripped response')
                    self.assertEqual(len(outcomes), 1)
                    self.assertIsInstance(outcomes[0], tool.VerificationError)
                    self.assertEqual(str(outcomes[0]), 'api.deadline_exceeded')
                    self.assertNotIn(self.token, str(outcomes[0]))
                finally:
                    stop.set(); server.shutdown(); server.server_close(); server_thread.join(); worker.join(timeout=3)

    def test_api_credential_redirect_is_rejected_without_following(self):
        import http.server
        import threading
        received = []
        class Redirect(http.server.BaseHTTPRequestHandler):
            def do_GET(inner):
                received.append(inner.path)
                inner.send_response(302)
                inner.send_header('Location', '/credential-sink')
                inner.send_header('Content-Length', '0'); inner.end_headers()
            def log_message(self, *_): pass
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Redirect)
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        try:
            with self.assertRaises(tool.VerificationError):
                tool.load_readiness('http://127.0.0.1:' + str(server.server_port), 'fixture-operation', self.token, None, True, 2)
            self.assertEqual(received, ['/api/v1/operations/fixture-operation/readiness'])
        finally:
            server.shutdown(); server.server_close(); thread.join()

    def test_cli_read_only_checks_and_private_report(self):
        import http.server
        import subprocess
        import sys
        import threading
        snapshot = self.snapshot(); calls = []
        token = self.token
        class API(http.server.BaseHTTPRequestHandler):
            def do_GET(inner):
                calls.append((inner.path, inner.headers.get('Authorization')))
                body = json.dumps({'data': snapshot}).encode()
                inner.send_response(200); inner.send_header('Content-Length', str(len(body)))
                inner.end_headers(); inner.wfile.write(body)
            def log_message(self, *_): pass
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), API)
        thread = threading.Thread(target=server.serve_forever, daemon=True); thread.start()
        output = Path(self.private.name) / 'result.json'
        try:
            result = subprocess.run([sys.executable, str(Path(tool.__file__)), '--api-url', 'http://127.0.0.1:' + str(server.server_port),
                    '--operation', 'fixture-operation', '--token-file', str(self.token_file), '--allow-http-api',
                    '--ca-file', str(self.cert), '--vantage', 'isolated-test-client', '--output', str(output)],
                    capture_output=True, text=True, timeout=15)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(json.loads(output.read_text())['status'], 'CONFIRMED')
            self.assertEqual(output.stat().st_mode & 0o777, 0o600)
            self.assertNotIn(token, output.read_text() + result.stdout + result.stderr)
            self.assertEqual(len(calls), 2)
            self.assertTrue(all(path.endswith('/readiness') and auth == 'Bearer ' + token for path, auth in calls))
            self.assertIsNone(self.requests[-1][2])
        finally:
            server.shutdown(); server.server_close(); thread.join()
            output.unlink(missing_ok=True)


if __name__ == '__main__':
    unittest.main()
