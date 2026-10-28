#!/usr/bin/env python3
"""Exercise the Controller's bundled Compose plugin with owned Docker fixtures."""

import argparse
import json
from pathlib import Path
import subprocess
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--image', default='nsc-controller:ci')
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    lock = json.loads((root / 'build/image-lock.json').read_text())
    socket = Path('/var/run/docker.sock')
    project = 'nsc-compose-check-' + uuid.uuid4().hex[:12]
    volume = project + '_data'
    sentinel = uuid.uuid4().hex
    fixture_image = lock['images']['alpine']

    def run(command, *, source=None, check=True):
        return subprocess.run(command, input=source, text=True, capture_output=True,
                              check=check, timeout=180)

    # The Controller's configured USER is deliberately retained here.
    plugin = ['docker', 'run', '--rm', '-i', '--read-only', '--network', 'none',
              '--tmpfs', '/tmp', '--group-add', str(socket.stat().st_gid),
              '-v', f'{socket}:{socket}', '--entrypoint', 'docker', args.image,
              'compose']
    actual = run(plugin + ['version', '--short']).stdout.strip().removeprefix('v')
    if actual != lock['tools']['docker_compose']:
        raise RuntimeError(f'Unexpected bundled Compose version: {actual}')
    manifest = json.dumps({'services': {'app': {
        'image': fixture_image, 'command': ['sh', '-c', 'exec sleep 600'],
        'network_mode': 'none', 'volumes': ['data:/data'],
        'healthcheck': {'test': ['CMD', 'test', '-d', '/data'],
                        'interval': '1s', 'timeout': '1s', 'retries': 10},
    }}, 'volumes': {'data': {}}})

    def compose(*command, source=manifest, check=True):
        return run(plugin + ['--project-name', project, '--file', '-', *command],
                   source=source, check=check)

    compose('config', '--quiet')
    invalid = compose('config', '--quiet', source='services: {app: {image: [invalid]}}', check=False)
    if invalid.returncode == 0:
        raise RuntimeError('Invalid Compose manifest was accepted')
    # Resolve only the pinned fixture image; no NSC or existing application image is changed.
    if run(['docker', 'image', 'inspect', fixture_image], check=False).returncode:
        run(['docker', 'pull', fixture_image])
    try:
        compose('up', '-d', '--wait', '--wait-timeout', '30', '--no-build', '--pull', 'never')
        compose('exec', '-T', 'app', 'sh', '-c', 'printf "%s" "$1" > /data/sentinel', 'sh', sentinel)
        compose('stop')
        compose('up', '-d', '--wait', '--wait-timeout', '30', '--no-build', '--pull', 'never')
        if compose('exec', '-T', 'app', 'cat', '/data/sentinel').stdout != sentinel:
            raise RuntimeError('Stop/restart did not preserve volume data')
        compose('down', '--remove-orphans')
        run(['docker', 'volume', 'inspect', volume])
        compose('up', '-d', '--wait', '--wait-timeout', '30', '--no-build', '--pull', 'never')
        if compose('exec', '-T', 'app', 'cat', '/data/sentinel').stdout != sentinel:
            raise RuntimeError('Down/recreate did not preserve volume data')
    finally:
        # Only the random project created by this invocation is eligible for cleanup.
        compose('down', '--volumes', '--remove-orphans')
    print('PASS: bundled Compose version, valid/invalid config, deployment, stop/restart, volume retention and cleanup')


if __name__ == '__main__':
    main()
