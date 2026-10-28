import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { execFileSync, spawnSync } from 'node:child_process'
import { appendFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, statSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'
import { evaluate } from './gate.mjs'

const expected = { source: { commit: 'a'.repeat(40), fingerprint: 'b'.repeat(64) }, contracts: { protocol: 4, renderer: 'fixture', migrations: { postgres: 'c'.repeat(64) } } }
const policy = { schema: 1, jobs: [{ id: 'fixture', workflowJob: 'fixture-job', checks: ['build', 'real-e2e'], images: ['controller'] }] }
const evidence = () => ({ schema: 1, job: 'fixture', ...structuredClone(expected), status: 'PASS', checks: [{ id: 'build', status: 'PASS' }, { id: 'real-e2e', status: 'PASS' }], images: { controller: { digest: 'sha256:' + 'd'.repeat(64), ...expected.source } }, exclusions: { ovh: 'DISABLED' } })
test('requires source-bound executed evidence and image identity', () => {
  assert.equal(evaluate(expected, policy, [evidence()], { 'fixture-job': 'success' }).status, 'PASS')
})
test('failed, skipped, missing, cancelled and unknown jobs all block', () => {
  for (const result of ['failure', 'skipped', 'cancelled', undefined, 'unknown']) assert.equal(evaluate(expected, policy, [evidence()], { 'fixture-job': result }).status, 'FAIL')
})
test('old source, changed schema and duplicate evidence cannot substitute for current results', () => {
  for (const field of ['commit', 'fingerprint', 'protocol', 'migration', 'renderer']) {
    const report = evidence()
    if (field === 'commit' || field === 'fingerprint') report.source[field] = '0'.repeat(field === 'commit' ? 40 : 64)
    else if (field === 'migration') report.contracts.migrations.postgres = '0'.repeat(64)
    else report.contracts[field] = 'obsolete'
    assert.equal(evaluate(expected, policy, [report], { 'fixture-job': 'success' }).status, 'FAIL')
  }
  assert.equal(evaluate(expected, policy, [evidence(), evidence()], { 'fixture-job': 'success' }).status, 'FAIL')
})
test('missing or unexecuted checks and mutable/mismatched images block', () => {
  for (const change of [r => r.checks.pop(), r => r.checks[1].status = 'SKIPPED', r => r.images = {}, r => r.images.controller.digest = 'latest', r => r.images.controller.commit = '0'.repeat(40), r => r.status = 'FAIL', r => r.errors = [{ code: 'image_identity_mismatch' }], r => r.exclusions.ovh = 'PASS']) {
    const report = evidence(); change(report)
    assert.equal(evaluate(expected, policy, [report], { 'fixture-job': 'success' }).status, 'FAIL')
  }
  assert.equal(evaluate(expected, policy, [], { 'fixture-job': 'success' }).status, 'FAIL')
})
test('an empty or malformed policy cannot disable admission', () => {
  for (const jobs of [[], undefined, [{ id: 'fixture', workflowJob: 'fixture-job', checks: [] }], [...policy.jobs, ...policy.jobs]]) assert.throws(() => evaluate(expected, { schema: 1, jobs }, [evidence()], { 'fixture-job': 'success' }))
})

const script = fileURLToPath(new URL('./gate.mjs', import.meta.url))
const privateDiagnostic = 'PRIVATE_FIXTURE_DOCKER_DIAGNOSTIC'
const imageRefs = { controller: 'nsc-controller:fixture', gateway: 'nsc-gateway:fixture' }
const successfulChecks = { build: 'success', 'real-e2e': 'success' }
const hash = value => createHash('sha256').update(value).digest('hex')

// Only this invocation-owned Git tree and its fake Docker executable are used.
// No command reaches the host Docker daemon or modifies the working repository.
function cliFixture(t) {
  const directory = mkdtempSync(join(tmpdir(), 'nsc-readiness-cli-'))
  t.after(() => rmSync(directory, { recursive: true, force: true }))
  const root = join(directory, 'repo'), bin = join(directory, 'bin')
  mkdirSync(root); mkdirSync(bin)
  const write = (path, value) => {
    mkdirSync(dirname(join(root, path)), { recursive: true })
    writeFileSync(join(root, path), value)
  }
  write('.gitignore', '/coverage/\n')
  write('README.md', 'Readiness CLI fixture.\n')
  write('internal/gateway/server.go', 'package gateway\nconst ProtocolVersion int32 = 4\n')
  write('internal/nginx/render.go', 'package nginx\nconst RendererVersion = "fixture-v1"\n')
  for (const driver of ['postgres', 'mysql', 'sqlite']) write(`internal/storage/migrations/${driver}/0001_initial.sql`, 'SELECT 1;\n')
  write('.github/readiness-policy.json', JSON.stringify({ schema: 1, jobs: [
    { id: 'fixture', workflowJob: 'fixture-job', checks: ['build', 'real-e2e'], images: ['controller', 'gateway'] },
  ], exclusions: { ovh: 'DISABLED' } }))
  const env = { ...process.env, GIT_CONFIG_GLOBAL: '/dev/null', GIT_CONFIG_NOSYSTEM: '1', GIT_TERMINAL_PROMPT: '0',
    GITHUB_RUN_ID: 'fixture-run', GITHUB_RUN_ATTEMPT: '1', NSC_CHECK_RESULTS: JSON.stringify(successfulChecks),
    NSC_EVIDENCE_IMAGES: JSON.stringify(imageRefs), NSC_REQUIRED_JOBS: JSON.stringify({ 'fixture-job': 'success' }),
    NSC_FIXTURE_DOCKER_LOG: join(directory, 'docker-calls.jsonl'), PATH: `${bin}:${process.env.PATH}` }
  for (const variable of ['GIT_DIR', 'GIT_WORK_TREE', 'GIT_INDEX_FILE', 'GITHUB_SHA']) delete env[variable]
  const git = args => execFileSync('git', args, { cwd: root, env, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
  git(['init', '--quiet', '--initial-branch=main'])
  git(['add', '.'])
  git(['-c', 'user.name=NSC Fixture', '-c', 'user.email=fixture@example.test', 'commit', '--quiet', '-m', 'fixture'])
  env.GITHUB_SHA = git(['rev-parse', 'HEAD'])
  writeFileSync(join(bin, 'docker'), `#!${process.execPath}\n` + String.raw`
const { appendFileSync } = require('node:fs')
const args = process.argv.slice(2)
appendFileSync(process.env.NSC_FIXTURE_DOCKER_LOG, JSON.stringify(args) + '\n')
const value = JSON.parse(process.env.NSC_FIXTURE_DOCKER_IMAGES || '{}')[args[2]]
if (args.length !== 3 || args[0] !== 'image' || args[1] !== 'inspect' || !value || value.failure) {
  process.stderr.write('PRIVATE_FIXTURE_DOCKER_DIAGNOSTIC\n'); process.exit(42)
}
process.stdout.write(value.raw === undefined ? JSON.stringify([value]) : value.raw)
`, { mode: 0o700 })
  const run = (args, override = {}) => spawnSync(process.execPath, [script, ...args], { cwd: root, env: { ...env, ...override }, encoding: 'utf8' })
  const current = () => {
    const result = run(['identity'])
    assert.equal(result.status, 0, result.stderr)
    return JSON.parse(result.stdout)
  }
  const image = (source, digest = 'd') => ({ Id: 'sha256:' + digest.repeat(64), Config: { Labels: {
    'org.opencontainers.image.revision': source.commit, 'org.nsc.source.fingerprint': source.fingerprint,
  } } })
  const bindImages = (source = current().source) => { env.NSC_FIXTURE_DOCKER_IMAGES = JSON.stringify({
    [imageRefs.controller]: image(source), [imageRefs.gateway]: image(source, 'e'),
  }) }
  const reportPath = join(root, 'coverage/input/fixture.json')
  const record = override => {
    const result = run(['record', 'fixture', reportPath], override)
    assert.ok(existsSync(reportPath), 'record did not retain its envelope')
    return { result, report: JSON.parse(readFileSync(reportPath)), raw: readFileSync(reportPath, 'utf8') }
  }
  const gate = override => {
    const decision = join(root, 'coverage/decision.json')
    const result = run(['gate', 'coverage/input', decision], override)
    assert.ok(existsSync(decision), 'gate did not retain its decision')
    return { result, report: JSON.parse(readFileSync(decision)) }
  }
  bindImages()
  return { root, env, write, git, run, current, image, bindImages, record, gate, reportPath }
}

test('CLI identity measures the real Git tree and protocol, renderer and ordered migrations', t => {
  const f = cliFixture(t), initial = f.current()
  assert.equal(initial.source.commit, f.env.GITHUB_SHA)
  assert.match(initial.source.fingerprint, /^[a-f0-9]{64}$/)
  assert.equal(initial.contracts.protocol, 4)
  assert.equal(initial.contracts.renderer, 'fixture-v1')
  assert.deepEqual(initial.run, { id: 'fixture-run', attempt: '1' })
  const migration = hash(`0001_initial.sql\0${hash('SELECT 1;\n')}\0`)
  assert.deepEqual(initial.contracts.migrations, { postgres: migration, mysql: migration, sqlite: migration })
  f.write('coverage/private-report.json', '{"fixture":"ignored"}')
  assert.deepEqual(f.current(), initial)
  f.write('untracked.txt', 'Uncommitted source.\n')
  assert.notEqual(f.current().source.fingerprint, initial.source.fingerprint)
  rmSync(join(f.root, 'untracked.txt'))
  assert.deepEqual(f.current(), initial)
  f.write('internal/gateway/server.go', 'package gateway\nconst ProtocolVersion int32 = 5\n')
  f.write('internal/nginx/render.go', 'package nginx\nconst RendererVersion = "fixture-v2"\n')
  f.write('internal/storage/migrations/postgres/0002_next.sql', 'SELECT 2;\n')
  const changed = f.current()
  assert.equal(changed.source.commit, initial.source.commit)
  assert.notEqual(changed.source.fingerprint, initial.source.fingerprint)
  assert.equal(changed.contracts.protocol, 5)
  assert.equal(changed.contracts.renderer, 'fixture-v2')
  assert.equal(changed.contracts.migrations.postgres, hash(['0001_initial.sql', hash('SELECT 1;\n'), '0002_next.sql', hash('SELECT 2;\n'), ''].join('\0')))
  assert.equal(changed.contracts.migrations.mysql, migration)
  const output = f.run(['github-output'])
  assert.equal(output.status, 0)
  assert.equal(output.stdout, `fingerprint=${changed.source.fingerprint}\ncommit=${initial.source.commit}\n`)
  assert.ok(!existsSync(f.env.NSC_FIXTURE_DOCKER_LOG), 'identity called Docker')
})

test('CLI record and gate admit only executed, current source-bound image evidence', t => {
  const f = cliFixture(t), { result, report } = f.record()
  assert.equal(result.status, 0, result.stderr)
  assert.equal(report.status, 'PASS')
  assert.deepEqual(report.checks, [{ id: 'build', status: 'PASS' }, { id: 'real-e2e', status: 'PASS' }])
  assert.deepEqual(report.errors, [])
  for (const name of ['controller', 'gateway']) {
    assert.equal(report.images[name].commit, f.env.GITHUB_SHA)
    assert.equal(report.images[name].fingerprint, f.current().source.fingerprint)
    assert.equal(report.images[name].digest_type, 'oci-image-config')
  }
  assert.equal(statSync(f.reportPath).mode & 0o777, 0o600)
  assert.equal(statSync(dirname(f.reportPath)).mode & 0o777, 0o700)
  assert.deepEqual(readFileSync(f.env.NSC_FIXTURE_DOCKER_LOG, 'utf8').trim().split('\n').map(line => JSON.parse(line)), [
    ['image', 'inspect', imageRefs.controller], ['image', 'inspect', imageRefs.gateway],
  ])
  const admitted = f.gate()
  assert.equal(admitted.result.status, 0, admitted.result.stderr)
  assert.equal(admitted.report.status, 'PASS')
  assert.deepEqual(admitted.report.errors, [])
})

test('CLI failure envelopes preserve the actual required step outcomes', t => {
  const f = cliFixture(t)
  for (const [outcome, status] of [['failure', 'FAIL'], ['skipped', 'SKIPPED'], ['cancelled', 'CANCELLED'], [undefined, 'BLOCKED'], ['unknown', 'BLOCKED']]) {
    const { result, report } = f.record({ NSC_CHECK_RESULTS: JSON.stringify({ build: 'success', 'real-e2e': outcome, irrelevant: 'success' }) })
    assert.equal(result.status, 1)
    assert.equal(report.status, 'FAIL')
    assert.deepEqual(report.checks, [{ id: 'build', status: 'PASS' }, { id: 'real-e2e', status }])
    assert.deepEqual(report.errors, [])
    assert.ok(report.images.controller && report.images.gateway)
    const blocked = f.gate()
    assert.equal(blocked.result.status, 1)
    assert.ok(blocked.report.errors.includes('required check not passed: fixture/real-e2e'))
  }
})

test('CLI missing and stale images retain safe FAIL envelopes and cannot satisfy the gate', t => {
  const f = cliFixture(t), source = f.current().source
  const cases = [
    { name: 'missing reference', refs: { controller: imageRefs.controller }, code: 'image_identity_unavailable' },
    { name: 'missing Docker image', gateway: { failure: true }, code: 'image_identity_unavailable' },
    { name: 'invalid inspection JSON', gateway: { raw: privateDiagnostic }, code: 'image_identity_unavailable' },
    { name: 'stale commit', gateway: f.image({ ...source, commit: '0'.repeat(40) }), code: 'image_identity_mismatch' },
    { name: 'stale fingerprint', gateway: f.image({ ...source, fingerprint: '0'.repeat(64) }), code: 'image_identity_mismatch' },
    { name: 'invalid immutable digest', gateway: { ...f.image(source), Id: 'mutable-tag' }, code: 'image_identity_invalid' },
  ]
  for (const scenario of cases) {
    const { result, report, raw } = f.record({ NSC_EVIDENCE_IMAGES: JSON.stringify(scenario.refs || imageRefs),
      NSC_FIXTURE_DOCKER_IMAGES: JSON.stringify({ [imageRefs.controller]: f.image(source), [imageRefs.gateway]: scenario.gateway || f.image(source, 'e') }) })
    assert.equal(result.status, 1, scenario.name)
    assert.equal(report.status, 'FAIL', scenario.name)
    assert.deepEqual(report.checks, [{ id: 'build', status: 'PASS' }, { id: 'real-e2e', status: 'PASS' }])
    assert.deepEqual(report.errors, [{ code: scenario.code, image: 'gateway' }])
    assert.ok(report.images.controller, scenario.name)
    assert.equal(report.images.gateway, undefined, scenario.name)
    assert.ok(![result.stdout, result.stderr, raw].some(value => value.includes(privateDiagnostic)), scenario.name)
    assert.ok(!raw.includes(imageRefs.gateway), 'unverified image reference leaked')
    let blocked = f.gate()
    assert.equal(blocked.result.status, 1, scenario.name)
    assert.ok(blocked.report.errors.includes('image identity missing/mismatched: fixture/gateway'))
    // Changing the summary flag cannot manufacture the missing immutable proof.
    writeFileSync(f.reportPath, JSON.stringify({ ...report, status: 'PASS' }))
    blocked = f.gate()
    assert.equal(blocked.result.status, 1, scenario.name)
  }
})

test('CLI rejects previous run/attempt and changed source instead of reusing old reports', t => {
  const f = cliFixture(t)
  assert.equal(f.record().result.status, 0)
  for (const override of [{ GITHUB_RUN_ID: 'previous-run' }, { GITHUB_RUN_ATTEMPT: '2' }]) {
    const blocked = f.gate(override)
    assert.equal(blocked.result.status, 1)
    assert.ok(blocked.report.errors.includes('workflow run mismatch: fixture'))
  }
  appendFileSync(join(f.root, 'README.md'), 'Changed configuration source.\n')
  const stale = f.gate()
  assert.equal(stale.result.status, 1)
  assert.ok(stale.report.errors.includes('source/contract mismatch: fixture'))
  const { result, report } = f.record()
  assert.equal(result.status, 1)
  assert.equal(report.status, 'FAIL')
  assert.deepEqual(report.images, {})
  assert.equal(report.errors.length, 2)
  assert.ok(report.errors.every(value => value.code === 'image_identity_mismatch'))
})

test('CLI checkout SHA mismatch keeps required outcomes and blocks admission', t => {
  const f = cliFixture(t)
  const { result, report } = f.record({ GITHUB_SHA: '0'.repeat(40) })
  assert.equal(result.status, 1)
  assert.equal(report.source.commit, f.env.GITHUB_SHA)
  assert.deepEqual(report.errors, [{ code: 'source_revision_mismatch' }])
  assert.deepEqual(report.checks, [{ id: 'build', status: 'PASS' }, { id: 'real-e2e', status: 'PASS' }])
  assert.ok(report.images.controller && report.images.gateway)
  assert.equal(f.gate().result.status, 1)
})
