#!/usr/bin/env python3
"""Produce explicit Gateway registration from operator-selected Swarm service IDs.

This read-only helper does not update a service, a database or a config file.
Merge its JSON output under controller.system_project in every Controller config.
Names are accepted only to ask Docker for the object's real ID, never for discovery
ownership inference. A replacement object requires regenerating this registration.
"""
import argparse
import json
import subprocess

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--runtime-id', required=True)
parser.add_argument('--production-service', action='append', default=[])
parser.add_argument('--sandbox-service', action='append', default=[])
args = parser.parse_args()

def docker(*command):
    return json.loads(subprocess.check_output(['docker', *command], text=True, timeout=20))

info = docker('info', '--format', '{{json .Swarm}}')
cluster = info.get('Cluster', {}).get('ID')
if not cluster or not info.get('ControlAvailable'):
    parser.error('run on a Manager in the intended cluster')
services = []
seen = set()
for environment, selected in [('prod', args.production_service), ('sandbox', args.sandbox_service)]:
    for reference in selected:
        # Formatting prevents deployment environment variables or Secrets appearing
        # in output. Explicit references are Docker CLI arguments, never shell code.
        service = docker('service', 'inspect', '--format', '{{json .ID}}', reference)
        if service in seen:
            parser.error('each object must be registered exactly once')
        seen.add(service)
        services.append({'object_id': service, 'environment': environment})
if not any(s['environment'] == 'prod' for s in services):
    parser.error('select at least one production service')
print(json.dumps({'runtime_id': args.runtime_id, 'cluster_id': cluster, 'services': services}, indent=2))
