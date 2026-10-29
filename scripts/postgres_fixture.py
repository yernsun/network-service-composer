"""Readiness for disposable PostgreSQL fixtures reached over TCP."""

import time


def wait_for_postgres(inner, node, attempts=60, delay=1, sleep=time.sleep):
    # The official image accepts Unix-socket connections on a temporary server
    # during initialization. Controllers need the final TCP-listening server.
    for attempt in range(attempts):
        try:
            inner(node, 'exec', 'fixture-db', 'pg_isready', '-h', '127.0.0.1',
                  '-U', 'nsc', '-d', 'nsc')
            return
        except RuntimeError:
            if attempt + 1 < attempts:
                sleep(delay)
    raise RuntimeError('fixture database TCP readiness timed out')


def start_migrated_controllers(inner, nodes, image):
    mounts = ('--network', 'host', '--user', '0',
              '-v', '/fixture/pki:/run/nsc:ro',
              '-v', '/fixture/controller.json:/etc/nsc/controller.yaml:ro')
    # Match HA installation: migrate once before any controller is started.
    # The per-node configurations must set storage.auto_migrate=false.
    inner(nodes[0], 'run', '--rm', *mounts, image,
          'migrate', '--config', '/etc/nsc/controller.yaml')
    for node in nodes:
        inner(node, 'run', '-d', '--name', 'fixture-controller', *mounts,
              '-v', '/var/run/docker.sock:/var/run/docker.sock', image)
