import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, isAbsolute, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { validateCoveragePolicy } from "./policy.mjs";
import { initialBaseline } from "./baseline.mjs";

const repository = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const slash = (value) => value.replaceAll("\\", "/");
const hash = (value) => createHash("sha256").update(value).digest("hex");
export function inBackendScope(file, includes) {
  if (!Array.isArray(includes) || includes.length === 0)
    throw new Error("Backend source scope is missing");
  return includes.some(pattern => {
    if (typeof pattern !== "string" || !pattern.endsWith("/**/*.go") || pattern.includes("..") || isAbsolute(pattern))
      throw new Error("Invalid backend source scope");
    return file.startsWith(pattern.slice(0, -"**/*.go".length));
  });
}
const git = (root, args) =>
  execFileSync("git", args, {
    cwd: root,
    encoding: "utf8",
    maxBuffer: 32 << 20,
  }).trimEnd();

export function classify(file, content = "") {
  if (
    /\.(?:test|spec|e2e)\.[cm]?[jt]sx?$|_test\.go$/.test(file) ||
    /(?:^|\/)(?:tests?|testdata|enginetest|fixtures)\//.test(file) ||
    /^scripts\/(?:e2e-|test-)/.test(file)
  )
    return "test";
  if (
    /^api\/gen\/|^frontend\/src\/shared\/i18n\/generated\//.test(file) ||
    /\.d\.ts$/.test(file) ||
    (file.endsWith(".go") &&
      /^\/\/ Code generated .* DO NOT EDIT\.$/m.test(
        content.split(/^package /m)[0],
      ))
  )
    return "generated";
  if (/^(?:internal|cmd)\/.*\.go$/.test(file)) return "go";
  if (/^frontend\/src\/.*\.(?:ts|tsx|js|jsx|vue)$/.test(file))
    return "frontend";
  if (/\.(?:go|[cm]?js|[jt]sx?|vue|py|sh)$/.test(file)) return "unsupported";
  return "non-executable";
}

export function changedRanges(diff) {
  const ranges = [];
  for (const line of diff.split("\n")) {
    const match = /^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@/.exec(line);
    if (!match) continue;
    const start = Number(match[1]),
      count = match[2] === undefined ? 1 : Number(match[2]);
    // Pure removals have no newly executable lines. Surrounding edited code is
    // still covered by its own added/replaced hunk and the full regression gate.
    if (count > 0) ranges.push([start, start + count - 1]);
  }
  return ranges;
}

export function collectChanges(root, requestedBase = "HEAD") {
  const head = git(root, ["rev-parse", "--verify", "HEAD"]);
  const initial = initialBaseline(root, requestedBase);
  const base = initial
    ? "EMPTY"
    : git(root, [
        "merge-base",
        git(root, ["rev-parse", "--verify", `${requestedBase}^{commit}`]),
        head,
      ]);
  const tracked = git(
    root,
    initial
      ? ["ls-files", "-z"]
      : [
          "diff",
          "--no-ext-diff",
          "--no-renames",
          "--name-only",
          "-z",
          base,
          "--",
        ],
  )
    .split("\0")
    .filter(Boolean);
  const untracked = git(root, [
    "ls-files",
    "--others",
    "--exclude-standard",
    "-z",
  ])
    .split("\0")
    .filter(Boolean);
  const newFiles = new Set(untracked);
  const changes = [...new Set([...tracked, ...untracked])]
    .sort()
    .map((file) => {
      const path = resolve(root, file);
      if (!existsSync(path))
        return {
          file,
          deleted: true,
          ranges: [],
          sha256: null,
          kind: "deleted",
        };
      const data = readFileSync(path),
        content = data.toString("utf8");
      const ranges =
        initial || newFiles.has(file)
          ? [[1, content.split("\n").length]]
          : changedRanges(
              git(root, [
                "diff",
                "--no-ext-diff",
                "--no-renames",
                "--unified=0",
                base,
                "--",
                file,
              ]),
            );
      return {
        file,
        deleted: false,
        ranges,
        sha256: hash(data),
        kind: classify(file, content),
      };
    });
  return { base, head, changes };
}

