import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  mkdtempSync,
  mkdirSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { test } from "node:test";
import {
  changedRanges,
  classify,
  collectChanges,
  evaluate as evaluateWithMinimum,
  inBackendScope,
  main,
  parseGoProfile,
  verifySnapshot,
} from "./check-incremental.mjs";

import { validateCoveragePolicy } from "./policy.mjs";

// Strict fixture settings deliberately exercise complete/incomplete evidence;
// production requirements always come from coverage.json.
const evaluate = (changes, reports, root) =>
  evaluateWithMinimum(changes, reports, root, { statements: 100, branches: 100 });

const module = "github.com/yernsun/network-service-composer";
const root = "/nsc";
const goFile = "internal/example/example.go";
const vueFile = "frontend/src/Example.vue";
const change = (file, kind, ranges = [[2, 2]]) => ({ file, kind, ranges });
function reports() {
  return {
    go: {
      tests_passed: true,
      sources: { [goFile]: true },
      branches: [
        {
          file: goFile,
          line: 2,
          end_line: 5,
          kind: "if",
          arms: ["true", "false"],
          hits: { true: true, false: true },
        },
      ],
    },
    statements: [{ file: goFile, start: 2, end: 3, total: 2, hit: true }],
    frontend: {
      [resolve(root, vueFile)]: {
        statementMap: { 0: { start: { line: 2 }, end: { line: 3 } } },
        s: { 0: 1 },
        branchMap: {
          0: {
            type: "if",
            loc: { start: { line: 2 }, end: { line: 5 } },
            locations: [
              { start: { line: 2 }, end: { line: 3 } },
              { start: { line: 4 }, end: { line: 5 } },
            ],
          },
        },
        b: { 0: [1, 1] },
      },
    },
  };
}

test("NSC policy is valid and retains backend service scope", () => {
  const policy = validateCoveragePolicy(JSON.parse(
    readFileSync(new URL("../../.agents/rules/coverage.json", import.meta.url)),
  ));
  assert.equal(policy.active_scope, "backend-service");
  assert.deepEqual(policy.backend.include, ["internal/**/*.go", "cmd/nsc-controller/**/*.go", "cmd/nsc-agent/**/*.go"]);
});


test("backend admission excludes helpers but retains in-service executable changes", () => {
  const includes = ["internal/**/*.go", "cmd/nsc-controller/**/*.go", "cmd/nsc-agent/**/*.go"];
  for (const file of ["internal/core/application.go", "internal/marketplace/manifest.go", "internal/core/future.ts", "cmd/nsc-agent/main.go"])
    assert.equal(inBackendScope(file, includes), true);
  for (const file of ["frontend/src/App.vue", "scripts/check-coverage.sh", "tools/branchcoverage/main.go", "cmd/nsc-market/main.go", "internal-other/helper.go"])
    assert.equal(inBackendScope(file, includes), false);
  for (const invalid of [[], ["../outside/**/*.go"], ["/tmp/**/*.go"], ["internal/*.go"]])
    assert.throws(() => inBackendScope("internal/a.go", invalid));
});

test("backend-only evidence needs no frontend reports and does not hide missing business branches", t => {
  const { dir, write } = fixture(t);
  write(".agents/rules/coverage.json", JSON.stringify({ active_scope: "backend-service", frontend: { branches: 80 }, backend: { branches: 80, include: ["internal/**/*.go"] }, incremental: { statements: 100, branches: 100 } }));
  write(goFile, "package example\nfunc Example() int { return 2 }\n");
  write("tools/unmeasured.py", "raise RuntimeError('helper')\n");
  write("frontend/src/unmeasured.ts", "throw new Error('frontend')\n");
  const invoke = args => main(["--base", "HEAD", ...args], dir);
  invoke(["--snapshot"]);
  const data = reports();
  write("coverage/go-branches.json", JSON.stringify(data.go));
  write("coverage/go-statements.out", `mode: atomic\n${module}/${goFile}:2.1,2.39 1 1\n`);
  write("coverage/go-statements-passed.json", "true");
  assert.equal(invoke([]), true);
  const result = JSON.parse(readFileSync(join(dir, "coverage/incremental.json")));
  assert.equal(result.scope, "backend-service");
  assert.ok(result.outside_scope.includes("tools/unmeasured.py"));
  assert.equal(result.unsupported.length, 0);
  data.go.branches[0].hits.false = false;
  write("coverage/go-branches.json", JSON.stringify(data.go));
  assert.equal(invoke([]), false);
});

