"""Run a complete, disjoint part of the Go race suite on an isolated CI runner."""
import os
import re
import subprocess
import sys

CORE_SHARDS = 4
CASE = re.compile(r"^(?:Test|Example|Fuzz)\w*$")


def core_partition(output, shard):
    if shard not in range(CORE_SHARDS):
        raise ValueError("invalid Core partition")
    cases = sorted(line for line in output.splitlines() if CASE.fullmatch(line))
    if len(cases) < CORE_SHARDS or len(set(cases)) != len(cases):
        raise ValueError("missing or duplicate Core test inventory")
    return cases[shard::CORE_SHARDS]


def run(shard, go="go", execute=subprocess.run):
    if shard not in [str(i) for i in range(CORE_SHARDS)] + ["other"]:
        raise ValueError("invalid race partition")

    def capture(args):
        return execute([go, *args], check=True, capture_output=True, text=True).stdout

    if shard == "other":
        core = capture(["list", "./internal/core"]).strip()
        packages = capture(["list", "./..."]).splitlines()
        if not core or packages.count(core) != 1 or len(packages) < 2:
            raise ValueError("incomplete package inventory")
        args = [package for package in packages if package != core]
    else:
        inventory = capture(["test", "-race", "-list", "^(Test|Example|Fuzz)", "./internal/core"])
        cases = core_partition(inventory, int(shard))
        print(f"Core race partition {shard}: {len(cases)} discovered cases", flush=True)
        args = ["-run", "^(" + "|".join(re.escape(case) for case in cases) + ")$", "./internal/core"]
    execute([go, "test", "-race", "-count=1", "-timeout=30m", *args], check=True)


if __name__ == "__main__":
    try:
        if len(sys.argv) != 2:
            raise ValueError("usage: race_shard.py <0|1|2|3|other>")
        run(sys.argv[1], os.environ.get("GO", "go"))
    except (ValueError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