export function parseGoProfile(text, module) {
  const lines = text.trim().split("\n");
  if (!/^mode: (?:set|count|atomic)$/.test(lines.shift() || ""))
    throw new Error("Invalid Go statement profile");
  const blocks = new Map();
  for (const line of lines) {
    const match = /^(.*):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$/.exec(line);
    if (!match) throw new Error("Invalid Go coverage block");
    if (!match[1].startsWith(module + "/"))
      throw new Error("Go profile contains a file outside the NSC module");
    const file = match[1].slice(module.length + 1);
    const key = match.slice(1, 7).join(":");
    const previous = blocks.get(key);
    blocks.set(key, {
      file,
      start: Number(match[2]),
      end: Number(match[4]),
      total: Number(match[6]),
      hit: Number(match[7]) > 0 || Boolean(previous?.hit),
    });
  }
  return [...blocks.values()];
}

const overlaps = (ranges, start, end = start) =>
  ranges.some(([a, b]) => a <= end && b >= start);
const counter = () => ({ covered: 0, total: 0, percent: 100 });

export function evaluate(changes, { go, statements, frontend }, root, minimum) {
  if (go.tests_passed !== true || !go.sources || !Array.isArray(go.branches))
    throw new Error("Missing or unsuccessful Go branch evidence");
  const result = {
    statements: counter(),
    branches: counter(),
    uncovered: [],
    unsupported: [],
    excluded: [],
  };
  const count = (metric, file, line, total, covered, detail) => {
    result[metric].total += total;
    result[metric].covered += covered;
    if (total !== covered)
      result.uncovered.push({ file, line, metric, covered, total, detail });
  };
  const frontendFiles = new Map(
    Object.entries(frontend).map(([key, value]) => [
      slash(isAbsolute(key) ? relative(root, key) : key),
      value,
    ]),
  );
  for (const change of changes) {
    const { file, kind, ranges } = change;
    if (
      change.deleted ||
      ranges.length === 0 ||
      kind === "test" ||
      kind === "generated" ||
      kind === "non-executable"
    ) {
      result.excluded.push({
        file,
        reason: change.deleted
          ? "deleted"
          : ranges.length === 0
            ? "no added executable content"
            : kind,
      });
      continue;
    }
    if (kind === "unsupported") {
      result.unsupported.push({
        file,
        reason:
          "No coverage adapter for this executable source; cannot assume coverage",
      });
      continue;
    }
    if (kind === "go") {
      if (!Object.hasOwn(go.sources, file))
        throw new Error(`Go branch inventory is missing ${file}`);
      const blocks = statements.filter((block) => block.file === file);
      if (go.sources[file] && blocks.length === 0)
        throw new Error(`Go statement evidence is missing ${file}`);
      for (const block of blocks) {
        if (overlaps(ranges, block.start, block.end))
          count(
            "statements",
            file,
            block.start,
            block.total,
            block.hit ? block.total : 0,
            "Go executable statement block",
          );
      }
      for (const branch of go.branches.filter(
        (branch) => branch.file === file,
      )) {
        if (!Number.isInteger(branch.end_line) || branch.end_line < branch.line)
          throw new Error(`Go branch span is missing ${file}:${branch.line}`);
        if (overlaps(ranges, branch.line, branch.end_line))
          count(
            "branches",
            file,
            branch.line,
            branch.arms.length,
            branch.arms.filter((arm) => branch.hits[arm] === true).length,
            branch.kind,
          );
      }
    } else if (kind === "frontend") {
      const report = frontendFiles.get(file);
      if (!report?.statementMap || !report.branchMap || !report.s || !report.b)
        throw new Error(`Frontend coverage evidence is missing ${file}`);
      for (const [id, location] of Object.entries(report.statementMap)) {
        if (overlaps(ranges, location.start.line, location.end.line))
          count(
            "statements",
            file,
            location.start.line,
            1,
            report.s[id] > 0 ? 1 : 0,
            "Istanbul executable statement",
          );
      }
      for (const [id, branch] of Object.entries(report.branchMap)) {
        const locations = [branch.loc, ...branch.locations].filter(
          (loc) => loc?.start?.line,
        );
        if (
          !locations.some((loc) =>
            overlaps(ranges, loc.start.line, loc.end.line),
          )
        )
          continue;
        if (
          !Array.isArray(report.b[id]) ||
          report.b[id].length !== branch.locations.length
        )
          throw new Error(
            `Frontend branch evidence is incomplete ${file}:${id}`,
          );
        count(
          "branches",
          file,
          locations[0].start.line,
          report.b[id].length,
          report.b[id].filter((hit) => hit > 0).length,
          branch.type,
        );
      }
    } else throw new Error(`Unknown coverage scope for ${file}`);
  }
  for (const metric of ["statements", "branches"]) {
    const c = result[metric];
    c.percent = c.total ? (100 * c.covered) / c.total : 100;
  }
  result.passed =
    result.unsupported.length === 0 &&
    ["statements", "branches"].every(metric => result[metric].percent >= minimum[metric]);
  return result;
}

