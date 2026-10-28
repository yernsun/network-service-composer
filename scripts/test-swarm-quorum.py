#!/usr/bin/env python3
"""Verify quorum loss/recovery in three disposable manager daemons."""
import argparse
import json
import os
from pathlib import Path
import subprocess
import time
import uuid


def run(*args, timeout=60, check=True):
    result = subprocess.run(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=timeout)
    if check and result.returncode:
        raise RuntimeError('fixture.command_failed')
    return result.returncode, result.stdout.decode().strip()


def wait(check, timeout=60):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            if check():
                return
        except RuntimeError:
            pass
        time.sleep(.5)
    raise RuntimeError('fixture.deadline_exceeded')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', required=True, type=Path)
    args = parser.parse_args()
    prefix = 'nsc-quorum-' + uuid.uuid4().hex[:12]
    network, nodes, created = prefix + '-net', [prefix + '-m' + str(i) for i in range(3)], []
    report = {'status': 'FAILED', 'checks': []}
    engine = json.loads((Path(__file__).resolve().parents[1] / 'build/image-lock.json').read_text())['images']['docker_dind']
    def inner(node, *command, **kwargs):
        return run('docker', 'exec', node, 'docker', *command, **kwargs)
    try:
        run('docker', 'network', 'create', '--label', 'io.network-service-composer.fixture=' + prefix, network)
        ips = []
        for node in nodes:
            created.append(node)
            run('docker', 'run', '-d', '--privileged', '--name', node, '--network', network,
                '--label', 'io.network-service-composer.fixture=' + prefix,
                '-e', 'DOCKER_TLS_CERTDIR=', engine, '--tls=false')
            wait(lambda node=node: inner(node, 'info')[0] == 0)
            ips.append(json.loads(run('docker', 'inspect', node)[1])[0]['NetworkSettings']['Networks'][network]['IPAddress'])
        inner(nodes[0], 'swarm', 'init', '--advertise-addr', ips[0])
        token = inner(nodes[0], 'swarm', 'join-token', '-q', 'manager')[1]
        for node, ip in zip(nodes[1:], ips[1:]):
            inner(node, 'swarm', 'join', '--advertise-addr', ip, '--token', token, ips[0] + ':2377')
        def members():
            ids = inner(nodes[0], 'node', 'ls', '-q')[1].splitlines()
            return json.loads(inner(nodes[0], 'node', 'inspect', *ids)[1])
        wait(lambda: len(members()) == 3 and all(n['ManagerStatus']['Reachability'] == 'reachable' for n in members()))
        report['checks'].append('three_reachable_managers')
        # Zero replicas avoids pulling/running business images; this tests Raft state.
        service = inner(nodes[0], 'service', 'create', '--detach', '--no-resolve-image', '--replicas', '0',
                        '--name', prefix + '-service', '--label', 'fixture.revision=1', 'example.test/fixture:fixed')[1]
        leader_name = next(node for node in nodes if inner(node, 'info', '--format', '{{.Swarm.NodeID}}')[1] == next(n['ID'] for n in members() if n['ManagerStatus'].get('Leader')))
        followers = [node for node in nodes if node != leader_name]
        run('docker', 'stop', '-t', '3', followers[0])
        inner(leader_name, 'service', 'update', '--detach', '--label-add', 'fixture.revision=2', service)
        report['checks'].append('one_manager_loss_preserves_writable_quorum')
        run('docker', 'stop', '-t', '3', followers[1])
        def unavailable_inventory():
            try:
                return inner(leader_name, 'node', 'ls', check=False, timeout=10)[0] != 0
            except subprocess.TimeoutExpired:
                return True
        wait(unavailable_inventory, timeout=30)
        # No destructive commands are attempted while observations lack quorum.
        report['checks'].append('two_manager_loss_leaves_inventory_unavailable')
        report['inventory_observation_during_quorum_loss'] = 'UNKNOWN'
        run('docker', 'start', followers[0])
        wait(lambda: inner(leader_name, 'node', 'ls', check=False, timeout=10)[0] == 0)
        restored = json.loads(inner(leader_name, 'service', 'inspect', service)[1])[0]
        assert restored['Spec']['Labels']['fixture.revision'] == '2'
        assert restored['ID'] == service
        report['checks'].append('quorum_recovery_preserves_service_identity_and_state')
        report['status'] = 'PASS'
    finally:
        for node in reversed(created):
            run('docker', 'rm', '-fv', node, check=False)
        run('docker', 'network', 'rm', network, check=False)
        args.output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        descriptor = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(descriptor, 'w') as stream:
            json.dump(report, stream, indent=2)


if __name__ == '__main__':
    main()