test("diff hunks retain changed lines and distinguish pure removal", () => {
  assert.deepEqual(
    changedRanges(
      "@@ -1 +2 @@\n-a\n+b\n@@ -10,3 +11,4 @@ text\n@@ -20,2 +25,0 @@",
    ),
    [
      [2, 2],
      [11, 14],
    ],
  );
  assert.deepEqual(changedRanges("no changes"), []);
});

test("classification does not silently drop unmeasured executable sources", () => {
  for (const file of [
    "tools/publish.go",
    "scripts/release.py",
    "scripts/launch.sh",
    "scripts/release.mjs",
  ])
    assert.equal(classify(file), "unsupported");
  for (const file of [
    "frontend/tests/example.spec.ts",
    "internal/core/example_test.go",
    "scripts/e2e-market.py",
    "internal/runtime/engine/enginetest/fake.go",
  ])
    assert.equal(classify(file), "test");
  assert.equal(classify("api/gen/schema.go"), "generated");
  assert.equal(classify("frontend/src/shared/api/schema.d.ts"), "generated");
  assert.equal(
    classify(
      goFile,
      "// Code generated by test. DO NOT EDIT.\npackage example",
    ),
    "generated",
  );
  assert.equal(
    classify(
      goFile,
      "// Mentioning Code generated is not a marker.\npackage example",
    ),
    "go",
  );
  assert.equal(classify(vueFile), "frontend");
  assert.equal(classify("docs/test-coverage.md"), "non-executable");
});

test("Go statement profiles merge repeated evidence and reject malformed or foreign records", () => {
  const line = `${module}/${goFile}:2.1,3.5 2`;
  const blocks = parseGoProfile(`mode: atomic\n${line} 0\n${line} 1\n`, module);
  assert.deepEqual(blocks, [
    { file: goFile, start: 2, end: 3, total: 2, hit: true },
  ]);
  assert.deepEqual(parseGoProfile("mode: set\n", module), []);
  assert.throws(
    () => parseGoProfile("not coverage", module),
    /Invalid Go statement/,
  );
  assert.throws(
    () => parseGoProfile("mode: set\nbad", module),
    /Invalid Go coverage block/,
  );
  assert.throws(
    () => parseGoProfile("mode: count\nother/a.go:1.1,2.1 1 1", module),
    /outside the NSC/,
  );
});

test("touched executable statements and every affected branch require coverage", () => {
  const data = reports();
  let result = evaluate(
    [change(goFile, "go"), change(vueFile, "frontend")],
    data,
    root,
  );
  assert.equal(result.passed, true);
  assert.deepEqual(result.statements, { covered: 3, total: 3, percent: 100 });
  assert.deepEqual(result.branches, { covered: 4, total: 4, percent: 100 });
  data.go.branches[0].hits.false = false;
  data.statements[0].hit = false;
  data.frontend[resolve(root, vueFile)].b[0] = [1, 0];
  data.frontend[resolve(root, vueFile)].s[0] = 0;
  result = evaluate(
    [change(goFile, "go"), change(vueFile, "frontend")],
    data,
    root,
  );
  assert.equal(result.passed, false);
  assert.equal(result.statements.percent, 0);
  assert.equal(result.branches.percent, 50);
  assert.equal(result.uncovered.length, 4);
  // Editing a branch body requires both arms, even when the condition line did not change.
  result = evaluate(
    [change(goFile, "go", [[4, 4]]), change(vueFile, "frontend", [[4, 4]])],
    data,
    root,
  );
  assert.equal(result.branches.total, 4);
  assert.equal(result.passed, false);
});

