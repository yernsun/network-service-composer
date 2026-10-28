#!/usr/bin/env python3
"""Separate-process TCP load balancer for the isolated Gateway HA fixture."""

import argparse
import json
from pathlib import Path
import signal
import threading

from gateway_acceptance import Balancer


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--backends-file', required=True)
    parser.add_argument('--ports-file', required=True)
    args = parser.parse_args()
    backends = json.loads(Path(args.backends_file).read_text())
    if not backends or any(set(backend) != {'http', 'https'} or
                           any(not isinstance(port, int) or not 0 < port <= 65535 for port in backend.values())
                           for backend in backends):
        raise ValueError('expected loopback HTTP/TLS backend ports')
    stopped = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stopped.set())
    signal.signal(signal.SIGINT, lambda *_: stopped.set())
    balancer = Balancer(backends)
    try:
        ports = Path(args.ports_file)
        ports.write_text(json.dumps({'ports': balancer.ports}) + '\n')
        ports.chmod(0o600)
        stopped.wait()
    finally:
        balancer.close()


if __name__ == '__main__':
    main()
