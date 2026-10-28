#!/usr/bin/env python3
"""Image-only NSC rolling updates against an explicitly selected local manager.

No dependency installation, migrations, enrollment, service creation, or automatic
rollback. JSON reports contain allowlisted observations, never Docker inspections.
"""
import argparse
import copy
import datetime
import fcntl
import hashlib
import http.client
import json
import os
from pathlib import Path
import re
import socket
import ssl
import threading
import time
from urllib.parse import quote, urlencode, urlsplit


class Failure(Exception):
    def __init__(self, code, unknown=False):
        self.code, self.unknown = code, unknown
        super().__init__(code)


def require(condition, code):
    if not condition:
        raise Failure(code)


def stamp():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def fingerprint(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':')).encode()).hexdigest()


def immutable(image):
    return isinstance(image, str) and re.fullmatch(r'[A-Za-z0-9][A-Za-z0-9._:/-]*@sha256:[0-9a-f]{64}', image) is not None


def image_of(service):
    return service['Spec']['TaskTemplate']['ContainerSpec']['Image']


def image_free(spec):
    result = copy.deepcopy(spec)
    result['TaskTemplate']['ContainerSpec'].pop('Image', None)
    return fingerprint(result)


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('localhost', timeout=10)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.sock.settimeout(self.timeout)
        self.sock.connect(self.path)


class Engine:
    def __init__(self, path):
        require(Path(path).is_absolute(), 'rollout.socket_invalid')
        self.path = path

    def call(self, method, path, body=None, query=None):
        connection = UnixConnection(self.path)
        try:
            target = '/v1.47' + path
            if query:
                target += '?' + urlencode(query)
            connection.request(method, target, None if body is None else json.dumps(body), {'Content-Type': 'application/json'})
            response = connection.getresponse()
            payload = response.read()
            if response.status == 409:
                raise Failure('rollout.concurrent_update')
            if response.status >= 400:
                raise Failure('rollout.engine_rejected', unknown=method != 'GET')
            return json.loads(payload) if payload else None
        except (OSError, http.client.HTTPException, ValueError):
            raise Failure('rollout.engine_unavailable', unknown=True) from None
        finally:
            connection.close()

    def service(self, reference):
        return self.call('GET', '/services/' + quote(reference, safe=''))

    def tasks(self, service_id):
        return self.call('GET', '/tasks', query={'filters': json.dumps({'service': [service_id]})})

    def nodes(self):
        return self.call('GET', '/nodes')

    def update(self, service, spec, rollback=False):
        query = {'version': service['Version']['Index']}
        if rollback:
            query['rollback'] = 'previous'
        return self.call('POST', '/services/' + service['ID'] + '/update', spec, query)


def fetch(url, token=None, ca_file=None, connect_address=None):
    parsed = urlsplit(url)
    require(parsed.scheme in ('http', 'https') and parsed.hostname and not parsed.username and not parsed.password
            and not parsed.fragment, 'rollout.url_invalid')
    connection = None
    try:
        if parsed.scheme == 'https':
            context = ssl.create_default_context(cafile=ca_file)
            connection = http.client.HTTPSConnection(parsed.hostname, parsed.port or 443, timeout=3, context=context)
            if connect_address:
                raw = socket.create_connection((connect_address, parsed.port or 443), timeout=3)
                try:
                    connection.sock = context.wrap_socket(raw, server_hostname=parsed.hostname)
                except BaseException:
                    raw.close()
                    raise
        else:
            connection = http.client.HTTPConnection(connect_address or parsed.hostname, parsed.port or 80, timeout=3)
        headers = {'Host': parsed.netloc, 'Connection': 'close'}
        if token:
            headers['Authorization'] = 'Bearer ' + token
        connection.request('GET', parsed.path or '/', headers=headers) if not parsed.query else connection.request(
            'GET', (parsed.path or '/') + '?' + parsed.query, headers=headers)
        response = connection.getresponse()
        # Bounded diagnostic probes; response bodies are never written to reports.
        data = response.read(1024 * 1024 + 1)
        require(len(data) <= 1024 * 1024, 'rollout.response_too_large')
        return response.status, data
    except (OSError, http.client.HTTPException, ValueError):
        raise Failure('rollout.http_unavailable', unknown=True) from None
    finally:
        if connection:
            connection.close()