test("unaffected decisions and non-executable changes do not inflate the denominator", () => {
  const data = reports();
  data.frontend[resolve(root, vueFile)].branchMap[0].loc = undefined;
  const result = evaluate(
    [
      change(goFile, "go", [[20, 20]]),
      change(vueFile, "frontend", [[20, 20]]),
      change("docs/a.md", "non-executable"),
      change("a_test.go", "test"),
      change("schema.d.ts", "generated"),
      { ...change("old.go", "go"), deleted: true },
      change(goFile, "go", []),
    ],
    data,
    root,
  );
  assert.equal(result.passed, true);
  assert.equal(result.statements.total, 0);
  assert.equal(result.branches.total, 0);
  assert.equal(result.excluded.length, 5);
});

test("missing evidence and unsupported executable files cannot become a green result", () => {
  const result = evaluate(
    [change("scripts/release.py", "unsupported")],
    reports(),
    root,
  );
  assert.equal(result.passed, false);
  assert.equal(result.unsupported.length, 1);
  const cases = [
    [
      (data) => {
        data.go.tests_passed = false;
      },
      change(goFile, "go"),
      /unsuccessful Go/,
    ],
    [
      (data) => {
        delete data.go.sources[goFile];
      },
      change(goFile, "go"),
      /inventory is missing/,
    ],
    [
      (data) => {
        data.statements = [];
      },
      change(goFile, "go"),
      /statement evidence is missing/,
    ],
    [
      (data) => {
        delete data.go.branches[0].end_line;
      },
      change(goFile, "go"),
      /branch span is missing/,
    ],
    [
      (data) => {
        data.frontend = {};
      },
      change(vueFile, "frontend"),
      /Frontend coverage evidence is missing/,
    ],
    [
      (data) => {
        data.frontend[resolve(root, vueFile)].b[0] = [1];
      },
      change(vueFile, "frontend"),
      /branch evidence is incomplete/,
    ],
    [() => {}, change("a", "unknown"), /Unknown coverage scope/],
  ];
  for (const [mutate, item, expected] of cases) {
    const data = reports();
    mutate(data);
    assert.throws(() => evaluate([item], data, root), expected);
  }
  const data = reports();
  data.go.sources[goFile] = false;
  data.statements = [];
  data.go.branches = [];
  assert.equal(evaluate([change(goFile, "go")], data, root).passed, true);
});

test("snapshots reject changed source and changed comparison bases", () => {
  const before = { base: "a", changes: [{ file: goFile, sha256: "one" }] };
  verifySnapshot(before, structuredClone(before));
  assert.throws(
    () => verifySnapshot(before, { ...before, base: "b" }),
    /regenerate all reports/,
  );
  assert.throws(
    () =>
      verifySnapshot(before, {
        ...before,
        changes: [{ file: goFile, sha256: "two" }],
      }),
    /source or comparison base changed/,
  );
});

function fixture(t) {
  const dir = mkdtempSync(join(tmpdir(), "nsc-incremental-test-"));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  const run = (...args) =>
    execFileSync(
      "git",
      [
        "-c",
        "user.name=NSC Test",
        "-c",
        "user.email=nsc-test@example.invalid",
        ...args,
      ],
      { cwd: dir, stdio: "pipe" },
    );
  const write = (file, text) => {
    mkdirSync(join(dir, file, ".."), { recursive: true });
    writeFileSync(join(dir, file), text);
  };
  run("init", "-q");
  write(".gitignore", "coverage/\nfrontend/coverage/\n");
  write("go.mod", `module ${module}\n\ngo 1.27\n`);
  write(goFile, "package example\nfunc Example() int { return 1 }\n");
  write("old.txt", "removed\n");
  write(
    ".agents/rules/coverage.json",
    JSON.stringify({
      frontend: { branches: 80 },
      backend: { branches: 80 },
      incremental: { statements: 100, branches: 100 },
    }),
  );
  run("add", ".");
  run("commit", "-qm", "NSC coverage fixture");
  return { dir, run, write };
}

