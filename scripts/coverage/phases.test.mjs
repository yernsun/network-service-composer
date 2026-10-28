import test from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync, spawnSync } from 'node:child_process'
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execute } from '../harness/run.mjs'

const repository = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

function fixture(t) {
  const root = mkdtempSync(resolve(tmpdir(), 'nsc-coverage-phases-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const write = (file, data, mode = 0o600) => {
    mkdirSync(dirname(resolve(root, file)), { recursive: true })
    writeFileSync(resolve(root, file), data, { mode })
  }
  for (const file of ['scripts/check-coverage.sh', 'scripts/coverage/check-incremental.mjs', 'scripts/coverage/check-incremental.test.mjs', 'scripts/coverage/baseline.mjs', 'scripts/coverage/policy.mjs', '.agents/rules/coverage.json']) {
    mkdirSync(dirname(resolve(root, file)), { recursive: true })
    copyFileSync(resolve(repository, file), resolve(root, file))
  }
  write('.gitignore', 'coverage/\n')
  write('go.mod', 'module github.com/yernsun/network-service-composer\n\ngo 1.27\n')
  write('internal/example.go', 'package example\nfunc Choose(b bool) bool { if b { return true }; return false }\n')
  // Only orchestration is replaced. The real snapshot/evidence checker and its
  // behavior tests execute; no business tests or network probes use this fake.
  write('fake-go.mjs', `#!/usr/bin/env node
import { appendFileSync, readFileSync, writeFileSync } from 'node:fs'
const args = process.argv.slice(2)
appendFileSync('coverage/calls.ndjson', JSON.stringify(args) + '\\n')
if (args[0] === 'run') {
  if (process.env.NSC_PHASE_FIXTURE_BLOCK === '1') await new Promise(() => setInterval(() => {}, 1000))
  const failed = process.env.NSC_PHASE_FIXTURE_BRANCH_FAIL === '1'
  writeFileSync('coverage/go-branches.json', JSON.stringify({ tests_passed: true, percent: failed ? 75 : 100,
    sources: { 'internal/example.go': true }, branches: [{ file: 'internal/example.go', line: 2, end_line: 2,
      arms: ['true', 'false'], hits: { true: true, false: true } }] }))
  process.exitCode = failed ? 1 : 0
} else if (args[0] === 'test') {
  writeFileSync('coverage/go-statements.out', 'mode: set\\ngithub.com/yernsun/network-service-composer/internal/example.go:2.1,2.70 1 1\\n')
  if (process.env.NSC_PHASE_FIXTURE_DRIFT === '1') appendFileSync('internal/example.go', '// changed during native measurement\\n')
  process.exitCode = process.env.NSC_PHASE_FIXTURE_NATIVE_FAIL === '1' ? 1 : 0
} else process.exitCode = 2
`, 0o700)
  const git = args => execFileSync('git', args, { cwd: root, stdio: 'pipe' })
  git(['init', '-q']); git(['add', '.'])
  git(['-c', 'user.name=NSC test', '-c', 'user.email=test@example.invalid', 'commit', '-qm', 'fixture'])
  const env = { ...process.env, GO: resolve(root, 'fake-go.mjs'), NODE: process.execPath, NSC_TEST_ISOLATED_NETWORK: '1', NSC_COVERAGE_BASE: 'HEAD' }
  for (const key of ['NSC_TEST_POSTGRES_DSN', 'NSC_TEST_MYSQL_DSN', 'NSC_PHASE_FIXTURE_BRANCH_FAIL', 'NSC_PHASE_FIXTURE_NATIVE_FAIL', 'NSC_PHASE_FIXTURE_DRIFT', 'NSC_PHASE_FIXTURE_BLOCK']) delete env[key]
  const run = (phase, extra = {}) => spawnSync('bash', ['scripts/check-coverage.sh', ...(phase ? [phase] : [])], { cwd: root, env: { ...env, ...extra }, encoding: 'utf8', timeout: 60000 })
  const json = file => JSON.parse(readFileSync(resolve(root, 'coverage', file), 'utf8'))
  const calls = () => readFileSync(resolve(root, 'coverage/calls.ndjson'), 'utf8').trim().split('\n').map(JSON.parse)
  return { root, env, write, run, json, calls }
}

test('default backend gate completes both measurements and admission with unchanged scope', t => {
  const f = fixture(t), result = f.run()
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(f.calls().map(args => args[0]), ['run', 'test'])
  assert.equal(f.json('go-branches-passed.json'), true)
  assert.equal(f.json('go-statements-passed.json'), true)
  assert.equal(f.json('incremental.json').passed, true)
  const native = f.calls()[1]
  assert.ok(native.includes('-coverpkg=./internal/...,./cmd/nsc-controller,./cmd/nsc-agent'))
  assert.deepEqual(native.slice(-3), ['./internal/...', './cmd/nsc-controller', './cmd/nsc-agent'])
})

test('aggregate branch failure retains evidence and does not prevent native measurement', t => {
  const f = fixture(t), result = f.run(undefined, { NSC_PHASE_FIXTURE_BRANCH_FAIL: '1' })
  assert.equal(result.status, 1)
  assert.deepEqual(f.calls().map(args => args[0]), ['run', 'test'])
  assert.equal(f.json('go-branches.json').tests_passed, true)
  assert.equal(f.json('go-branches-passed.json'), false)
  assert.equal(f.json('go-statements-passed.json'), true)
  assert.equal(f.json('incremental.json').passed, true)
  assert.equal(f.run('report').status, 1, 'incremental success must not waive aggregate failure')
})

test('native failure and source drift cannot promote partial coverage to success', t => {
  for (const extra of [{ NSC_PHASE_FIXTURE_NATIVE_FAIL: '1' }, { NSC_PHASE_FIXTURE_DRIFT: '1' }]) {
    const f = fixture(t)
    assert.equal(f.run('prepare').status, 0)
    assert.equal(f.run('branches').status, 0)
    const result = f.run('statements', extra)
    assert.equal(result.status, 1)
    assert.equal(f.json('go-statements-passed.json'), false)
    assert.ok(existsSync(resolve(f.root, 'coverage/go-statements.out')))
    assert.equal(f.run('report').status, 1)
  }
})

test('prepare removes stale reports; phases without preparation fail before Go executes', t => {
  const f = fixture(t)
  for (const file of ['go-branches.json', 'go-branches-passed.json', 'go-statements.out', 'go-statements-passed.json', 'incremental.json', 'source-snapshot.json']) f.write('coverage/' + file, 'true\n')
  assert.equal(f.run('prepare').status, 0)
  for (const file of ['go-branches.json', 'go-branches-passed.json', 'go-statements.out', 'go-statements-passed.json', 'incremental.json']) assert.ok(!existsSync(resolve(f.root, 'coverage', file)))
  assert.equal(f.run('report').status, 1)
  rmSync(resolve(f.root, 'coverage/source-snapshot.json'))
  assert.equal(f.run('branches').status, 1)
  assert.equal(f.run('statements').status, 1)
  assert.ok(!existsSync(resolve(f.root, 'coverage/calls.ndjson')))
})

test('namespace re-execution forwards the selected phase', t => {
  const f = fixture(t)
  // Scoped command fixtures verify forwarding only; actual isolation is
  // exercised by the Linux coverage entry point with real business tests.
  f.write('bin/unshare', '#!/bin/sh\nshift\nshift\nexec "$@"\n', 0o700)
  f.write('bin/ip', '#!/bin/sh\nexit 0\n', 0o700)
  assert.equal(f.run('prepare').status, 0)
  const result = f.run('branches', { NSC_TEST_ISOLATED_NETWORK: '', PATH: resolve(f.root, 'bin') + ':' + f.env.PATH })
  assert.equal(result.status, 0, result.stderr)
  assert.deepEqual(f.calls().map(args => args[0]), ['run'])
  assert.ok(!existsSync(resolve(f.root, 'coverage/go-statements.out')))
})

test('timed-out branch process leaves failure evidence for independent native and report phases', async t => {
  const f = fixture(t)
  assert.equal(f.run('prepare').status, 0)
  const outcome = await execute(['bash', 'scripts/check-coverage.sh', 'branches'], { root: f.root, env: { ...f.env, NSC_PHASE_FIXTURE_BLOCK: '1' }, log: resolve(f.root, 'coverage/blocked.log'), timeout: 1000 })
  assert.equal(outcome.status, 'FAIL'); assert.equal(outcome.timedOut, true)
  assert.equal(f.json('go-branches-passed.json'), false)
  assert.equal(f.run('statements').status, 0)
  assert.equal(f.run('report').status, 1)
  assert.ok(!existsSync(resolve(f.root, 'coverage/go-branches.json')))
})