def gateway_healthy(value, binding, after=0):
    evidence = value.get('evidence') or {}
    try:
        observed = datetime.datetime.fromisoformat(evidence['observed_at'].replace('Z', '+00:00')).timestamp()
        revision = value['desired_config_revision']
        model, rendered = value['model_digest'], value['rendered_digest']
        return (value['id'] == binding['gateway_id'] and value['pool_id'] == binding['pool_id']
                and value['enabled'] is True and value['status'] == 'READY' and revision > 0
                and value['applied_config_revision'] == evidence['confirmed_revision'] == evidence['last_confirmed_revision'] == revision
                and evidence['loading_confirmed'] is True and evidence['startup_state'] == 'CONFIRMED'
                and evidence['configuration_state'] == 'CONFIRMED'
                and re.fullmatch('[0-9a-f]{64}', model) is not None
                and re.fullmatch('[0-9a-f]{64}', rendered) is not None
                and model == evidence['model_digest'] and rendered == evidence['rendered_digest'] == evidence['disk_digest']
                and max(after, time.time() - 30) <= observed <= time.time() + 5)
    except (KeyError, TypeError, ValueError):
        return False


def validate_policy(service, component):
    spec = service['Spec']
    require('Global' in spec['Mode'], 'rollout.global_required')
    for key in ('UpdateConfig', 'RollbackConfig'):
        policy = spec.get(key, {})
        require(policy.get('Parallelism') == 1 and policy.get('Order') == 'stop-first'
                and policy.get('FailureAction') == 'pause' and policy.get('MaxFailureRatio', 0) == 0
                and policy.get('Monitor', 0) >= 30_000_000_000
                and policy.get('Delay', 0) >= 30_000_000_000, 'rollout.policy_unsafe')
    container = spec['TaskTemplate']['ContainerSpec']
    require(container.get('StopGracePeriod', 0) >= 30_000_000_000, 'rollout.grace_too_short')
    health = container.get('Healthcheck', {}).get('Test', [])
    if component == 'gateway':
        require(health == ['CMD', '/usr/local/bin/nsc-agent', '--healthcheck', '--config', '/etc/nsc/agent.yaml'],
                'rollout.gateway_healthcheck_required')
        ports = spec.get('EndpointSpec', {}).get('Ports', [])
        require(all(any(p['TargetPort'] == target and p['Protocol'] == protocol and p.get('PublishMode') == 'ingress'
                        for p in ports) for target, protocol in ((80, 'tcp'), (443, 'tcp'), (443, 'udp'))), 'rollout.ingress_required')
    else:
        require(health == ['CMD', 'wget', '-q', '-O', '/dev/null', 'http://127.0.0.1:8080/readyz'],
                'rollout.controller_healthcheck_required')
    require(immutable(container['Image']), 'rollout.current_image_not_immutable')


def selected_nodes(service, nodes):
    """Fail closed for unsupported constraints rather than guessing eligibility."""
    constraints = service['Spec']['TaskTemplate'].get('Placement', {}).get('Constraints', [])
    require(bool(constraints), 'rollout.placement_required')
    chosen = []
    for node in nodes:
        matches = True
        for expression in constraints:
            match = re.fullmatch(r'\s*(node\.id|node\.role|node\.labels\.[\w.-]+)\s*(==|!=)\s*([^\s]+)\s*', expression)
            require(match is not None, 'rollout.constraint_unsupported')
            key, operator, expected = match.groups()
            actual = node['ID'] if key == 'node.id' else node['Spec'].get('Role') if key == 'node.role' else node['Spec'].get('Labels', {}).get(key[12:])
            matches = matches and ((actual == expected) if operator == '==' else (actual != expected))
        if matches:
            require(node['Status']['State'] == 'ready' and node['Spec']['Availability'] == 'active', 'rollout.node_unavailable')
            chosen.append(node['ID'])
    require(bool(chosen), 'rollout.no_eligible_nodes')
    return set(chosen)