test("genuine initial admission records not-applicable without inventing incremental percentages", t => {
  const { dir, run, write } = fixture(t);
  write(".agents/rules/coverage.json", JSON.stringify({ initial_commit: "full-coverage-baseline", active_scope: "backend-service", frontend: { branches: 50 }, backend: { branches: 80, include: ["internal/**/*.go"] }, incremental: { statements: 90, branches: 90 } }));
  run("add", "."); run("commit", "--amend", "--no-edit", "-q");
  const invoke = args => main(["--base", "0".repeat(40), ...args], dir);
  invoke(["--snapshot"]);
  const data = reports(); data.go.percent = 84;
  write("coverage/go-branches.json", JSON.stringify(data.go));
  write("coverage/go-branches-passed.json", "true");
  write("coverage/go-statements.out", `mode: atomic\n${module}/${goFile}:2.1,2.39 1 1\n`);
  write("coverage/go-statements-passed.json", "true");
  assert.equal(invoke([]), true);
  const result = JSON.parse(readFileSync(join(dir, "coverage/incremental.json")));
  assert.equal(result.status, "NOT_APPLICABLE");
  assert.equal(result.passed, null);
  assert.equal(result.statements, undefined);
  data.go.percent = 79;
  write("coverage/go-branches.json", JSON.stringify(data.go));
  assert.throws(() => invoke([]), /full backend branch/);
});

test("Git comparison includes staged, unstaged, untracked, renamed and deleted files only in its fixture", (t) => {
  const { dir, run, write } = fixture(t);
  write(goFile, "package example\nfunc Example() int { return 2 }\n");
  run("add", goFile);
  write("untracked.ts", "export const value = 1\n");
  write("unstaged.sh", "#!/bin/sh\nexit 0\n");
  rmSync(join(dir, "old.txt"));
  let value = collectChanges(dir);
  assert.deepEqual(value.changes.find((c) => c.file === goFile).ranges, [
    [2, 2],
  ]);
  assert.equal(value.changes.find((c) => c.file === "old.txt").deleted, true);
  assert.equal(
    value.changes.find((c) => c.file === "untracked.ts").kind,
    "unsupported",
  );
  assert.throws(() => collectChanges(dir, "nonexistent-baseline"));
  renameSync(join(dir, goFile), join(dir, "renamed.go"));
  run("add", "-A");
  value = collectChanges(dir);
  assert.equal(value.changes.find((c) => c.file === goFile).deleted, true);
  assert.deepEqual(value.changes.find((c) => c.file === "renamed.go").ranges, [
    [1, 2],
  ]);
  assert.throws(() => collectChanges(dir, "0".repeat(40)), /unchanged committed source/);
});

