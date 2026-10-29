import json
from pathlib import Path
import subprocess
import unittest
from unittest.mock import Mock

from race_shard import CORE_SHARDS, core_partition, run


class RacePartitionTests(unittest.TestCase):
    def test_every_case_is_selected_once_including_examples_and_fuzz_seeds(self):
        cases = ["TestA", "TestAB", "TestZ", "FuzzInput", "Example", "ExampleValue"]
        inventory = "\n".join([*cases, "ok fixture/core 0.1s", "BenchmarkIgnored"])
        parts = [core_partition(inventory, i) for i in range(CORE_SHARDS)]
        self.assertCountEqual(sum(parts, []), cases)
        self.assertEqual(len(set(sum(parts, []))), len(cases))
        for part in parts:
            self.assertTrue(part)

    def test_bad_or_empty_inventory_fails(self):
        for inventory in ["", "TestA\nTestB", "TestA\nTestA\nTestB\nTestC"]:
            with self.assertRaises(ValueError):
                core_partition(inventory, 0)
        with self.assertRaises(ValueError):
            core_partition("", CORE_SHARDS)

    def test_core_uses_exact_names_and_keeps_race_enabled(self):
        execute = Mock(side_effect=[subprocess.CompletedProcess([], 0, "TestA\nTestAB\nTestC\nTestD\n"), None])
        run("0", execute=execute)
        args = execute.call_args.args[0]
        self.assertEqual(args, ["go", "test", "-race", "-count=1", "-timeout=30m", "-run", "^(TestA)$", "./internal/core"])
        self.assertTrue(execute.call_args.kwargs["check"])

    def test_other_partition_runs_every_other_package_without_test_filter(self):
        execute = Mock(side_effect=[subprocess.CompletedProcess([], 0, "module/internal/core\n"),
                                   subprocess.CompletedProcess([], 0, "module/api\nmodule/internal/core\nmodule/new\n"), None])
        run("other", execute=execute)
        self.assertEqual(execute.call_args.args[0], ["go", "test", "-race", "-count=1", "-timeout=30m", "module/api", "module/new"])

    def test_discovery_and_execution_failures_propagate(self):
        failure = subprocess.CalledProcessError(1, ["go", "test"])
        for responses in [[failure], [subprocess.CompletedProcess([], 0, "TestA\nTestB\nTestC\nTestD\n"), failure]]:
            with self.assertRaises(subprocess.CalledProcessError):
                run("0", execute=Mock(side_effect=responses))
        with self.assertRaises(ValueError):
            run("invalid", execute=Mock())

    def test_final_gate_requires_all_five_partitions(self):
        root = Path(__file__).resolve().parents[1]
        policy = json.loads((root / ".github/readiness-policy.json").read_text())
        jobs = [job for job in policy["jobs"] if job["workflowJob"] == "race"]
        self.assertCountEqual([job["id"] for job in jobs], [f"race-{i}" for i in range(CORE_SHARDS)] + ["race-other"])
        self.assertTrue(all(job["checks"] == ["race"] for job in jobs))
        workflow = (root / ".github/workflows/ci.yml").read_text()
        self.assertIn("shard: [0, 1, 2, 3, other]", workflow)
        self.assertIn('"race":"${{ needs.race.result }}"', workflow)


if __name__ == "__main__":
    unittest.main()
