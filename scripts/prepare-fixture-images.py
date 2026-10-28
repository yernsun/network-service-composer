#!/usr/bin/env python3
"""Prepare only locked NSC fixture images in the selected test engine cache."""
import argparse
import json
from pathlib import Path
import re
import subprocess

PROFILES = {
    'ha': ['postgres', 'mysql', 'nginx', 'docker_dind'],
    'p0': ['postgres', 'mysql', 'nginx', 'docker_dind', 'node_frontend_builder', 'alpine', 'registry', 'prometheus_verification'],
    'docker': ['postgres', 'mysql', 'nginx', 'docker_dind', 'node_frontend_builder', 'alpine', 'registry', 'pebble', 'pebble_challtestsrv'],
}


def run(*arguments):
    return subprocess.run(['docker', *arguments], capture_output=True, text=True, timeout=300)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--profile', choices=PROFILES, default='p0')
    args = parser.parse_args()
    locked = json.loads((Path(__file__).resolve().parents[1] / 'build/image-lock.json').read_text())['images']
    for key in PROFILES[args.profile]:
        image = locked[key]
        if not re.fullmatch(r'[a-zA-Z0-9./:_-]+@sha256:[a-f0-9]{64}', image):
            raise RuntimeError('fixture.image_reference_not_locked')
        if run('image', 'inspect', image).returncode and run('pull', image).returncode:
            raise RuntimeError('fixture.image_prepare_failed:' + key)
        # Nested save/load fixtures use canonical tags because Docker archives do
        # not preserve RepoDigests. The alias always derives from the locked ref.
        if run('tag', image, image.split('@')[0]).returncode:
            raise RuntimeError('fixture.image_alias_failed:' + key)
        print('fixture_image_prepared=' + key, flush=True)


if __name__ == '__main__':
    main()
