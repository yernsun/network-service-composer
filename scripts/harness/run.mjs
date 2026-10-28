import { spawn, execFileSync } from 'node:child_process'
import { chmodSync, closeSync, copyFileSync, existsSync, mkdirSync, openSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { sha256 } from './contracts.mjs'
import { initialBaseline } from '../coverage/baseline.mjs'

export function snapshot(root, env = process.env) {
  const git = args => execFileSync('git', args, { cwd: root, encoding: 'utf8' }).trim()
  const commit = git(['rev-parse', '--verify', 'HEAD'])
  const base = env.NSC_COVERAGE_BASE || 'HEAD'
  const resolvedBase = initialBaseline(root, base) ? 'EMPTY' : git(['merge-base', base, 'HEAD'])
  const files = [...new Set(git(['ls-files', '-z', '--cached', '--others', '--exclude-standard']).split('\0').filter(Boolean))].sort()
  return { commit, base, resolvedBase, fingerprint: sha256(files.map(file => `${file}\0${existsSync(resolve(root, file)) ? sha256(readFileSync(resolve(root, file))) : 'DELETED'}\0`).join('')) }
}

export function execute(command, { root, env, log, timeout = 1_200_000, signal }) {
  return new Promise(resolveResult => {
    const fd = openSync(log, 'w', 0o600)
    let child
    try { child = spawn(command[0], command.slice(1), { cwd: root, env, stdio: ['ignore', fd, fd], detached: true }) }
    catch (error) { closeSync(fd); resolveResult({ status: 'BLOCKED', exitCode: null, error: error.message }); return }
    let timedOut = false
    let spawnError
    let force
    const stop = () => {
      if (!child.pid) return
      try { process.kill(-child.pid, 'SIGTERM') } catch { /* Already exited. */ }
      if (!force) force = setTimeout(() => { try { process.kill(-child.pid, 'SIGKILL') } catch { /* Already exited. */ } }, 1000)
    }
    const timer = setTimeout(() => { timedOut = true; stop() }, timeout)
    signal?.addEventListener('abort', stop, { once: true })
    if (signal?.aborted) stop()
    child.on('error', error => { spawnError = error.message })
    child.on('close', (code, termination) => {
      // Keep escalation scheduled even if the parent exits before a stubborn
      // descendant. The timeout belongs to the entire isolated process group.
      clearTimeout(timer); signal?.removeEventListener('abort', stop); closeSync(fd)
      resolveResult({ status: spawnError ? 'BLOCKED' : code === 0 && !timedOut && !signal?.aborted ? 'PASS' : 'FAIL', exitCode: code, signal: termination, timedOut, error: spawnError })
    })
  })
}

export function stepsFor(mode, env, reportDir) {
  const go = env.GO || 'go', npm = env.NPM || 'npm', node = env.NODE || process.execPath
  const step = (id, command, needs = []) => ({ id, command, needs })
  const steps = [
    step('contracts', [node, 'scripts/harness/contracts.mjs']),
    step('brand-contract', [node, 'scripts/check-brand.mjs']),
    step('repository-privacy', [node, 'scripts/check-repository-privacy.mjs', '--worktree']),
    step('privacy-tests', [node, '--test', 'scripts/check-repository-privacy.test.mjs']),
    step('harness-tests', [node, '--test', 'scripts/harness/contracts.test.mjs', 'scripts/harness/run.test.mjs', 'scripts/coverage/phases.test.mjs', 'scripts/coverage/baseline.test.mjs', 'scripts/release/export.test.mjs', 'scripts/release/legal-check.test.mjs']),
    step('go-format', [node, 'scripts/harness/format.mjs']),
    step('frontend-lint', [npm, '--prefix', 'frontend', 'run', 'lint']),
    step('frontend-types', [npm, '--prefix', 'frontend', 'run', 'typecheck']),
    step('api-contract', [npm, '--prefix', 'frontend', 'run', 'api:check']),
    step('i18n-contract', [npm, '--prefix', 'frontend', 'run', 'i18n:check']),
    step('protobuf-contract', ['make', 'verify-generated']),
    step('official-market', [go, 'run', './cmd/nsc-market', '--official', '--output', resolve(reportDir, 'market')]),
    // vet needs the embedded assets even in the check profile.
    step('frontend-build', [npm, '--prefix', 'frontend', 'run', 'build']),
    step('go-vet', [go, 'vet', './...'], ['frontend-build']),
  ]
  if (mode === 'full') steps.push(
    step('controller-build', [go, 'build', '-o', resolve(reportDir, 'nsc-controller'), './cmd/nsc-controller'], ['frontend-build']),
    step('agent-build', [go, 'build', '-o', resolve(reportDir, 'nsc-agent'), './cmd/nsc-agent']),
    step('coverage-prepare', ['bash', 'scripts/check-coverage.sh', 'prepare'], ['frontend-build']),
    step('coverage-branches', ['bash', 'scripts/check-coverage.sh', 'branches'], ['frontend-build', 'coverage-prepare']),
    step('coverage-statements', ['bash', 'scripts/check-coverage.sh', 'statements'], ['frontend-build', 'coverage-prepare']),
    step('coverage-report', ['bash', 'scripts/check-coverage.sh', 'report'], ['frontend-build', 'coverage-prepare']),
  )
  return steps
}

export async function runSteps({ root, env, reportDir, steps, fingerprint = () => snapshot(root, env), runner = execute, signal }) {
  mkdirSync(reportDir, { recursive: true })
  const report = { startedAt: new Date().toISOString(), status: 'RUNNING', source: null, steps: [] }
  const save = () => writeFileSync(resolve(reportDir, 'summary.json'), JSON.stringify(report, null, 2) + '\n', { mode: 0o600 })
  try { report.source = fingerprint() } catch (error) { report.status = 'BLOCKED'; report.error = error.message; save(); return report }
  save()
  for (const step of steps) {
    const entry = { ...step, log: resolve(reportDir, `${step.id}.log`), startedAt: new Date().toISOString() }
    if (signal?.aborted || step.needs.some(id => report.steps.find(item => item.id === id)?.status !== 'PASS')) {
      Object.assign(entry, { status: 'BLOCKED', exitCode: null, error: signal?.aborted ? 'Run cancelled' : 'Prerequisite did not pass' })
    } else {
      console.log(`[harness] ${step.id}`)
      try { Object.assign(entry, await runner(step.command, { root, env, log: entry.log, signal })) }
      catch (error) { Object.assign(entry, { status: 'FAIL', exitCode: null, error: error.message }) }
    }
    entry.finishedAt = new Date().toISOString()
    report.steps.push(entry); save()
  }
  report.status = report.steps.length && report.steps.every(step => step.status === 'PASS') ? 'PASS' : 'FAIL'
  try { if (JSON.stringify(fingerprint()) !== JSON.stringify(report.source)) { report.status = 'FAIL'; report.error = 'Source or comparison baseline changed during verification' } }
  catch (error) { report.status = 'FAIL'; report.error = error.message }
  report.finishedAt = new Date().toISOString(); save()
  return report
}

export function retainCoverageEvidence(root, reportDir, steps) {
  if (!steps.some(step => step.id.startsWith('coverage-') && step.status !== 'BLOCKED')) return []
  const retained = []
  for (const path of ['coverage/go-branches.json', 'coverage/go-branches-passed.json', 'coverage/go-statements.out', 'coverage/go-statements-passed.json', 'coverage/incremental.json', 'coverage/source-snapshot.json']) {
    if (!existsSync(resolve(root, path))) continue
    const dest = resolve(reportDir, 'evidence', path)
    mkdirSync(dirname(dest), { recursive: true, mode: 0o700 })
    copyFileSync(resolve(root, path), dest)
    chmodSync(dest, 0o600)
    retained.push(path)
  }
  return retained
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const mode = process.argv[2] || 'full'
  if (!['check', 'full'].includes(mode)) { console.error('Usage: node scripts/harness/run.mjs [check|full]'); process.exitCode = 1 }
  else {
    const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
    const reportDir = resolve(root, 'coverage/harness', `${new Date().toISOString().replace(/[:.]/g, '-')}-${process.pid}`)
    const env = { ...process.env }
    // Never inherit operator-provided live integration targets into default checks.
    for (const key of Object.keys(env)) if (/^NSC_(TEST_|E2E_)/.test(key)) delete env[key]
    const abort = new AbortController()
    for (const sig of ['SIGINT', 'SIGTERM']) process.once(sig, () => abort.abort())
    const report = await runSteps({ root, env, reportDir, steps: stepsFor(mode, env, reportDir), signal: abort.signal })
    if (mode === 'full') retainCoverageEvidence(root, reportDir, report.steps)
    console.log(`[harness] ${report.status}: ${reportDir}/summary.json`)
    process.exitCode = report.status === 'PASS' ? 0 : 1
  }
}