def current_tasks(tasks, nodes, image=None):
    desired = [t for t in tasks if t['DesiredState'] == 'running']
    return (len(desired) == len(nodes) and {t['NodeID'] for t in desired} == set(nodes)
            and all(t['Status']['State'] == 'running' and (image is None or t['Spec']['ContainerSpec']['Image'] == image) for t in desired))


class Journal:
    def __init__(self, directory):
        self.directory = Path(directory)
        self.directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        require(not self.directory.is_symlink() and self.directory.stat().st_mode & 0o077 == 0, 'rollout.report_permissions')
        self.lock = open(self.directory / 'lock', 'a', encoding='utf-8')
        os.chmod(self.directory / 'lock', 0o600)
        try:
            fcntl.flock(self.lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            self.lock.close()
            raise Failure('rollout.report_busy') from None
        self.path = self.directory / 'summary.json'
        self.data = json.loads(self.path.read_text()) if self.path.exists() else {'schema_version': 1, 'operations': [], 'events': []}

    def save(self):
        temporary = self.directory / 'summary.tmp'
        fd = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
        with os.fdopen(fd, 'w') as stream:
            json.dump(self.data, stream, indent=2)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, self.path)

    def event(self, code, status):
        self.data['events'].append({'at': stamp(), 'code': code, 'status': status})
        self.data['status'] = status
        self.save()

    def close(self):
        self.lock.close()


class Traffic:
    def __init__(self, probes, directory):
        self.probes, self.directory = probes, directory
        self.stop = threading.Event()
        self.counts = [{'samples': 0, 'failed': 0} for _ in probes]
        self.threads = []
        self.write_lock = threading.Lock()
        self.error = False

    def sample(self, index):
        probe = self.probes[index]
        started = time.monotonic()
        ok = False
        try:
            status, data = fetch(probe['url'], ca_file=probe.get('ca_file'), connect_address=probe.get('connect_address'))
            ok = status == probe.get('status', 200) and hashlib.sha256(data).hexdigest() == probe['body_sha256']
        except Failure:
            pass
        item = {'at': stamp(), 'probe': index, 'ok': ok, 'duration_ms': round((time.monotonic() - started) * 1000, 2)}
        with self.write_lock:
            self.counts[index]['samples'] += 1
            self.counts[index]['failed'] += int(not ok)
            fd = os.open(self.directory / 'requests.jsonl', os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW, 0o600)
            with os.fdopen(fd, 'a') as stream:
                stream.write(json.dumps(item) + '\n')
        return ok

    def start(self):
        for index in range(len(self.probes)):
            def loop(index=index):
                while not self.stop.is_set():
                    try:
                        self.sample(index)
                    except OSError:
                        self.error = True
                        self.stop.set()
                    self.stop.wait(0.2)
            thread = threading.Thread(target=loop, daemon=True)
            thread.start()
            self.threads.append(thread)

    def finish(self):
        self.stop.set()
        for thread in self.threads:
            thread.join()
        return copy.deepcopy(self.counts)


