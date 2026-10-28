#!/usr/bin/env python3
"""Read-only access verification from the intended client network.

No results are uploaded. API credentials are never sent to a business endpoint.
DNS/TLS/HTTP checks and loaded-configuration evidence remain separate results.
"""

import argparse
import datetime
import hashlib
import http.client
import json
import os
from pathlib import Path
import socket
import ssl
import stat
import re
import subprocess
import sys
import urllib.parse
import urllib.request


class VerificationError(Exception):
    pass


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, fp, code, message, headers, new_url):
        raise VerificationError("api.redirect_rejected")


def read_token(path):
    info = path.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_size > 16384:
        raise VerificationError("api.token_file_must_be_private_regular_file")
    value = path.read_text().strip()
    if not value or any(char.isspace() for char in value):
        raise VerificationError("api.invalid_token_file")
    return value


def deadline_action(action, arguments, timeout):
    # A socket timeout measures inactivity. A private child also bounds peers
    # that keep dripping headers/body bytes, from any caller thread or platform.
    # Credentials travel through stdin, never command arguments or diagnostics.
    prefix = "api" if action == "api" else "access"
    try:
        result = subprocess.run([sys.executable, str(Path(__file__).resolve()), "--internal-action"],
                                input=json.dumps({"action": action, "arguments": arguments}),
                                capture_output=True, text=True, timeout=timeout, check=True)
        response = json.loads(result.stdout)
        if not isinstance(response, dict):
            raise VerificationError(prefix + ".observation_unavailable")
        if response.get("error_code"):
            raise VerificationError(response["error_code"])
        return response["data"]
    except subprocess.TimeoutExpired:
        raise VerificationError(prefix + ".deadline_exceeded") from None
    except (subprocess.SubprocessError, ValueError, KeyError, TypeError):
        raise VerificationError(prefix + ".observation_unavailable") from None


def load_readiness(base, operation, token, ca_file, allow_http, timeout):
    return deadline_action("api", [base, operation, token, ca_file, allow_http, timeout], timeout)


def _load_readiness(base, operation, token, ca_file, allow_http, timeout):
    parsed = urllib.parse.urlsplit(base)
    if parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.scheme not in ("http", "https"):
        raise VerificationError("api.invalid_url")
    if parsed.scheme != "https" and not allow_http:
        raise VerificationError("api.https_required")
    context = ssl.create_default_context(cafile=ca_file)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect(), urllib.request.HTTPSHandler(context=context))
    request = urllib.request.Request(base.rstrip("/") + "/api/v1/operations/" + urllib.parse.quote(operation, safe="") + "/readiness",
                                     headers={"Authorization": "Bearer " + token, "Accept": "application/json"})
    with opener.open(request, timeout=timeout) as response:
        raw = response.read(1048577)
    if len(raw) > 1048576:
        raise VerificationError("api.response_too_large")
    return json.loads(raw)["data"]


def resolve_addresses(host, port, timeout):
    # Use a short-lived child so platform resolver stalls obey the deadline.
    program = 'import json,socket,sys; v=json.load(sys.stdin); print(json.dumps(sorted({i[4][0] for i in socket.getaddrinfo(v[0],v[1],type=socket.SOCK_STREAM)})))'
    try:
        result = subprocess.run([sys.executable, '-c', program], input=json.dumps([host, port]),
                                capture_output=True, text=True, timeout=timeout, check=True)
        return json.loads(result.stdout)
    except (subprocess.SubprocessError, ValueError):
        raise VerificationError("access.dns_unavailable") from None


def failed_check(target, error):
    result = {"status": "FAILED", "error_code": str(error) if isinstance(error, VerificationError) else "access.observation_unavailable", "addresses": []}
    if isinstance(target, dict):
        for key in ("host", "scheme", "port"):
            if isinstance(target.get(key), (str, int)):
                result[key] = target[key]
    return result


