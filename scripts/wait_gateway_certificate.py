"""Wait for a trusted TLS endpoint to serve the expected certificate generation."""

import argparse
import hashlib
import socket
import ssl
import time


def wait_for_certificate(ca_file, connect_host, port, server_name, fingerprint, timeout=90):
    if len(fingerprint) != 64 or any(c not in '0123456789abcdef' for c in fingerprint):
        raise ValueError('expected certificate fingerprint must be lowercase SHA-256')
    context = ssl.create_default_context(cafile=ca_file)
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            with socket.create_connection((connect_host, port), timeout=2) as connection:
                with context.wrap_socket(connection, server_hostname=server_name) as tls:
                    observed = hashlib.sha256(tls.getpeercert(binary_form=True)).hexdigest()
                    if observed == fingerprint:
                        return
        except OSError:
            # Loading a new NGINX generation may temporarily reject this SNI.
            # Certificate/hostname verification failures must never count as ready.
            pass
        time.sleep(min(1, max(0, deadline - time.monotonic())))
    raise TimeoutError('gateway did not serve the expected trusted certificate before the deadline')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--ca-file', required=True)
    parser.add_argument('--connect-host', required=True)
    parser.add_argument('--port', type=int, required=True)
    parser.add_argument('--server-name', required=True)
    parser.add_argument('--fingerprint', required=True)
    args = parser.parse_args()
    wait_for_certificate(**vars(args))


if __name__ == '__main__':
    main()
