"""Exercise Docker's actual stack interpolation, independently of YAML parsing."""
import os
from pathlib import Path
import subprocess
import unittest


class StackRenderingTests(unittest.TestCase):
    def test_node_local_sources_survive_stack_conversion(self):
        root = Path(__file__).resolve().parents[1]
        env = dict(os.environ, NSC_CONTROLLER_IMAGE='example.test/controller@sha256:' + 'a' * 64,
                   NSC_GATEWAY_IMAGE='example.test/gateway@sha256:' + 'b' * 64,
                   NSC_DOCKER_GID='1000', NSC_NODE_DIR='/srv/nsc-fixture', NSC_INGRESS_NETWORK='fixture-overlay')
        result = subprocess.run(['docker', 'stack', 'config', '-c', str(root / 'deploy/swarm.yaml')],
                                env=env, capture_output=True, text=True, timeout=15, check=True)
        for name in ('controller.yaml', 'controller-secrets', 'agent.yaml', 'gateway-secrets', 'gateway-state', 'gateway-cache'):
            self.assertIn('source: /srv/nsc-fixture/' + name, result.stdout)
        self.assertNotIn(str(root), result.stdout)
        self.assertNotIn('${', result.stdout)
        self.assertIn('name: fixture-overlay', result.stdout)
        self.assertIn('mode: global', result.stdout)
        self.assertIn('failure_action: pause', result.stdout)


if __name__ == '__main__':
    unittest.main()