class Rollout:
    def __init__(self, configuration, journal, timeout=900, engine=None):
        self.cfg, self.journal, self.timeout = configuration, journal, timeout
        self.engine = engine or Engine(configuration.get('docker_socket', '/var/run/docker.sock'))
        self.bindings = configuration['gateways']
        require(len(self.bindings) >= 2 and len({v['node_id'] for v in self.bindings}) == len(self.bindings)
                and len({v['gateway_id'] for v in self.bindings}) == len(self.bindings)
                and len({v['pool_id'] for v in self.bindings}) == 1, 'rollout.bindings_invalid')
        self.token = Path(configuration['token_file']).read_text().strip()
        require(bool(self.token), 'rollout.token_empty')
        self.probes = configuration['probes']
        for binding in self.bindings:
            require(all(any(p['node_id'] == binding['node_id'] and urlsplit(p['url']).scheme == scheme for p in self.probes)
                        for scheme in ('http', 'https')), 'rollout.node_probes_required')
        for probe in self.probes:
            require(re.fullmatch('[0-9a-f]{64}', probe['body_sha256']) is not None, 'rollout.probe_digest_required')
        # Bind recovery to the same operator inputs without persisting credentials.
        identity = fingerprint({k: configuration[k] for k in ('controller_service', 'gateway_service', 'gateways', 'api_url', 'probes')})
        previous = journal.data.setdefault('configuration_sha256', identity)
        require(previous == identity, 'rollout.report_configuration_mismatch')
        self.traffic = Traffic(self.probes, journal.directory)

    def api(self, path):
        status, data = fetch(self.cfg['api_url'].rstrip('/') + path, self.token, self.cfg.get('api_ca_file'))
        require(status == 200, 'rollout.api_not_ready')
        try:
            return json.loads(data)['data']
        except (KeyError, ValueError, TypeError):
            raise Failure('rollout.api_invalid', unknown=True) from None

    def evidence(self, after=0):
        evidence = []
        for binding in self.bindings:
            value = self.api('/api/v1/gateways/' + quote(binding['gateway_id'], safe=''))
            if gateway_healthy(value, binding, after):
                evidence.append({'node_id': binding['node_id'], 'gateway_id': binding['gateway_id'],
                                 'revision': value['applied_config_revision'], 'model_digest': value['model_digest'],
                                 'rendered_digest': value['rendered_digest'], 'observed_at': value['evidence']['observed_at']})
        return evidence

    def inspect(self, component):
        service = self.engine.service(self.cfg[component + '_service'])
        recorded = self.journal.data.setdefault('services', {})
        require(component not in recorded or recorded[component] == service['ID'], 'rollout.service_replaced')
        recorded[component] = service['ID']
        validate_policy(service, component)
        return service

    def check(self, rollback=False):
        info = self.engine.call('GET', '/info')
        require(info.get('Swarm', {}).get('ControlAvailable') is True, 'rollout.manager_required')
        nodes = self.engine.nodes()
        services = {c: self.inspect(c) for c in ('controller', 'gateway')}
        for component, service in services.items():
            state = service.get('UpdateStatus', {}).get('State', '')
            permitted = ('', 'completed', 'rollback_completed', 'paused', 'rollback_paused') if rollback else ('', 'completed', 'rollback_completed')
            require(state in permitted, 'rollout.update_in_progress')
            selected = selected_nodes(service, nodes)
            if component == 'gateway':
                require(selected == {b['node_id'] for b in self.bindings}, 'rollout.node_mapping_mismatch')
            tasks = self.engine.tasks(service['ID'])
            if not rollback:
                require(current_tasks(tasks, selected, image_of(service)), 'rollout.tasks_not_ready')
            elif component == 'controller':
                require(any(t['DesiredState'] == 'running' and t['Status']['State'] == 'running' for t in tasks),
                        'rollout.controller_unavailable')
        evidence = self.evidence()
        running = {t['NodeID'] for t in self.engine.tasks(services['gateway']['ID'])
                   if t['DesiredState'] == 'running' and t['Status']['State'] == 'running'}
        healthy = [e for e in evidence if e['node_id'] in running]
        require(len(healthy) >= 2 and (rollback or len(healthy) == len(self.bindings)), 'rollout.insufficient_healthy_gateways')
        require(all(self.traffic.sample(i) for i in range(len(self.probes))), 'rollout.business_probe_failed')
        self.journal.data['evidence'] = healthy
        self.journal.save()
        return services

    def observe(self, op):
        service = self.engine.service(op['service_id'])
        actual = fingerprint(service['Spec'])
        if actual == op['before_sha256'] and op['status'] == 'UNKNOWN':
            raise Failure('rollout.write_unconfirmed', unknown=True)
        require(actual == op['target_sha256'], 'rollout.concurrent_update')
        nodes = selected_nodes(service, self.engine.nodes())
        require(nodes == set(op['nodes']), 'rollout.topology_changed')
        tasks = self.engine.tasks(service['ID'])
        op['tasks'] = [{'id': t['ID'], 'node_id': t['NodeID'], 'desired': t['DesiredState'],
                        'state': t['Status']['State']} for t in tasks]
        state = service.get('UpdateStatus', {}).get('State', '')
        op['swarm_state'] = state
        if state in ('paused', 'rollback_paused'):
            raise Failure('rollout.paused')
        wanted = 'rollback_completed' if op['action'] == 'rollback' else 'completed'
        if state != wanted or not current_tasks(tasks, nodes, op['target_image']):
            return False
        status, _ = fetch(self.cfg['api_url'].rstrip('/') + '/readyz', ca_file=self.cfg.get('api_ca_file'))
        if status != 200:
            return False
        evidence = self.evidence(op['started_epoch'])
        if len(evidence) != len(self.bindings):
            return False
        op['evidence'] = evidence
        return True

    def wait(self, op):
        deadline = time.monotonic() + self.timeout
        while time.monotonic() < deadline:
            if self.observe(op):
                op['status'] = 'SUCCEEDED'
                self.journal.save()
                return
            self.journal.save()
            time.sleep(1)
        raise Failure('rollout.deadline_exceeded', unknown=True)

    def change(self, component, service, target, rollback=False):
        require(immutable(target), 'rollout.target_image_not_immutable')
        if not rollback and image_of(service) == target:
            return
        spec = copy.deepcopy(service['PreviousSpec'] if rollback else service['Spec'])
        spec['TaskTemplate']['ContainerSpec']['Image'] = target
        nodes = sorted(selected_nodes(service, self.engine.nodes()))
        op = {'component': component, 'action': 'rollback' if rollback else 'upgrade', 'service_id': service['ID'],
              'base_version': service['Version']['Index'], 'before_sha256': fingerprint(service['Spec']),
              'non_image_sha256': image_free(service['Spec']), 'target_sha256': fingerprint(spec),
              'previous_image': image_of(service), 'target_image': target, 'nodes': nodes,
              'started_at': stamp(), 'started_epoch': time.time(), 'status': 'UNKNOWN'}
        self.journal.data['operations'].append(op)
        self.journal.save()  # Persist intent BEFORE sending a potentially ambiguous write.
        try:
            self.engine.update(service, service['Spec'] if rollback else spec, rollback)
            op['status'] = 'RUNNING'
            self.journal.save()
            self.wait(op)
        except Failure as exc:
            op['status'] = 'UNKNOWN' if exc.unknown else 'FAILED'
            op['reason'] = exc.code
            self.journal.save()
            raise

    def upgrade(self, controller_image, gateway_image):
        require(immutable(controller_image) and immutable(gateway_image), 'rollout.target_image_not_immutable')
        self.check()
        self.journal.data['requested_images'] = {'controller': controller_image, 'gateway': gateway_image}
        self.journal.save()
        for component, target in (('controller', controller_image), ('gateway', gateway_image)):
            require(not self.traffic.error and not any(c['failed'] for c in self.traffic.counts), 'rollout.traffic_failed')
            services = self.check()
            self.change(component, services[component], target)

    def rollback(self, component):
        service = self.check(rollback=True)[component]
        candidates = [op for op in self.journal.data['operations'] if op['component'] == component]
        require(bool(candidates), 'rollout.no_recorded_update')
        previous = candidates[-1]
        require(previous['action'] == 'upgrade' and fingerprint(service['Spec']) == previous['target_sha256']
                and fingerprint(service.get('PreviousSpec')) == previous['before_sha256']
                and image_free(service['PreviousSpec']) == image_free(service['Spec']), 'rollout.rollback_conflict')
        self.journal.data.setdefault('requested_images', {})[component] = previous['previous_image']
        self.journal.save()
        self.change(component, service, previous['previous_image'], rollback=True)

    def status(self):
        require(bool(self.journal.data.get('requested_images') or self.journal.data['operations']), 'rollout.no_recorded_update')
        # Only the latest operation per component is current; keep earlier history.
        latest = {op['component']: op for op in self.journal.data['operations']}
        complete = True
        for op in latest.values():
            if self.observe(op):
                op['status'] = 'SUCCEEDED'
            else:
                op['status'] = 'RUNNING'
                complete = False
        for component, target in self.journal.data.get('requested_images', {}).items():
            service = self.inspect(component)
            complete = complete and image_of(service) == target
        if complete:
            self.check()
        self.journal.save()
        return complete


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=('check', 'upgrade', 'status', 'rollback'))
    parser.add_argument('--config', required=True, type=Path)
    parser.add_argument('--report-dir', required=True, type=Path)
    parser.add_argument('--controller-image')
    parser.add_argument('--gateway-image')
    parser.add_argument('--component', choices=('controller', 'gateway'), default='gateway')
    parser.add_argument('--timeout', type=int, default=900)
    args = parser.parse_args()
    journal, rollout = None, None
    status, code = 'FAILED', 'rollout.input_invalid'
    try:
        require(args.timeout > 0, 'rollout.timeout_invalid')
        journal = Journal(args.report_dir)
        rollout = Rollout(json.loads(args.config.read_text()), journal, args.timeout)
        if args.action == 'status':
            status = 'SUCCEEDED' if rollout.status() else 'RUNNING'
        elif args.action == 'check':
            rollout.check()
            status = 'SUCCEEDED'
        else:
            rollout.check(rollback=args.action == 'rollback')
            rollout.traffic.start()
            if args.action == 'upgrade':
                rollout.upgrade(args.controller_image, args.gateway_image)
            else:
                rollout.rollback(args.component)
            # Even a no-op must not claim traffic acceptance without samples.
            deadline = time.monotonic() + 10
            while min(c['samples'] for c in rollout.traffic.counts) < 10 and time.monotonic() < deadline:
                time.sleep(.2)
            status = 'SUCCEEDED'
        code = 'rollout.' + status.lower()
    except Failure as exc:
        status, code = ('UNKNOWN' if exc.unknown else 'FAILED'), exc.code
    except KeyboardInterrupt:
        status, code = 'UNKNOWN', 'rollout.interrupted'
    except (OSError, ValueError, KeyError, TypeError):
        status, code = 'UNKNOWN' if journal and journal.data['operations'] else 'FAILED', 'rollout.input_or_report_unavailable'
    finally:
        if journal:
            if rollout:
                counts = rollout.traffic.finish()
                journal.data['events'].append({'at': stamp(), 'action': args.action, 'traffic': counts})
                if any(c['failed'] for c in counts):
                    journal.data['traffic_failed'] = True
                if rollout.traffic.error or (args.action in ('upgrade', 'rollback') and status == 'SUCCEEDED'
                                             and any(c['samples'] < 10 for c in counts)):
                    status, code = 'UNKNOWN', 'rollout.traffic_evidence_missing'
                if journal.data.get('traffic_failed') and status == 'SUCCEEDED':
                    status, code = 'FAILED', 'rollout.traffic_failed'
            journal.event(code, status)
            journal.close()
    print(json.dumps({'status': status, 'code': code}))
    return 0 if status == 'SUCCEEDED' else 1


if __name__ == '__main__':
    raise SystemExit(main())