test("CLI writes incremental evidence and refuses stale snapshots or failed tests", (t) => {
  const { dir, write } = fixture(t);
  // CI's NSC_COVERAGE_BASE belongs to the real repository, not this fixture.
  const invoke = (args) => main(["--base", "HEAD", ...args], dir);
  write(goFile, "package example\nfunc Example() int { return 2 }\n");
  assert.equal(invoke(["--snapshot"]), true);
  const data = reports();
  write("coverage/go-branches.json", JSON.stringify(data.go));
  write(
    "coverage/go-statements.out",
    `mode: atomic\n${module}/${goFile}:2.1,2.39 1 1\n`,
  );
  write("coverage/go-statements-passed.json", "true");
  write("coverage/frontend-tests.json", '{"success":true}');
  write("frontend/coverage/coverage-final.json", "{}");
  assert.equal(invoke([]), true);
  assert.equal(
    JSON.parse(readFileSync(join(dir, "coverage/incremental.json"))).passed,
    true,
  );
  write("coverage/go-statements-passed.json", "false");
  assert.throws(() => invoke([]), /tests fail/);
  write("coverage/go-statements-passed.json", "true");
  data.go.branches[0].hits.false = false;
  write("coverage/go-branches.json", JSON.stringify(data.go));
  assert.equal(invoke([]), false);
  write(goFile, "package example\nfunc Example() int { return 3 }\n");
  assert.throws(() => invoke([]), /source or comparison base changed/);
  write(
    ".agents/rules/coverage.json",
    '{"incremental":{"statements":99,"branches":100}}',
  );
  assert.throws(() => invoke(["--thresholds"]), /Invalid coverage threshold/);
});

test("policy validation rejects missing, nonnumeric and disabling thresholds", () => {
  const valid = { frontend: { branches: 80 }, backend: { branches: 80 }, incremental: { statements: 90, branches: 90 } };
  for (const [scope, metric] of [["frontend", "branches"], ["backend", "branches"], ["incremental", "statements"], ["incremental", "branches"]]) {
    for (const invalid of [undefined, null, "90", true, 0, -1, 101, NaN, Infinity]) {
      const policy = structuredClone(valid);
      policy[scope][metric] = invalid;
      assert.throws(() => validateCoveragePolicy(policy), /Invalid coverage threshold/);
    }
    for (const value of [0.5, 80, 90, 99, 100]) {
      const policy = structuredClone(valid);
      policy[scope][metric] = value;
      assert.doesNotThrow(() => validateCoveragePolicy(policy));
    }
  }
  assert.throws(() => validateCoveragePolicy(null), /threshold/);
  assert.throws(() => validateCoveragePolicy({ ...valid, active_scope: "typo" }), /scope/);
});

test("CLI applies configured incremental floors independently and keeps uncovered evidence", t => {
  const { dir, write } = fixture(t);
  const policy = { active_scope: "backend-service", frontend: { branches: 80 }, backend: { branches: 80, include: ["internal/**/*.go"] }, incremental: { statements: 90, branches: 90 } };
  write(goFile, "package example\nfunc Example() int { return 2 }\n");
  const data = reports();
  data.go.branches[0].arms = Array.from({ length: 10 }, (_, i) => `arm-${i}`);
  data.go.branches[0].hits = Object.fromEntries(data.go.branches[0].arms.map((arm, i) => [arm, i < 9]));
  write("coverage/go-branches.json", JSON.stringify(data.go));
  write("coverage/go-statements.out", `mode: atomic\n${module}/${goFile}:2.1,2.20 9 1\n${module}/${goFile}:2.21,2.39 1 0\n`);
  write("coverage/go-statements-passed.json", "true");
  const run = () => {
    write(".agents/rules/coverage.json", JSON.stringify(policy));
    main(["--base", "HEAD", "--snapshot"], dir);
    return main(["--base", "HEAD"], dir);
  };
  assert.equal(run(), true);
  const result = JSON.parse(readFileSync(join(dir, "coverage/incremental.json")));
  for (const metric of ["statements", "branches"]) {
    assert.deepEqual(result[metric], { covered: 9, total: 10, percent: 90 });
    assert.ok(result.uncovered.some(item => item.metric === metric));
    policy.incremental[metric] = 90.01;
    assert.equal(run(), false, `${metric} below minimum must fail without rounding`);
    policy.incremental[metric] = 90;
  }
  write("internal/unmeasured.py", "raise RuntimeError('business logic')\n");
  assert.equal(run(), false, "numeric success must not hide unsupported business source");
  write("coverage/go-statements-passed.json", "false");
  assert.throws(run, /tests fail/);
});
