#!/usr/bin/env python3
"""Real NSC rolling acceptance in three disposable nested Docker daemons.

The host Swarm is never initialized, joined, or updated. Initial provisioning is
outside traffic windows. All subsequent windows use Swarm ingress, no test LB.
"""
import argparse
import copy
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import time

from gateway_acceptance import Fixture, HOST, BODY, utc_now
import swarm_rollout as rollout


class SwarmFixture(Fixture):
    def __init__(self, args):
        super().__init__(args)
        self.nodes = []
        self.journal = None
        self.registry = self.network + '-registry'
        self.summary['limitations'] = [
            'Nested daemons share one physical host; host/zone failures are not tested.',
            'Image variants use the current binary with distinct immutable manifests; arbitrary cross-version compatibility is not established.',
            'HTTP/1.1 and verified TLS are sampled; HTTP/2, HTTP/3 and unbounded long-lived connections are not established.']
        self.summary['source_fingerprint'] = self.source_fingerprint()

    def source_fingerprint(self):
        files = subprocess.check_output(['git', '-C', str(self.repository), 'ls-files', '-z', '--cached', '--others',
            '--exclude-standard', '--', 'internal', 'cmd', 'api', 'build', 'frontend/src', 'frontend/package.json',
            'frontend/package-lock.json', 'scripts/swarm_rollout.py', 'scripts/swarm_rollout_acceptance.py',
            'deploy/swarm.yaml']).split(b'\0')
        digest = hashlib.sha256()
        for name in sorted(set(files) - {b''}):
            path = self.repository / os.fsdecode(name)
            digest.update(name + b'\0')
            digest.update(path.read_bytes() if path.is_file() else b'<missing>')
        return digest.hexdigest()

    def address(self, container):
        return json.loads(self.docker('inspect', container))[0]['NetworkSettings']['Networks'][self.network]['IPAddress']

    def inner(self, index, *args):
        return self.docker('exec', self.nodes[index], 'docker', *args)

    def prepare_swarm(self):
        self.stage = 'swarm_prerequisites'
        images = json.loads((self.repository / 'build/image-lock.json').read_text())['images']
        for key in ('docker_dind', 'registry'):
            self.docker('image', 'inspect', images[key])
        self.prepare()
        self.sampler.close()
        self.sampler = None
        self.balancer.close()
        self.balancer = None
        # Make the application endpoint reachable from nested overlay networks.
        route = self.api('GET', '/routes/' + self.route_id)
        self.publish('swarm-seed', [{'route': {'id': self.route_id, 'domain_id': self.domain['id'], 'path': '/',
                                              'match': 'prefix', 'kind': 'proxy', 'websocket_enabled': True,
                                              'enabled': True, 'source': 'API',
                                              'external_url': 'http://' + self.address(self.backend) + ':8080'},
                                     'expected_revision': route['revision']}])
        self.stage = 'nested_swarm'
        self.run(self.registry, images['registry'])
        registry_address = self.address(self.registry) + ':5000'
        for index in range(3):
            node = self.network + '-node-' + str(index)
            directory = self.work / ('node-' + str(index))
            directory.mkdir(mode=0o755)
            self.nodes.append(node)
            self.containers.append(node)
            self.docker('run', '-d', '--privileged', '--name', node, '--network', self.network,
                        '--label', 'io.network-service-composer.e2e=true', '-e', 'DOCKER_TLS_CERTDIR=',
                        '-v', str(directory) + ':/fixture/node', '-v', str(self.work) + ':/fixture/shared:ro',
                        images['docker_dind'], 'dockerd', '--tls=false', '--host=unix:///var/run/docker.sock',
                        '--host=unix:///fixture/node/docker.sock', '--insecure-registry=' + registry_address)
            self.wait(lambda index=index: self.daemon_ready(index), 'nested Docker', timeout=90)
            self.docker('exec', node, 'chown', f'{os.getuid()}:{os.getgid()}', '/fixture/node/docker.sock')
        self.inner(0, 'swarm', 'init', '--advertise-addr', self.address(self.nodes[0]))
        join = self.inner(0, 'swarm', 'join-token', '-q', 'manager')
        for index in (1, 2):
            self.inner(index, 'swarm', 'join', '--token', join, '--advertise-addr', self.address(self.nodes[index]),
                       self.address(self.nodes[0]) + ':2377')
        self.node_ids = [self.inner(i, 'info', '--format', '{{.Swarm.NodeID}}') for i in range(3)]
        for index, node_id in enumerate(self.node_ids):
            self.inner(0, 'node', 'update', '--label-add', 'nsc.controller=true', '--label-add',
                       'nsc.gateway=' + ('true' if index < 2 else 'false'), node_id)
        self.inner(0, 'network', 'create', '--driver', 'overlay', '--attachable', 'nsc-fixture-ingress')
        self.stage = 'immutable_fixture_images'
        archive = self.work / 'images.tar'
        self.docker('save', '-o', str(archive), self.args.controller_image, self.args.gateway_image)
        self.inner(0, 'load', '-i', '/fixture/shared/images.tar')
        self.refs = {}
        for component, source in (('controller', self.args.controller_image), ('gateway', self.args.gateway_image)):
            self.refs[component] = self.push(source, registry_address + '/' + component + ':base')
            self.refs[component + '_next'] = self.variant(source, component + '-next', registry_address, 'LABEL nsc.fixture.replacement=next\n')
        self.refs['delayed'] = self.variant(self.args.gateway_image, 'gateway-delayed', registry_address,
            'ENTRYPOINT ["/bin/sh", "-c", "sleep 15; exec /usr/local/bin/nsc-agent \\"$@\\"", "--"]\n')
        self.refs['bad'] = self.variant(self.args.gateway_image, 'gateway-bad', registry_address,
            'ENTRYPOINT ["/bin/sh", "-c", "exit 42"]\n')
        archive.unlink()
        self.stage = 'node_identity_provisioning'
        original = json.loads(self.docker('inspect', self.controller))[0]['Config']['Env']
        base_config = {}
        for item in original:
            key, _, value = item.partition('=')
            if not key.startswith('NSC_'):
                continue
            pieces = key[4:].lower().split('__')
            target = base_config
            for piece in pieces[:-1]:
                target = target.setdefault(piece, {})
            if value in ('true', 'false') or pieces[-1] == 'encryption_keys':
                value = json.loads(value)
            target[pieces[-1]] = value
        self.dsn = f'postgres://nsc:{self.database_password}@{self.address(self.database)}:5432/nsc?sslmode=disable'
        self.production_gateways = self.gateways[:2]
        identity = self.cli('id', 'gateway')
        self.cli('pki', 'issue-agent', '--ca-cert', '/fixture/secrets/ca.crt', '--ca-key', '/fixture/secrets/ca.key',
                 '--cert', '/fixture/secrets/gateway-2.crt', '--key', '/fixture/secrets/gateway-2.key',
                 '--pool-id', self.pool['id'], '--gateway-id', identity, '--valid-for', '2h')
        self.production_gateways.append({'id': identity, 'directory': 'gateway-2'})
        for index, gateway in enumerate(self.production_gateways):
            directory = self.work / ('node-' + str(index))
            address = self.address(self.nodes[index])
            controller_id = 'controller-' + str(index)
            self.cli('pki', 'issue-controller', '--instance-id', controller_id,
                     '--ca-cert', '/fixture/secrets/ca.crt', '--ca-key', '/fixture/secrets/ca.key',
                     '--cert', f'/fixture/secrets/{controller_id}.crt', '--key', f'/fixture/secrets/{controller_id}.key',
                     '--ip', address, '--valid-for', '2h')
            config = copy.deepcopy(base_config)
            config['storage'].update(dsn=self.dsn, auto_migrate=False)
            config['controller'].update(instance_id=controller_id, local_gateway=False)
            config['controller']['internal'].update(url='https://' + address + ':9444', grpc_address=address + ':9443')
            config['server']['public_url'] = 'http://' + address + ':8080'
            (directory / 'controller.yaml').write_text(json.dumps(config))
            agent_config = {'storage': {'driver': 'postgres', 'dsn': self.dsn, 'ha': True, 'auto_migrate': False},
                'agent': {'gateway_id': gateway['id'], 'pool_id': self.pool['id'], 'name': gateway['directory'],
                          'controller_address': address + ':9443', 'server_name': address, 'mode': 'production',
                          'ca_cert_file': '/run/nsc/ca.crt', 'tls_cert_file': '/run/nsc/agent.crt',
                          'tls_key_file': '/run/nsc/agent.key', 'state_dir': '/var/lib/nsc', 'heartbeat_interval': '1s'},
                'nginx': {'binary': '/usr/sbin/nginx', 'generation_dir': '/var/lib/nsc/generations',
                          'main_config_path': '/var/lib/nsc/nginx.conf', 'worker_processes': '1'}}
            (directory / 'agent.yaml').write_text(json.dumps(agent_config))
            for role, cert_name in (('controller', controller_id), ('gateway', gateway['directory'])):
                secrets = directory / (role + '-secrets')
                secrets.mkdir(mode=0o755)
                for source, target in [('ca.crt', 'ca.crt'), (cert_name + '.crt', 'controller.crt' if role == 'controller' else 'agent.crt'),
                                       (cert_name + '.key', 'controller.key' if role == 'controller' else 'agent.key')]:
                    # The private parent fixture directory limits access on the host.
                    self.docker('run', '--rm', '--user', '0', '-v', str(self.work) + ':/fixture', self.cleanup_image,
                                'sh', '-c', 'cp /fixture/secrets/' + source + ' /fixture/' + directory.name + '/' + role + '-secrets/' + target
                                + '; chmod 644 /fixture/' + directory.name + '/' + role + '-secrets/' + target)
            state = directory / 'gateway-state'
            state.mkdir(mode=0o777)
            state.chmod(0o777)
            cache = directory / 'gateway-cache'
            cache.mkdir(mode=0o777)
            cache.chmod(0o777)
        self.stage = 'swarm_initial_deployment'
        for gateway in self.gateways[:2]:
            self.docker('stop', gateway['name'])
        self.docker('stop', self.controller)
        environment = dict(os.environ, NSC_CONTROLLER_IMAGE=self.refs['controller'], NSC_GATEWAY_IMAGE=self.refs['gateway'],
                           NSC_NODE_DIR='/fixture/node', NSC_DOCKER_GID='0', NSC_INGRESS_NETWORK='nsc-fixture-ingress')
        result = subprocess.run(['docker', 'stack', 'config', '-c', str(self.repository / 'deploy/swarm.yaml')],
                                env=environment, capture_output=True, text=True, timeout=30, check=True)
        stack_path = self.work / 'stack.yaml'
        stack_path.write_text(result.stdout)
        self.inner(0, 'stack', 'deploy', '--detach=true', '-c', '/fixture/shared/stack.yaml', 'nsc')
        self.api_port = 8080
        # The host can reach the nested nodes directly on their private bridge IP.
        token = self.work / 'rollout-token'
        token.write_text(self.admin_token)
        configuration = {'docker_socket': str(self.work / 'node-0/docker.sock'), 'controller_service': 'nsc_controller',
                         'gateway_service': 'nsc_gateway', 'api_url': 'http://' + self.address(self.nodes[0]) + ':8080',
                         'token_file': str(token), 'gateways': [], 'probes': []}
        for index, gateway in enumerate(self.production_gateways):
            if index < 2:
                configuration['gateways'].append({'node_id': self.node_ids[index], 'gateway_id': gateway['id'], 'pool_id': self.pool['id']})
            for scheme in ('http', 'https'):
                configuration['probes'].append({'node_id': self.node_ids[index], 'url': scheme + '://' + HOST + '/',
                    'connect_address': self.address(self.nodes[index]), 'ca_file': str(self.work / 'tls.crt'),
                    'body_sha256': hashlib.sha256(BODY).hexdigest()})
        output = Path(self.args.output).resolve().parent / 'rollout'
        self.journal = rollout.Journal(output)
        self.runner = rollout.Rollout(configuration, self.journal, timeout=600)
        self.wait(self.cluster_ready, 'all real Swarm services and exact loaded evidence', timeout=240)
        self.summary['images']['swarm_variants'] = self.refs
        self.summary['node_ids'] = self.node_ids

    def daemon_ready(self, index):
        return subprocess.run(['docker', 'exec', self.nodes[index], 'docker', 'info'], capture_output=True).returncode == 0

    def push(self, source, target):
        self.inner(0, 'tag', source, target)
        self.inner(0, 'push', target)
        return json.loads(self.inner(0, 'image', 'inspect', target))[0]['RepoDigests'][0]

    def variant(self, source, name, registry, instructions):
        # Dockerfiles are fixed fixture inputs, never operator-provided shell text.
        context = self.work / (name + '-build')
        context.mkdir()
        path = context / 'Dockerfile'
        path.write_text('FROM ' + source + '\n' + instructions)
        self.inner(0, 'build', '-t', name + ':fixture', '/fixture/shared/' + context.name)
        return self.push(name + ':fixture', registry + '/' + name + ':fixture')

    def cluster_ready(self):
        try:
            self.runner.check()
            return True
        except rollout.Failure as error:
            if self.summary.get('last_readiness_code') != error.code:
                print('swarm_rollout_acceptance readiness=' + error.code, flush=True)
                self.summary['last_readiness_code'] = error.code
            return False

    def window(self, name, action):
        self.stage = name
        print('swarm_rollout_acceptance stage=' + name, flush=True)
        traffic = rollout.Traffic(self.runner.probes, self.journal.directory)
        start = utc_now()
        traffic.start()
        before = self.runner.evidence()
        completed = False
        try:
            action()
            after = self.runner.evidence()
            assert {(e['gateway_id'], e['revision'], e['model_digest']) for e in before} == {
                (e['gateway_id'], e['revision'], e['model_digest']) for e in after}, 'image update changed Pool state or identity'
            deadline = time.monotonic() + 15
            while min(c['samples'] for c in traffic.counts) < 10 and time.monotonic() < deadline:
                time.sleep(.2)
            completed = True
        finally:
            counts = traffic.finish()
            valid = completed and all(c['samples'] >= 10 and c['failed'] == 0 for c in counts)
            self.summary['scenarios'].append({'name': name, 'started_at': start, 'finished_at': utc_now(),
                                             'traffic': counts, 'status': 'PASS' if valid else 'FAIL'})
        assert valid, 'HTTP/TLS continuity failed; retain all requests'

    def failed_upgrade(self):
        try:
            self.runner.upgrade(self.refs['controller_next'], self.refs['bad'])
        except rollout.Failure as error:
            assert error.code == 'rollout.paused', error.code
        else:
            raise AssertionError('bad image did not pause the update')
        service = self.runner.engine.service('nsc_gateway')
        tasks = self.runner.engine.tasks(service['ID'])
        healthy = {t['NodeID'] for t in tasks if t['DesiredState'] == 'running' and t['Status']['State'] == 'running'}
        assert len(healthy) >= 2, 'paused update consumed remaining capacity'
        self.runner.rollback('gateway')
        assert rollout.image_of(self.runner.engine.service('nsc_gateway')) == self.refs['delayed']

    def run_windows(self):
        self.window('controller_election_and_serial_gateway_image_replacement',
                    lambda: self.runner.upgrade(self.refs['controller_next'], self.refs['gateway_next']))
        self.window('delayed_gateway_readiness_and_persistent_lkg',
                    lambda: self.runner.upgrade(self.refs['controller_next'], self.refs['delayed']))
        # Two Gateways establish the minimum supported healthy-upgrade topology.
        # Provision the third before fault injection so rollback retains capacity.
        self.stage = 'provision_rollback_capacity'
        self.inner(0, 'node', 'update', '--label-add', 'nsc.gateway=true', self.node_ids[2])
        configuration = copy.deepcopy(self.runner.cfg)
        configuration['gateways'].append({'node_id': self.node_ids[2], 'gateway_id': self.production_gateways[2]['id'],
                                          'pool_id': self.pool['id']})
        self.journal.close()
        self.journal = rollout.Journal(Path(self.args.output).resolve().parent / 'rollback')
        self.runner = rollout.Rollout(configuration, self.journal, timeout=600)
        self.wait(self.cluster_ready, 'third Gateway capacity', timeout=180)
        self.window('bad_image_pause_and_capacity_checked_rollback', self.failed_upgrade)
        self.summary['status'] = 'PASS'

    def cleanup(self):
        if self.summary['source_fingerprint'] != self.source_fingerprint():
            self.summary.update(status='FAIL', error='fixture.source_changed')
        if self.journal:
            self.journal.close()
        # Docker image VOLUME declarations create anonymous volumes (including
        # nested /var/lib/docker). Capture only this fixture's volume identities
        # before the base fixture removes its containers and private diagnostics.
        volumes = set()
        for name in self.containers:
            result = subprocess.run(['docker', 'inspect', name], capture_output=True, text=True)
            if result.returncode == 0:
                volumes.update(m['Name'] for m in json.loads(result.stdout)[0].get('Mounts', []) if m['Type'] == 'volume')
        try:
            super().cleanup()
        finally:
            for volume in sorted(volumes):
                subprocess.run(['docker', 'volume', 'rm', volume], capture_output=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--controller-image', required=True)
    parser.add_argument('--gateway-image', required=True)
    parser.add_argument('--output', required=True)
    args = parser.parse_args()
    fixture = SwarmFixture(args)
    try:
        fixture.prepare_swarm()
        fixture.run_windows()
    except BaseException as error:
        fixture.summary.update(status='FAIL', error=error.code if isinstance(error, rollout.Failure) else type(error).__name__)
        raise
    finally:
        fixture.cleanup()
    assert fixture.summary['status'] == 'PASS', 'fixture evidence is not a pass'
    print('swarm_rollout_acceptance status=PASS report=' + args.output, flush=True)


if __name__ == '__main__':
    main()
