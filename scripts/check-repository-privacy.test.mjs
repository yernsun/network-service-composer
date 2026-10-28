import test from 'node:test'
import assert from 'node:assert/strict'
import { execFileSync, spawnSync } from 'node:child_process'
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { formatFindings, historicalNotice, inspectFile, inspectPush, inspectTree, inspectWorktree } from './check-repository-privacy.mjs'

const repository = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const endpoint = 'https://' + ['console', 'operator', 'com'].join('.') + '/'
const token = ['ghp', 'a'.repeat(40)].join('_')
const personPath = ['/home', 'operator', 'checkout'].join('/')
const inspect = (path, text) => inspectFile(path, Buffer.from(text))
function fixture(t) {
  const root = mkdtempSync(resolve(tmpdir(), 'nsc-privacy-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  const git = (...args) => execFileSync('git', args, { cwd: root, encoding: 'utf8', stdio: 'pipe' }).trim()
  const put = (path, text) => { mkdirSync(dirname(resolve(root, path)), { recursive: true }); writeFileSync(resolve(root, path), text) }
  git('init', '-q'); git('config', 'user.name', 'NSC fixture'); git('config', 'user.email', 'test@example.invalid')
  put('README.md', '# Fixture\n'); git('add', 'README.md'); git('commit', '-qm', 'base')
  return { root, git, put, base: git('rev-parse', 'HEAD') }
}

test('private file classes and actual deployment records cannot enter a snapshot', () => {
  for (const path of ['.env', 'deploy/.env.production', 'local/config.json', 'operator/config.json', 'cert.pem', 'key.key', 'backup.dump', 'backup.sql.gz', 'backup.tar.gz', 'snapshot.zip', 'state.db-wal', 'state.sqlite3', 'coverage/live-check.md', 'pki/cert.crt', 'reviews/operator.md', 'deploy/secrets/controller/config.json', 'scripts/__pycache__/fixture.pyc', 'deploy/controller-global.service.json', 'docs/cluster-rebuild-2026-01-02.md']) {
    assert(inspect(path, 'data').length, path)
  }
  assert.deepEqual(inspect('deploy/.env.example', 'TOKEN=<operator-token>'), [])
  assert(inspect('compose.override.yaml', 'services: {}').length)
  assert.deepEqual(inspect('deploy/secrets/controller/.gitkeep', ''), [])
  assert.deepEqual(inspect('internal/pki/pki.go', 'package pki'), [])
  assert.deepEqual(inspect('scripts/coverage/policy.mjs', 'export const policy = {}'), [])
  assert.deepEqual(inspect('docs/regression-2026-01-02.md', historicalNotice), [])
  assert(inspect('docs/regression-2026-01-02.md', historicalNotice + 'Extra rollout data').length)
})

test('operator paths, endpoints, private addresses and deployment IDs are identified', () => {
  const windows = ['C:', 'Users', 'operator', 'checkout'].join('\\')
  const data = 'safe\n' + personPath + '\n' + windows + '\n' + endpoint + '\n' + ['192', '168', '55', '42'].join('.') + '\n"SecretID": "' + 'a'.repeat(25) + '"'
  const findings = inspect('deploy/config.json', data)
  assert.deepEqual(new Set(findings.map(f => f.category)), new Set(['operator-path', 'installation-endpoint', 'private-address', 'installation-resource-id']))
  assert(findings.every(f => f.line > 1))
  assert.equal(inspect('config/controller.yaml', 'entrypoint_domain: ' + new URL(endpoint).hostname)[0].category, 'installation-domain')
})

test('private keys and credentials are blocked without echoing matched bytes', () => {
  const key = '-----BEGIN ' + 'PRIVATE KEY-----\n' + 'A'.repeat(64) + '\n-----END ' + 'PRIVATE KEY-----'
  const findings = inspect('src/config.txt', token + '\n' + key)
  assert.deepEqual(findings.map(f => f.category), ['credential-format', 'private-key-material'])
  const diagnostic = formatFindings(findings)
  assert(!diagnostic.includes(token)); assert(!diagnostic.includes('A'.repeat(64)))
  assert.equal(inspect('config/controller.yaml', 'master_key: ' + 'b'.repeat(44))[0].category, 'credential-assignment')
  assert.deepEqual(inspect('deploy/.env.example', 'NSC_MASTER_KEY=<base64-encoded-32-byte-key>'), [])
})

test('reserved examples, upstream documentation and synthetic network policy remain valid', () => {
  assert.deepEqual(inspect('docs/example.md', 'https://console.example.test https://github.com/ https://example.org\n192.0.2.10 2001:db8::10 10.0.0.0/8\n/srv/nsc/state /var/lib/nsc /home/<user>/checkout'), [])
  assert.deepEqual(inspect('internal/api/ip_access_test.go', '192.168.1.10'), [])
  assert.deepEqual(inspect('config/controller.yaml', 'entrypoint_domain: console.example.test'), [])
})

test('only exact public fixture seeds on their documented fixture surfaces are allowed', () => {
  const workflow = readFileSync(resolve(repository, '.github/workflows/ci.yml'), 'utf8')
  for (const key of ['SESSION_KEY', 'ENCRYPTION_KEY_B64']) {
    const seed = workflow.match(new RegExp('NSC_' + key + ':\\s*["\']?([A-Za-z0-9_+/=-]{32,})'))?.[1]
    assert(seed, 'CI must retain a recognizable public fixture seed')
    const assignment = 'NSC_' + key + '=' + seed
    assert.deepEqual(inspect('.github/workflows/ci.yml', assignment), [])
    assert.deepEqual(inspect('scripts/e2e-compose.sh', assignment), [])
    assert.equal(inspect('config/controller.yaml', assignment)[0].category, 'credential-assignment')
    assert.equal(inspect('scripts/e2e-new.sh', assignment)[0].category, 'credential-assignment')
    assert.equal(inspect('scripts/e2e-compose.sh', assignment + 'extra')[0].category, 'credential-assignment')
  }
  assert.equal(inspect('scripts/e2e-compose.sh', 'NSC_ADMIN_TOKEN=' + 'b'.repeat(40))[0].category, 'credential-assignment')
})

test('the staged check reads index bytes rather than sanitized working files', t => {
  const f = fixture(t)
  f.put('docs/settings.md', endpoint); f.git('add', 'docs/settings.md')
  f.put('docs/settings.md', 'https://console.example.test')
  assert.deepEqual(inspectWorktree(f.root), [])
  assert.equal(inspectTree(f.root)[0].category, 'installation-endpoint')
  f.git('add', 'docs/settings.md'); assert.deepEqual(inspectTree(f.root), [])
})

test('force-added ignored files are still checked; ordinary local files stay ignored', t => {
  const f = fixture(t)
  f.put('.gitignore', '.env\ncoverage/\n*.zip\n'); f.git('add', '.gitignore'); f.put('.env', 'KEY=local-value')
  f.put('coverage/live-check.md', 'operator record'); f.put('backup.zip', 'backup bytes')
  assert.deepEqual(inspectWorktree(f.root), [])
  f.git('add', '-f', '.env', 'coverage/live-check.md', 'backup.zip')
  assert.deepEqual(new Set(inspectTree(f.root).map(finding => finding.path)), new Set(['.env', 'coverage/live-check.md', 'backup.zip']))
})

test('outgoing history is checked even when its final tree removed private content', t => {
  const f = fixture(t)
  f.put('docs/settings.md', endpoint); f.git('add', 'docs/settings.md'); f.git('commit', '-qm', 'private fixture')
  const affected = f.git('rev-parse', 'HEAD')
  f.git('rm', '-q', 'docs/settings.md'); f.git('commit', '-qm', 'remove fixture')
  const head = f.git('rev-parse', 'HEAD')
  assert.deepEqual(inspectTree(f.root, head), [])
  const input = `refs/heads/main ${head} refs/heads/main ${f.base}\n`
  const findings = inspectPush(f.root, input)
  assert.equal(findings.length, 1); assert.equal(findings[0].commit, affected.slice(0, 12))
  assert.equal(inspectPush(f.root, input.replace(f.base, '0'.repeat(40))).length, 1)
  assert.deepEqual(inspectPush(f.root, `refs/heads/main ${'0'.repeat(40)} refs/heads/main ${head}`), [])
})

test('local replacement objects cannot disguise private staged or outgoing blobs', t => {
  const f = fixture(t)
  f.put('docs/settings.md', endpoint); f.git('add', 'docs/settings.md'); f.git('commit', '-qm', 'private fixture')
  const head = f.git('rev-parse', 'HEAD'), privateBlob = f.git('rev-parse', 'HEAD:docs/settings.md')
  f.git('replace', privateBlob, f.git('rev-parse', 'HEAD:README.md'))
  assert(!f.git('cat-file', 'blob', privateBlob).includes(endpoint))
  assert.equal(inspectTree(f.root)[0].category, 'installation-endpoint')
  assert.equal(inspectPush(f.root, `refs/heads/main ${head} refs/heads/main ${f.base}\n`)[0].category, 'installation-endpoint')
})

test('missing remote evidence and malformed push input fail closed', t => {
  const f = fixture(t)
  assert.throws(() => inspectPush(f.root, 'invalid'), /Invalid/)
  assert.throws(() => inspectPush(f.root, `ref ${f.base} ref ${'f'.repeat(40)}`), /unavailable locally/)
})

test('symlink snapshots are read as link bytes without reading an external file', t => {
  const f = fixture(t)
  symlinkSync(personPath, resolve(f.root, 'private-link')); f.git('add', 'private-link')
  assert.equal(inspectTree(f.root)[0].category, 'operator-path')
})

test('an unresolved index cannot become a privacy pass', t => {
  const f = fixture(t), blob = f.git('rev-parse', 'HEAD:README.md')
  execFileSync('git', ['update-index', '--index-info'], { cwd: f.root, input: `100644 ${blob} 1\tconflict\n100644 ${blob} 2\tconflict\n` })
  assert.throws(() => inspectTree(f.root), /index conflicts/)
})

test('the installed commit hook blocks private staged data with a redacted diagnostic', t => {
  const f = fixture(t)
  mkdirSync(resolve(f.root, 'scripts')); mkdirSync(resolve(f.root, '.githooks'))
  copyFileSync(resolve(repository, 'scripts/check-repository-privacy.mjs'), resolve(f.root, 'scripts/check-repository-privacy.mjs'))
  copyFileSync(resolve(repository, '.githooks/pre-commit'), resolve(f.root, '.githooks/pre-commit'))
  f.git('config', 'core.hooksPath', '.githooks')
  f.put('docs/settings.md', token); f.git('add', 'docs/settings.md')
  const result = spawnSync('git', ['commit', '-qm', 'rejected fixture'], { cwd: f.root, env: { ...process.env, NODE: process.execPath }, encoding: 'utf8' })
  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /credential-format/)
  assert(!result.stderr.includes(token)); assert.equal(f.git('rev-parse', 'HEAD'), f.base)
})

test('the installed push hook rejects intermediate private commits before updating a remote', t => {
  const f = fixture(t), remote = resolve(f.root, 'remote.git')
  f.git('init', '--bare', '-q', remote); f.git('remote', 'add', 'origin', remote)
  f.git('push', '-q', 'origin', 'HEAD:main')
  f.put('docs/settings.md', token); f.git('add', 'docs/settings.md'); f.git('commit', '-qm', 'private fixture')
  f.git('rm', '-q', 'docs/settings.md'); f.git('commit', '-qm', 'remove fixture')
  mkdirSync(resolve(f.root, 'scripts')); mkdirSync(resolve(f.root, '.githooks'))
  copyFileSync(resolve(repository, 'scripts/check-repository-privacy.mjs'), resolve(f.root, 'scripts/check-repository-privacy.mjs'))
  copyFileSync(resolve(repository, '.githooks/pre-push'), resolve(f.root, '.githooks/pre-push'))
  f.git('config', 'core.hooksPath', '.githooks')
  const result = spawnSync('git', ['push', 'origin', 'HEAD:main'], { cwd: f.root, env: { ...process.env, NODE: process.execPath }, encoding: 'utf8' })
  assert.notEqual(result.status, 0); assert.match(result.stderr, /credential-format/)
  assert(!result.stderr.includes(token))
  assert.equal(f.git('--git-dir=' + remote, 'rev-parse', 'refs/heads/main'), f.base)
})
