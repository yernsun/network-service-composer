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