def probe(target, ca_file, timeout, edge_fingerprint=None):
    try:
        host, port = target["host"], int(target["port"])
        scheme, path = target["scheme"], target.get("path", "/")
        if not isinstance(host, str) or not isinstance(path, str) or scheme not in ("http", "https") or not 1 <= port <= 65535 or not host or any(c.isspace() for c in host) or any(c in host for c in "/?#@") or any(ord(c) < 32 for c in host + path) or not path.startswith("/"):
            raise VerificationError("access.invalid_target")
        addresses = resolve_addresses(host, port, timeout)
        if not addresses or len(addresses) > 32:
            raise VerificationError("access.invalid_dns_answers")
    except (OSError, ValueError, KeyError, TypeError, VerificationError) as error:
        return failed_check(target, error)
    results = []
    for address in addresses:
        try:
            result = deadline_action("transport", [target, address, ca_file, timeout, edge_fingerprint], timeout)
        except VerificationError as error:
            result = {"address": address, "status": "FAILED", "error_code": str(error)}
        results.append(result)
    return {"host": host, "scheme": scheme, "port": port,
            "status": "CONFIRMED" if all(value["status"] == "CONFIRMED" for value in results) else "FAILED", "addresses": results}


def _probe_address(target, address, ca_file, timeout, edge_fingerprint):
    host, port = target["host"], int(target["port"])
    scheme, path = target["scheme"], target.get("path", "/")
    result = {"address": address, "status": "FAILED"}
    connection = http.client.HTTPConnection(host, port, timeout=timeout)
    try:
        context = ssl.create_default_context(cafile=ca_file) if scheme == "https" else None
        raw = socket.create_connection((address, port), timeout=timeout)
        if scheme == "https":
            try:
                connection.sock = context.wrap_socket(raw, server_hostname=host)
            except Exception:
                raw.close()
                raise
            actual = hashlib.sha256(connection.sock.getpeercert(binary_form=True)).hexdigest()
            result["certificate_fingerprint"] = actual
            expected = edge_fingerprint or target.get("certificate_fingerprint", "")
            if not expected or actual != expected:
                raise VerificationError("access.certificate_fingerprint_mismatch")
            result["certificate_scope"] = "edge" if edge_fingerprint else "gateway"
        else:
            connection.sock = raw
        host_header = "[" + host + "]" if ":" in host else host
        default_port = 443 if scheme == "https" else 80
        if port != default_port:
            host_header += ":" + str(port)
        connection.request("GET", path, headers={"Host": host_header, "Connection": "close"})
        response = connection.getresponse()
        result["http_status"] = response.status
        # Status is sufficient for reachability; do not consume streaming bodies,
        # cookies, application headers or confidential URLs.
        if not 200 <= response.status < 300:
            raise VerificationError("access.http_response_not_successful")
        result["status"] = "CONFIRMED"
        if edge_fingerprint:
            result["origin_certificate"] = "NOT_VERIFIED"
    except (OSError, ValueError, http.client.HTTPException, VerificationError) as error:
        result["error_code"] = str(error) if isinstance(error, VerificationError) else "access.transport_failed"
    finally:
        connection.close()
    return result


