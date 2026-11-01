#!/usr/bin/env python3
"""Source-bound DNS editor and route validation acceptance in an owned Swarm.

Reuses the isolated bootstrap fixture, with no production credentials, real DNS
provider calls or changes to the host Swarm. OVH verification remains DISABLED.
"""
import argparse
import importlib.util
import json
from pathlib import Path
import secrets
import ssl
import subprocess
import traceback
import urllib.error
import urllib.request


def exercise(fixture, repository, output):
    fixture.prepare()
    fixture.install()
    certificate = fixture.docker('exec', fixture.nodes[0], 'cat',
                                 '/fixture/state.offline/materials/console.crt')
    password = fixture.docker('exec', fixture.nodes[0], 'cat',
                              '/fixture/state.offline/materials/admin-password')
    url = 'https://' + fixture.address(fixture.nodes[0]) + ':8443'
    context = ssl.create_default_context(cadata=certificate)

    def api(method, path, body=None, revision=None):
        headers = {'Authorization': 'Bearer ' + password,
                   'Content-Type': 'application/json',
                   'X-NSC-Reauthenticate': password,
                   'Idempotency-Key': secrets.token_hex(16)}
        if revision is not None:
            headers['If-Match'] = '"rev-' + str(revision) + '"'
        request = urllib.request.Request(
            url + '/api/v1' + path,
            data=json.dumps(body).encode() if body is not None else None,
            headers=headers, method=method)
        try:
            with urllib.request.urlopen(request, context=context, timeout=30) as response:
                return json.load(response)['data']
        except urllib.error.HTTPError as error:
            fixture.private_log('private-dns-route-api-error.json', error.read())
            raise

    def operation_done(identifier):
        operation = api('GET', '/operations/' + identifier)
        if operation['status'] in ['FAILED', 'UNKNOWN', 'CANCELLED']:
            fixture.private_log('private-application-operation.json',
                                json.dumps(operation).encode())
            raise RuntimeError('owned application operation did not succeed')
        return operation['status'] == 'SUCCEEDED'

    fixture.summary['stage'] = 'business_application_fixtures'
    # Ordinary managed Services are routing backends. The system Sandbox keeps
    # its deliberate isolation from the production ingress network.
    nginx = json.loads((repository / 'build/image-lock.json').read_text())['images']['nginx']
    source_id = fixture.docker('image', 'inspect', nginx, '--format', '{{.Id}}')
    export = subprocess.Popen(['docker', 'save', nginx], stdout=subprocess.PIPE,
                              stderr=subprocess.PIPE)
    load = subprocess.Popen(['docker', 'exec', '-i', fixture.nodes[0], 'docker', 'load'],
                            stdin=export.stdout, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE)
    export.stdout.close()
    try:
        _, load_error = load.communicate(timeout=180)
        _, export_error = export.communicate(timeout=30)
    finally:
        for process in (load, export):
            if process.poll() is None:
                process.kill()
                process.communicate()
    if load.returncode or export.returncode:
        fixture.private_log('private-web-image-transfer.log', export_error + load_error)
        raise AssertionError('fixture web image transfer failed')
    tag = fixture.registry_address + '/web:fixture'
    fixture.inner(0, 'tag', source_id, tag)
    fixture.inner(0, 'push', tag)
    web = next(value for value in json.loads(fixture.inner(0, 'image', 'inspect', tag))[0]['RepoDigests']
               if value.startswith(fixture.registry_address + '/'))
    fixture.inner(0, 'pull', web)
    fixture.summary['images']['web'] = source_id

    project = api('POST', '/projects', {
        'name': 'DNS routing acceptance', 'slug': 'dns-routing-acceptance',
        'default_runtime_id': fixture.record['runtime_id'],
        'initial_environment': {'name': 'prod'}})
    environments = api('GET', '/projects/' + project['id'] + '/environments?limit=200')
    primary = next(env for env in environments if env['id'] == project['initial_environment_id'])
    secondary = api('POST', '/projects/' + project['id'] + '/environments', {
        'name': 'trial', 'runtime_id': fixture.record['runtime_id']})
    targets = []
    for environment in (primary, secondary):
        preview = api('POST', '/environments/' + environment['id'] + '/preflight', {
            'compose_yaml': 'services:\n  web:\n    image: ' + web + '\n    expose: ["80"]\n'})
        operation = api('POST', '/environments/' + environment['id'] + '/deployments',
                        {'preflight_id': preview['preflight_id']}, environment['revision'])
        fixture.wait(lambda: operation_done(operation['id']), timeout=150)
        targets.append(next(service for service in api('GET', '/services?limit=200')
                            if service.get('environment', {}).get('id') == environment['id']))
    primary_service, secondary_service = targets

    draft = api('POST', '/environments/' + primary['id'] + '/domain-drafts', {
        'service_id': primary_service['id'],
        'domain': {'host': 'routing.fixture.test', 'entrypoints': ['http'],
                   'tls': {'mode': 'disabled'}},
        'route': {'path': '/', 'backend': {'port': 80, 'protocol': 'http'}}})
    published = api('POST', '/domain-drafts/' + draft['id'] + '/publish', {}, draft['revision'])
    fixture.wait(lambda: operation_done(published['operation_id']), timeout=150)
    domain = api('GET', '/domains/' + published['published_domain_id'])
    attached = api('POST', '/services/' + secondary_service['id'] + '/domain-bindings', {
        'domain_id': domain['id'],
        'route': {'path': '/__nsc_validation/', 'strip_path': True,
                  'backend': {'port': 80, 'protocol': 'http'}}})
    fixture.wait(lambda: operation_done(attached['operation']['id']), timeout=150)
    route_id = attached['binding']['route']['id']

    def services_ready():
        services = {service['id']: service for service in api('GET', '/services?limit=200')}
        return all(services[target['id']]['dns_status'] == 'READY' and
                   services[target['id']]['healthy_instances'] > 0 for target in targets)

    fixture.wait(services_ready, timeout=90)
    fixture.summary['checks']['two_managed_environment_services_with_real_bindings'] = 'PASS'
    base = '/gateway-pools/' + fixture.record['pool_id'] + '/nginx'
    current = api('GET', base)['draft']
    route = next(value for value in api('GET', '/routes?limit=200') if value['id'] == route_id)
    route['read_timeout_seconds'] = 70
    api('PUT', base + '/draft', {
        'snippets': current.get('snippets', []),
        'routes': [{'route': route, 'expected_revision': route['revision']}]}, current['revision'])
    assert primary_service['environment']['id'] != secondary_service['environment']['id'], \
        'fixture did not create different environment claims'
    payload = {
        'url': url, 'password': password,
        'domain_path': '/projects/' + primary['project_id'] + '/environments/' + primary['id'] +
                       '/domains/' + domain['id'] + '?tab=routes',
        'validation_route': route_id, 'primary_service': primary_service['id'],
        'secondary_service': secondary_service['id'], 'output': str(output)}
    fixture.summary['stage'] = 'browser'
    result = subprocess.run(['node', str(repository / 'scripts/dns_route_browser.mjs')],
                            input=json.dumps(payload).encode(), capture_output=True, timeout=360)
    fixture.private_log('private-dns-route-browser.log', result.stderr)
    if result.returncode:
        raise AssertionError('browser regression failed; private diagnostics retained')
    fixture.summary['checks'].update({key: 'PASS' for key in json.loads(result.stdout)['checks']})
    fixture.summary['stage'] = 'actual_access'
    for path in ('/', '/__nsc_validation/'):
        request = urllib.request.Request('http://' + fixture.address(fixture.nodes[0]) + path,
                                         headers={'Host': 'routing.fixture.test'})
        with urllib.request.urlopen(request, timeout=10) as response:
            assert response.status == 200
    fixture.summary['checks']['actual_http_access_root_and_cross_environment_route'] = 'PASS'
    fixture.summary['status'] = 'PASS'


