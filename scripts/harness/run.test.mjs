import test from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { resolve } from 'node:path'
import { execute, retainCoverageEvidence, runSteps, snapshot, stepsFor } from './run.mjs'

function fixture(t) {
  const root = mkdtempSync(resolve(tmpdir(), 'nsc-harness-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  return { root, env: process.env, reportDir: resolve(root, 'reports'), fingerprint: () => ({ fingerprint: 'same' }) }
}
const step = (id, needs = []) => ({ id, needs, command: ['fixture', id] })

test('continues independent checks and blocks failed dependencies', async t => {
  const f = fixture(t), calls = []
  const report = await runSteps({ ...f, steps: [step('failed'), step('dependent', ['failed']), step('independent')], runner: async command => { calls.push(command[1]); return { status: command[1] === 'failed' ? 'FAIL' : 'PASS', exitCode: command[1] === 'failed' ? 1 : 0 } } })
  assert.equal(report.status, 'FAIL'); assert.deepEqual(calls, ['failed', 'independent'])
  assert.deepEqual(report.steps.map(s => s.status), ['FAIL', 'BLOCKED', 'PASS'])
  assert.equal(JSON.parse(readFileSync(resolve(f.reportDir, 'summary.json'))).steps.length, 3)
})

test('source changes, absent baseline and runner failures cannot pass', async t => {
  for (const problem of ['drift', 'baseline', 'runner', 'empty']) {
    const f = fixture(t); let n = 0
    const report = await runSteps({ ...f, steps: problem === 'empty' ? [] : [step('check')], fingerprint: () => { if (problem === 'baseline') throw new Error('missing baseline'); return problem === 'drift' ? ++n : 1 }, runner: async () => { if (problem === 'runner') throw new Error('runner failed'); return { status: 'PASS', exitCode: 0 } } })
    assert.notEqual(report.status, 'PASS', problem)
  }
})

test('successful checks produce a complete summary; cancellation retains evidence', async t => {
  const f = fixture(t), abort = new AbortController()
  const report = await runSteps({ ...f, steps: [step('first'), step('second')], signal: abort.signal, runner: async () => { abort.abort(); return { status: 'PASS', exitCode: 0 } } })
  assert.equal(report.steps[0].status, 'PASS'); assert.equal(report.steps[1].status, 'BLOCKED'); assert.equal(report.status, 'FAIL')
  const success = await runSteps({ ...f, steps: [step('ok')], runner: async () => ({ status: 'PASS', exitCode: 0 }) })
  assert.equal(success.status, 'PASS'); assert.ok(success.finishedAt); assert.ok(success.steps[0].log)
})

test('actual processes preserve exit, missing-tool, timeout and cancellation outcomes', async t => {
  const f = fixture(t)
  for (const [command, extra, status] of [
    [[process.execPath, '-e', 'console.log("evidence")'], {}, 'PASS'],
    [[process.execPath, '-e', 'process.exit(7)'], {}, 'FAIL'],
    [['/nonexistent-nsc-tool'], {}, 'BLOCKED'],
    [[process.execPath, '-e', 'setInterval(()=>{},1000)'], { timeout: 100 }, 'FAIL'],
    [[process.execPath, '-e', 'setInterval(()=>{},1000)'], { signal: AbortSignal.abort() }, 'FAIL'],
  ]) {
    const result = await execute(command, { ...f, log: resolve(f.root, 'step.log'), ...extra })
    assert.equal(result.status, status)
    if (extra.timeout) assert.equal(result.timedOut, true)
    if (status === 'PASS') assert.match(readFileSync(resolve(f.root, 'step.log'), 'utf8'), /evidence/)
    if (command[2] === 'process.exit(7)') assert.equal(result.exitCode, 7)
  }
})

test('fingerprint includes untracked edits and rejects a missing baseline', t => {
  const f = fixture(t)
  const git = args => execFileSync('git', args, { cwd: f.root, stdio: 'pipe' })
  git(['init', '-q']); git(['-c', 'user.name=NSC test', '-c', 'user.email=test@example.invalid', 'commit', '--allow-empty', '-qm', 'fixture'])
  const before = snapshot(f.root, {})
  writeFileSync(resolve(f.root, 'new.go'), 'package fixture\n')
  assert.notEqual(snapshot(f.root, {}).fingerprint, before.fingerprint)
  assert.throws(() => snapshot(f.root, { NSC_COVERAGE_BASE: '000000' }))
  assert.throws(() => snapshot(f.root, { NSC_COVERAGE_BASE: 'missing-branch' }))
})

test('check/full share checks; only full adds build and coverage, no deployment', () => {
  const env = { GO: '/chosen/go', NPM: '/chosen/npm' }
  const check = stepsFor('check', env, '/tmp/nsc-report'), full = stepsFor('full', env, '/tmp/nsc-report')
  assert.deepEqual(full.slice(0, check.length), check)
  assert.equal(full.filter(s => s.id.startsWith('coverage-')).length, 4)
  assert.ok(!check.some(s => s.id.startsWith('coverage-')))
  assert.ok(full.some(s => s.command[0] === '/chosen/go'))
  assert.ok(full.every(s => !s.command.includes('docker')))
})

test('coverage measurement failures keep independent measurements and final admission visible', async t => {
  const steps = stepsFor('full', {}, '/tmp/nsc-report').filter(s => s.id === 'frontend-build' || s.id.startsWith('coverage-'))
  for (const failed of ['coverage-branches', 'coverage-statements', 'coverage-prepare']) {
    const f = fixture(t), calls = []
    const report = await runSteps({ ...f, steps, runner: async command => {
      const phase = command.at(-1)
      calls.push(phase)
      return { status: phase === failed.replace('coverage-', '') ? 'FAIL' : 'PASS', exitCode: phase === failed.replace('coverage-', '') ? 1 : 0 }
    } })
    assert.equal(report.status, 'FAIL')
    if (failed === 'coverage-prepare') {
      assert.deepEqual(report.steps.slice(-3).map(s => s.status), ['BLOCKED', 'BLOCKED', 'BLOCKED'])
      assert.ok(!calls.includes('branches') && !calls.includes('statements') && !calls.includes('report'))
    } else {
      assert.deepEqual(calls.slice(-4), ['prepare', 'branches', 'statements', 'report'])
      assert.equal(report.steps.at(-1).status, 'PASS')
      assert.equal(report.steps.find(s => s.id === failed).status, 'FAIL')
    }
  }
})

test('coverage timeout and cancellation retain only attempted private evidence', t => {
  const f = fixture(t)
  mkdirSync(resolve(f.root, 'coverage'))
  writeFileSync(resolve(f.root, 'coverage/source-snapshot.json'), '{"source":"fixture"}\n')
  writeFileSync(resolve(f.root, 'coverage/go-branches-passed.json'), 'false\n')
  const timedOut = [{ id: 'coverage-prepare', status: 'PASS' }, { id: 'coverage-branches', status: 'FAIL', exitCode: null, timedOut: true }]
  const retained = retainCoverageEvidence(f.root, f.reportDir, timedOut)
  assert.deepEqual(retained, ['coverage/go-branches-passed.json', 'coverage/source-snapshot.json'])
  assert.equal(readFileSync(resolve(f.reportDir, 'evidence/coverage/go-branches-passed.json'), 'utf8'), 'false\n')
  assert.equal(statSync(resolve(f.reportDir, 'evidence/coverage/go-branches-passed.json')).mode & 0o777, 0o600)
  assert.deepEqual(retainCoverageEvidence(f.root, resolve(f.root, 'cancelled'), [{ id: 'coverage-prepare', status: 'BLOCKED', error: 'Run cancelled' }]), [])
})