def verify(load, access_probe, expected_operation=None):
    before = load()
    if not isinstance(before, dict):
        raise VerificationError("readiness.observation_invalid")
    baseline = before.get("baseline_digest")
    if not isinstance(baseline, str) or not re.fullmatch(r"[a-f0-9]{64}", baseline):
        raise VerificationError("readiness.baseline_missing")
    if not isinstance(before.get("operation_id"), str) or not before["operation_id"] or expected_operation and before["operation_id"] != expected_operation:
        raise VerificationError("readiness.operation_mismatch")
    targets = before.get("access_targets") or []
    if not isinstance(targets, list):
        raise VerificationError("access.invalid_targets")
    if not targets:
        raise VerificationError("access.no_target")
    checks = []
    for target in targets:
        try:
            check = access_probe(target)
            if not isinstance(check, dict) or check.get("status") not in ("CONFIRMED", "FAILED", "UNKNOWN"):
                raise VerificationError("access.result_invalid")
        except (OSError, ValueError, KeyError, TypeError, http.client.HTTPException, VerificationError) as error:
            check = failed_check(target, error)
        checks.append(check)
    observation_error = None
    try:
        after = load()
        if not isinstance(after, dict):
            raise VerificationError("readiness.observation_invalid")
    except (OSError, ValueError, KeyError, TypeError, http.client.HTTPException, VerificationError) as error:
        after = {}
        observation_error = str(error) if isinstance(error, VerificationError) else "readiness.observation_unavailable"
    state = "CONFIRMED" if all(check["status"] == "CONFIRMED" for check in checks) else "FAILED"
    if after.get("baseline_digest") != baseline or after.get("operation_id") != before["operation_id"]:
        state = "UNKNOWN"
    required = {"accepted", "workload", "certificate", "gateway", "client_access"}
    stages = after.get("stages") or []
    valid_stages = (isinstance(stages, list) and len(stages) == len(required)
                    and all(isinstance(stage, dict) and isinstance(stage.get("name"), str) and stage["name"] in required and isinstance(stage.get("state"), str) for stage in stages)
                    and {stage["name"] for stage in stages} == required)
    prerequisites = valid_stages and all(stage["state"] in ("CONFIRMED", "NOT_APPLICABLE") for stage in stages if stage["name"] != "client_access")
    if not valid_stages and not observation_error:
        observation_error = "readiness.observation_invalid"
    access_state = state
    if state == "CONFIRMED" and not prerequisites:
        state = "UNKNOWN"
    report = {"schema": 1, "status": state, "access_status": access_state, "baseline_digest": baseline,
            "source_operation_id": before["operation_id"], "checked_at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
            "readiness_prerequisites_confirmed": prerequisites,
            "baseline_unchanged": after.get("baseline_digest") == baseline, "access": checks}
    if observation_error:
        report["readiness_error_code"] = observation_error
    return report


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--api-url", required=True)
    parser.add_argument("--operation", required=True)
    parser.add_argument("--token-file", required=True, type=Path)
    parser.add_argument("--api-ca-file")
    parser.add_argument("--ca-file", help="business endpoint CA bundle")
    parser.add_argument("--expect-edge-fingerprint", help="explicit edge certificate; does not verify origin TLS")
    parser.add_argument("--allow-http-api", action="store_true", help="explicit local fixture/operator exception")
    parser.add_argument("--timeout", type=float, default=5)
    parser.add_argument("--vantage", required=True, help="operator-assigned client network label")
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    if not 0 < args.timeout <= 30 or not args.vantage or len(args.vantage) > 128 or any(ord(c) < 32 for c in args.vantage):
        parser.error("timeout must be bounded by 30 seconds and vantage by 128 characters")
    if args.expect_edge_fingerprint and not re.fullmatch(r"[a-f0-9]{64}", args.expect_edge_fingerprint):
        parser.error("edge fingerprint must be a lowercase SHA-256 digest")
    try:
        token = read_token(args.token_file)
        report = verify(lambda: load_readiness(args.api_url, args.operation, token, args.api_ca_file, args.allow_http_api, args.timeout),
                    lambda target: probe(target, args.ca_file, args.timeout, args.expect_edge_fingerprint), args.operation)
    except (OSError, ValueError, KeyError, TypeError, VerificationError) as error:
        report = {"schema": 1, "status": "UNKNOWN", "error_code": str(error) if isinstance(error, VerificationError) else "verification.incomplete", "source_operation_id": args.operation}
    report["vantage"] = args.vantage
    report["verifier_digest"] = hashlib.sha256(Path(__file__).read_bytes()).hexdigest()
    args.output.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    descriptor = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(descriptor, "w") as stream:
        json.dump(report, stream, indent=2)
    print(report["status"])
    return 0 if report["status"] == "CONFIRMED" else 1


def internal_action():
    action = None
    try:
        message = json.loads(sys.stdin.read(1048577))
        action, arguments = message["action"], message["arguments"]
        if action == "api":
            value = _load_readiness(*arguments)
        elif action == "transport":
            value = _probe_address(*arguments)
        else:
            raise VerificationError("verification.invalid_action")
        response = {"data": value}
    except VerificationError as error:
        response = {"error_code": str(error)}
    except (OSError, ValueError, KeyError, TypeError, http.client.HTTPException):
        response = {"error_code": "api.observation_unavailable" if action == "api" else "access.observation_unavailable"}
    print(json.dumps(response))
    return 0


if __name__ == "__main__":
    raise SystemExit(internal_action() if sys.argv[1:] == ["--internal-action"] else main())
