#!/usr/bin/env python3
"""Read-only Gateway application registration from explicitly selected objects.

Run on the Swarm Manager hosting the existing production/Sandbox containers.
The generated JSON replaces controller.system_project on every Controller.
No container, service, volume, database or configuration is modified.
"""
import argparse
import json
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--swarm-runtime-id', required=True)
parser.add_argument('--container-runtime-id', required=True)
parser.add_argument('--controller-service', action='append', required=True)
parser.add_argument('--postgres-service', required=True)
parser.add_argument('--production-container', required=True)
parser.add_argument('--sandbox-container', required=True)
args = parser.parse_args()

def docker(*command):
    return json.loads(subprocess.check_output(['docker', *command], text=True, timeout=20))

swarm = docker('info', '--format', '{{json .Swarm}}')
cluster = swarm.get('Cluster', {}).get('ID')
if not cluster or not swarm.get('ControlAvailable'):
    parser.error('run on a Manager of the registered cluster')

def service(reference):
    return docker('service', 'inspect', '--format', '{{json .ID}}', reference)

def container(reference):
    value = docker('inspect', '--type', 'container', '--format',
                   '{"id":{{json .Id}},"running":{{json .State.Running}},"labels":{{json .Config.Labels}}}', reference)
    labels = value.get('labels') or {}
    if not value['running'] or not labels.get('com.docker.compose.project') or not labels.get('com.docker.compose.service'):
        parser.error('selected container must be running and discoverable through its Compose labels')
    return value['id']

controllers = [service(value) for value in args.controller_service]
postgres = service(args.postgres_service)
production = container(args.production_container)
sandbox = container(args.sandbox_container)
objects = controllers + [postgres, production, sandbox]
if len(set(objects)) != len(objects):
    parser.error('each selected runtime object must be unique')
applications = []
for key, name, environment, runtime, ids in [
    ('controller', 'Controller', 'prod', args.swarm_runtime_id, controllers),
    ('postgresql', 'PostgreSQL', 'prod', args.swarm_runtime_id, [postgres]),
    ('nginx', 'NGINX', 'prod', args.container_runtime_id, [production]),
    ('nginx-sandbox', 'NGINX Sandbox', 'sandbox', args.container_runtime_id, [sandbox]),
]:
    applications.append(dict(key=key, name=name, environment=environment, runtime_id=runtime, object_ids=ids))
print(json.dumps(dict(runtime_id=args.swarm_runtime_id, cluster_id=cluster,
                     services=[dict(object_id=value, environment='prod') for value in controllers + [postgres]],
                     applications=applications), indent=2))
