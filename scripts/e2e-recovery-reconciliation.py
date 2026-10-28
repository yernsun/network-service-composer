#!/usr/bin/env python3
"""Read-only runtime reconciliation against an isolated nested Docker daemon."""

import argparse
import base64
import copy
import datetime
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys
import uuid


spec = importlib.util.spec_from_file_location('recovery_e2e', Path(__file__).with_name('e2e-recovery.py'))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)

# Kept identical to the existing nested-daemon scheduler fixture until the
# central image lock contains docker_dind. No tag-only fallback is accepted.
LEGACY_DIND_PIN = 'docker:28-dind@sha256:2a232a42256f70d78e3cc5d2b5d6b3276710a0de0596c145f627ecfae90282ac'


class ReconciliationFixture(module.RecoveryFixture):
    def __init__(self, args):
        super().__init__(args)
        suffix = uuid.uuid4().hex[:12]
        self.network = 'nsc-recovery-runtime-net-' + suffix
        self.daemon = 'nsc-recovery-runtime-daemon-' + suffix
        self.daemon_created = False
        self.network_created = False
        self.directory = self.output / 'runtime'
        self.directory.mkdir(mode=0o700)
        (self.directory / 'material').mkdir(mode=0o700)
        self.summary = {'format': 1, 'status': 'FAIL', 'checks': [], 'started_at': datetime.datetime.now(datetime.timezone.utc).isoformat(),
                        'scope': 'ISOLATED_SQLITE_AND_NESTED_DOCKER_RUNTIME', 'ovh': 'DISABLED',
                        'limitations': ['This fixture verifies read-only review and deliberate stale-claim admission; it does not execute a business redeploy.',
                                        'Compose ownership is exercised here; Swarm reconciliation boundaries have separate unit coverage.']}

    def command(self, *arguments, check=True):
        return self.cli(self.directory, self.network, *arguments) if check else self.cli_result(*arguments)

    def cli_result(self, *arguments):
        command = ['run', '--rm', '--user', self.user, '--network', self.network,
                   '-v', f'{self.directory}:/source', '-v', f'{self.directory / "material"}:/run/secrets:ro']
        if self.args.controller_binary:
            command.extend(['-v', f'{Path(self.args.controller_binary).resolve()}:/usr/local/bin/nsc-controller:ro'])
        return self.docker(*command, self.args.image, *arguments, check=False)

    def helper_command(self, action, *arguments):
        _, output = self.docker('run', '--rm', '--user', self.user, '--network', self.network,
                                '-v', f'{self.directory}:/source', '-v', f'{self.helper}:/fixture-helper:ro',
                                '--entrypoint', '/fixture-helper', self.args.image, '--action', action,
                                '--config', '/source/config.json', '--metadata', '/source/metadata.json', *arguments)
        return json.loads(output) if output else None

    def nested(self, *arguments, check=True):
        return self.docker('exec', self.daemon, 'docker', *arguments, check=check)

    def assert_rejected(self, arguments, reason):
        code, _ = self.command(*arguments, check=False)
        error = (self.output / f'command-{self.sequence}-error.log').read_bytes() if code else b''
        if code == 0 or reason not in error:
            raise RuntimeError('reconciliation failed to reject the intended unsafe case')
        observed = self.helper_command('inspect')
        if observed != self.held_baseline:
            raise RuntimeError('rejected reconciliation changed held operation or restored database')

    def execute(self):
        _, commit = self.run('git', 'rev-parse', 'HEAD')
        self.summary['source_commit'] = commit.decode().strip()
        self.summary['source_scripts_sha256'] = {name: hashlib.sha256((self.repository / name).read_bytes()).hexdigest()
            for name in ('scripts/e2e-recovery-reconciliation.py', 'scripts/e2e-recovery.py', 'scripts/recovery-fixture/main.go')}
        self.summary['controller_image_id'] = self.docker('image', 'inspect', '-f', '{{.Id}}', self.args.image)[1].decode().strip()
        self.args.image = self.summary['controller_image_id']
        if self.args.controller_binary:
            self.summary['controller_binary_sha256'] = hashlib.sha256(Path(self.args.controller_binary).read_bytes()).hexdigest()
        if not self.args.helper:
            self.run(os.environ.get('GO', 'go'), 'build', '-trimpath', '-buildvcs=false', '-o', str(self.helper), './scripts/recovery-fixture')
        daemon_image = self.images.get('docker_dind', LEGACY_DIND_PIN)
        self.summary['dind_reference_source'] = 'IMAGE_LOCK' if 'docker_dind' in self.images else 'EXISTING_SCHEDULER_FIXTURE_PIN'
        self.summary['dind_image_reference'] = daemon_image
        self.docker('network', 'create', '--internal', '--label', 'io.network-service-composer.e2e=true', self.network)
        self.network_created = True
        self.docker('run', '-d', '--privileged', '--name', self.daemon, '--hostname', self.daemon,
                    '--network', self.network, '--label', 'io.network-service-composer.e2e=true',
                    '-e', 'DOCKER_TLS_CERTDIR=', daemon_image, '--tls=false')
        self.daemon_created = True
        self.wait(lambda: self.nested('info', check=False)[0] == 0, 'nested Docker readiness')
        inspect = json.loads(self.docker('inspect', self.daemon)[1])[0]
        if inspect['HostConfig'].get('PortBindings') or any(mount.get('Source') == '/var/run/docker.sock' for mount in inspect['Mounts']):
            raise RuntimeError('nested runtime acquired a published port or host Docker socket')
        archive = self.directory / 'fixture-image.tar'
        with archive.open('wb') as output:
            self.docker('image', 'save', self.images['alpine'], stdout=output)
        archive.chmod(0o600)
        with archive.open('rb') as source:
            result = subprocess.run(['docker', 'exec', '-i', self.daemon, 'docker', 'image', 'load'], stdin=source,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=120)
        if result.returncode:
            raise RuntimeError('could not load the pinned workload image into the isolated daemon')
        _, workload_image = self.docker('image', 'inspect', '-f', '{{.Id}}', self.images['alpine'])
        workload_image = workload_image.decode().strip()
        self.summary['workload_image_id'] = workload_image
        self.nested('image', 'inspect', workload_image)
        _, container = self.nested('run', '-d', '--name', 'recovery-owned-web', '--restart', 'always',
                                    '--label', 'com.docker.compose.project=recovery-owned',
                                    '--label', 'com.docker.compose.service=web', workload_image, 'sleep', '600')
        self.container = container.decode().strip()
        _, identity = self.nested('info', '--format', '{{.ID}}')
        self.engine = identity.decode().strip()
        config = {'storage': {'driver': 'sqlite', 'dsn': 'file:/source/recovery.db?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_time_format=sqlite',
                              'ha': False, 'auto_migrate': False},
                  'server': {'grpc_listen': '', 'public_url': 'http://controller.example.test:8080'},
                  'controller': {'recovery_mode': True, 'local_gateway': False}, 'management': {'enabled': True},
                  'security': {'admin_token': secrets.token_urlsafe(32), 'session_key': secrets.token_hex(16), 'primary_key_id': 'backup',
                               'encryption_keys': {'backup': base64.b64encode(secrets.token_bytes(32)).decode()}}}
        module.private_json(self.directory / 'config.json', config)
        self.command('migrate', '--config', '/source/config.json')
        self.helper_command('seed-runtime', '--runtime-endpoint', 'tcp://' + self.daemon + ':2375', '--container', self.container)
        self.command('recovery-prepare', '--config', '/source/config.json', '--output', '/source/prepared.json')
        self.metadata = json.loads((self.directory / 'metadata.json').read_text())
        self.operation = self.metadata['interrupted_operation_id']
        self.held_baseline = self.helper_command('inspect')
        common = ['recovery-reconcile', '--config', '/source/config.json', '--operation', self.operation]
        self.assert_rejected([*common, '--expected-engine', 'wrong-reviewed-engine', '--output', '/source/wrong-engine.json'],
                             b'identity is not the reviewed target')
        self.summary['checks'].append('wrong_engine_rejected_without_database_or_workload_change')
        self.command(*common, '--expected-engine', self.engine, '--output', '/source/observed.json')
        if self.helper_command('inspect') != self.held_baseline:
            raise RuntimeError('read-only runtime capture changed restored state')
        review = json.loads((self.directory / 'observed.json').read_text())
        item = next(item for item in review['items'] if item['id'] == self.operation)
        evidence = item['reconciliation']['evidence']
        if item['decision'] != 'reconcile' or review['activation_reviewed'] or not item['reconciliation']['receipt']:
            raise RuntimeError('capture bypassed explicit operator review')
        if len(evidence['objects']) != 1 or evidence['objects'][0]['id'] != self.container or not evidence['objects'][0]['active']:
            raise RuntimeError('capture did not observe the actual owned workload')
        self.summary['checks'].append('fresh_owned_runtime_capture_read_only_with_authenticated_receipt')
        review['activation_reviewed'] = True
        module.private_json(self.directory / 'reviewed.json', review)
        self.nested('stop', '-t', '2', 'recovery-owned-web')
        self.assert_rejected(['recovery-release', '--config', '/source/config.json', '--review', '/source/reviewed.json'], b'precondition')
        self.summary['checks'].append('changed_workload_state_rejected_and_unknown_history_preserved')
        self.nested('start', 'recovery-owned-web')
        self.command(*common, '--expected-engine', self.engine, '--output', '/source/observed-fresh.json')
        fresh = json.loads((self.directory / 'observed-fresh.json').read_text())
        fresh['activation_reviewed'] = True
        module.private_json(self.directory / 'reviewed-fresh.json', fresh)
        # Allow the nested daemon to stop its workload and containerd before
        # restarting the same engine; forced outer shutdown leaves stale state.
        self.docker('stop', '-t', '30', self.daemon)
        self.assert_rejected(['recovery-release', '--config', '/source/config.json', '--review', '/source/reviewed-fresh.json'], b'runtime observation unavailable')
        self.summary['checks'].append('offline_runtime_rejected_and_holds_preserved')
        self.docker('start', self.daemon)
        self.wait(lambda: self.nested('info', check=False)[0] == 0, 'same engine restart')
        self.nested('start', 'recovery-owned-web')
        self.command(*common, '--expected-engine', self.engine, '--output', '/source/final-observation.json')
        final = json.loads((self.directory / 'final-observation.json').read_text())
        final['activation_reviewed'] = True
        module.private_json(self.directory / 'final-review.json', final)
        self.command('recovery-release', '--config', '/source/config.json', '--review', '/source/final-review.json')
        released = self.helper_command('inspect')
        original = copy.deepcopy(self.held_baseline['operations'][self.operation])
        original['held'] = False
        if released['prepared'] or released['operations'][self.operation] != original:
            raise RuntimeError('reviewed reconciliation guessed outcome or changed operation history')
        _, running = self.nested('inspect', '-f', '{{.State.Running}}', self.container)
        if running.strip() != b'true':
            raise RuntimeError('read-only recovery tools changed the owned workload')
        claimed = self.helper_command('claim-approved')
        if claimed['operations'][self.operation]['attempt'] != original['attempt'] + 1:
            raise RuntimeError('explicitly reviewed stale claim was not recoverable once')
        self.summary['checks'].extend(['explicit_fresh_review_releases_hold_without_guessing_outcome',
                                       'owned_workload_preserved', 'existing_stale_claim_admitted_deliberately'])
        self.summary['status'] = 'PASS'

    def capture_daemon_diagnostics(self):
        # Capture only this fixture's explicitly created daemon. These private
        # files explain infrastructure failure without changing recovery verdicts.
        if not self.daemon_created:
            return
        diagnostics = {'status': 'UNAVAILABLE'}
        try:
            code, state = self.docker('inspect', '-f', '{{json .State}}', self.daemon, check=False)
            if code == 0:
                module.private_json(self.output / 'daemon-state.json', json.loads(state))
                diagnostics['state'] = 'daemon-state.json'
            result = subprocess.run(['docker', 'logs', '--timestamps', '--tail', '200', self.daemon],
                                    stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=10)
            descriptor = os.open(self.output / 'daemon.log', os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(descriptor, 'wb') as stream:
                stream.write(result.stdout)
            diagnostics['logs'] = 'daemon.log'
            diagnostics['logs_exit_code'] = result.returncode
            diagnostics['status'] = 'CAPTURED' if code == 0 and result.returncode == 0 else 'PARTIAL'
        except Exception:
            # Diagnostic failure cannot hide the original failure or prevent
            # owned-resource cleanup from running.
            diagnostics['capture_error'] = 'owned_daemon_diagnostics_unavailable'
        self.summary['daemon_diagnostics'] = diagnostics

    def cleanup(self):
        cleanup = []
        if self.daemon_created:
            cleanup.append(self.docker('rm', '-fv', self.daemon, check=False)[0])
        if self.network_created:
            cleanup.append(self.docker('network', 'rm', self.network, check=False)[0])
        self.summary['cleanup_exit_codes'] = cleanup
        if any(cleanup):
            self.summary['status'] = 'FAIL'
            self.summary['error'] = 'owned reconciliation resources did not clean up'


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    version = (Path(__file__).resolve().parent.parent / 'VERSION').read_text().strip()
    parser.add_argument('--image', default=os.environ.get('NSC_CONTROLLER_IMAGE', 'nsc-controller:v' + version))
    parser.add_argument('--controller-binary', help='optional current static candidate binary')
    parser.add_argument('--helper', help='optional compiled scripts-only recovery fixture helper')
    parser.add_argument('--output', default='coverage/recovery-reconciliation/' + datetime.datetime.now().strftime('%Y%m%dT%H%M%S') + '-' + str(os.getpid()))
    args = parser.parse_args()
    fixture = ReconciliationFixture(args)
    try:
        fixture.execute()
    except Exception as error:
        fixture.summary['error'] = str(error)
    finally:
        if fixture.summary['status'] != 'PASS':
            fixture.capture_daemon_diagnostics()
        fixture.cleanup()
        fixture.summary['finished_at'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        module.private_json(fixture.output / 'summary.json', fixture.summary)
    print('recovery_reconciliation status=' + fixture.summary['status'] + ' report=' + str(fixture.output / 'summary.json'), flush=True)
    return 0 if fixture.summary['status'] == 'PASS' else 1


if __name__ == '__main__':
    sys.exit(main())