export function verifySnapshot(before, after) {
  if (JSON.stringify(before) !== JSON.stringify(after))
    throw new Error(
      "NSC source or comparison base changed after coverage started; regenerate all reports",
    );
}

export function main(args = process.argv.slice(2), root = repository) {
  const { values } = parseArgs({
    args,
    options: {
      snapshot: { type: "boolean" },
      base: { type: "string" },
      thresholds: { type: "boolean" },
      backend: { type: "boolean" },
    },
    strict: true,
  });
  const policy = JSON.parse(
    readFileSync(resolve(root, ".agents/rules/coverage.json"), "utf8"),
  );
  validateCoveragePolicy(policy);
  if (values.thresholds) {
    console.log(policy.backend.branches, policy.frontend.branches);
    return true;
  }
  const backendOnly = values.backend || policy.active_scope === "backend-service";
  if (backendOnly) inBackendScope("", policy.backend.include);
  const current = collectChanges(
    root,
    values.base ?? process.env.NSC_COVERAGE_BASE ?? "HEAD",
  );
  const snapshotPath = resolve(root, "coverage/source-snapshot.json");
  mkdirSync(dirname(snapshotPath), { recursive: true });
  if (values.snapshot) {
    writeFileSync(snapshotPath, JSON.stringify(current, null, 2) + "\n");
    return true;
  }
  verifySnapshot(JSON.parse(readFileSync(snapshotPath, "utf8")), current);
  const read = (file) => readFileSync(resolve(root, file), "utf8");
  const module = /^module\s+(\S+)/m.exec(read("go.mod"))?.[1];
  if (!module) throw new Error("NSC Go module identity is unavailable");
  if (
    JSON.parse(read("coverage/go-statements-passed.json")) !== true ||
    (!backendOnly && JSON.parse(read("coverage/frontend-tests.json")).success !== true)
  )
    throw new Error("Coverage cannot pass while Go or frontend tests fail");
  const reports = {
    go: JSON.parse(read("coverage/go-branches.json")),
    statements: parseGoProfile(read("coverage/go-statements.out"), module),
    frontend: backendOnly ? {} : JSON.parse(read("frontend/coverage/coverage-final.json")),
  };
  if (current.base === "EMPTY" && policy.initial_commit === "full-coverage-baseline") {
    if (reports.go.tests_passed !== true || JSON.parse(read("coverage/go-branches-passed.json")) !== true ||
        !Number.isFinite(reports.go.percent) || reports.go.percent < policy.backend.branches)
      throw new Error("Initial baseline requires successful full backend branch coverage");
    const result = { base: current.base, head: current.head, status: "NOT_APPLICABLE", reason: "Initial commit: establishing baseline", passed: null,
      admission: "FULL_REGRESSION_REQUIRED", backend_branches: reports.go.percent, frontend_gate: "REQUIRED_SEPARATELY", minimum: policy.incremental };
    writeFileSync(resolve(root, "coverage/incremental.json"), JSON.stringify(result, null, 2) + "\n");
    console.log("NSC incremental coverage: NOT_APPLICABLE (initial commit: establishing baseline); full regression gates remain required");
    return true;
  }
  const selected = backendOnly
    ? current.changes.filter(change => inBackendScope(change.file, policy.backend.include))
    : current.changes;
  const result = {
    base: current.base,
    head: current.head,
    minimum: policy.incremental,
    scope: backendOnly ? "backend-service" : "all",
    outside_scope: backendOnly ? current.changes.filter(change => !inBackendScope(change.file, policy.backend.include)).map(change => change.file) : [],
    ...evaluate(selected, reports, root, policy.incremental),
  };
  writeFileSync(
    resolve(root, "coverage/incremental.json"),
    JSON.stringify(result, null, 2) + "\n",
  );
  for (const metric of ["statements", "branches"])
    console.log(
      `NSC incremental ${metric}: ${result[metric].covered}/${result[metric].total} (${result[metric].percent.toFixed(2)}%), required ${policy.incremental[metric]}%`,
    );
  if (result.unsupported.length)
    console.error(
      `${result.unsupported.length} changed executable files have no coverage adapter; see coverage/incremental.json`,
    );
  return result.passed;
}

if (
  process.argv[1] &&
  resolve(process.argv[1]) === fileURLToPath(import.meta.url)
) {
  try {
    if (!main()) process.exitCode = 1;
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