def main():
    repository = Path(__file__).resolve().parent.parent
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--controller-image', required=True)
    parser.add_argument('--gateway-image', required=True)
    parser.add_argument('--fixture-image', required=True)
    parser.add_argument('--output', type=Path, default=repository / 'coverage/dns-route/integration')
    options = parser.parse_args()
    spec = importlib.util.spec_from_file_location('fixture', repository / 'scripts/bootstrap_acceptance.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    args = argparse.Namespace(
        controller_image=options.controller_image, gateway_image=options.gateway_image,
        controller_binary=repository / 'bin/nsc-controller', fixture_image=options.fixture_image,
        nodes=1, cli_user=0, keep=True)
    fixture = module.BootstrapFixture(args)
    output = options.output.resolve()
    output.mkdir(parents=True, exist_ok=True)
    try:
        exercise(fixture, repository, output)
    except Exception as error:
        fixture.summary['status'] = 'FAIL'
        fixture.summary['reason'] = type(error).__name__
        fixture.private_log('private-dns-route-error.log', traceback.format_exc().encode())
        try:
            fixture.private_log('private-services.json',
                                json.dumps(fixture.api(0, '/services?limit=200')).encode())
        except Exception:
            fixture.summary['diagnostics_unavailable'] = True
        (output / 'diagnostics-reference.restricted.json').write_text(
            json.dumps({'directory': str(fixture.work)}) + '\n')
    finally:
        fixture.cleanup()
        fixture.summary['source_fingerprint_final'] = fixture.command(
            'node', 'scripts/readiness/gate.mjs', 'fingerprint')
        if fixture.summary.get('source_fingerprint') != fixture.summary['source_fingerprint_final']:
            fixture.summary['status'] = 'FAIL'
            fixture.summary['reason'] = 'source_changed'
        (output / 'summary.json').write_text(json.dumps(fixture.summary, indent=2) + '\n')
        print(json.dumps(fixture.summary), flush=True)
    return 0 if fixture.summary['status'] == 'PASS' else 1


if __name__ == '__main__':
    raise SystemExit(main())
