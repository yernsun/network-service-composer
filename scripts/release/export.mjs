import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { chmodSync, existsSync, lstatSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, relative, resolve, sep } from 'node:path'
import { fileURLToPath } from 'node:url'
import { inspectFile, formatFindings } from '../check-repository-privacy.mjs'

const directories = new Set(['.agents', '.github', '.githooks', 'api', 'build', 'cmd', 'config', 'contrib', 'deploy', 'docs', 'examples', 'frontend', 'internal', 'scripts', 'test', 'third_party', 'tools'])
const rootFiles = new Set(['AGENTS.md', 'ARCHITECTURE.md', 'CONTRIBUTING.md', 'SECURITY.md', 'README.md', 'LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.md', 'VERSION', 'Makefile', '.gitignore', '.dockerignore', 'go.mod', 'go.sum', 'compose.yaml'])
const hash = data => createHash('sha256').update(data).digest('hex')
const git = (root, args) => execFileSync('git', ['--no-replace-objects', ...args], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 64 << 20 })

export function exportCandidate(root, destination) {
  root = resolve(root); destination = resolve(destination)
  if (existsSync(destination)) throw new Error('Candidate destination must not exist; never overwrite evidence')
  const paths = [...new Set(git(root, ['ls-files', '-z', '--cached', '--others', '--exclude-standard']).split('\0').filter(Boolean))].sort()
  const files = [], excluded = [], findings = []
  const mediaPath = resolve(root, 'scripts/release/media.json')
  const media = existsSync(mediaPath) ? JSON.parse(readFileSync(mediaPath)).files : {}
  for (const path of paths) {
    if (path === 'compose.override.yaml') { excluded.push({ path, reason: 'operator override' }); continue }
    if (!rootFiles.has(path) && !(path.includes('/') && directories.has(path.split('/')[0]))) throw new Error(`File is outside the release allowlist: ${path}`)
    const source = resolve(root, path)
    if (relative(root, source).startsWith('..' + sep)) throw new Error('Source path escapes repository')
    if (!existsSync(source)) { excluded.push({ path, reason: 'removed from candidate' }); continue }
    if (!lstatSync(source).isFile()) throw new Error(`Release source must be a regular file: ${path}`)
    const bytes = readFileSync(source)
    if ((bytes.includes(0) || /\.(?:png|jpe?g|webp|gif|ico|woff2?)$/i.test(path)) && media[path] !== hash(bytes))
      throw new Error(`Opaque source asset requires privacy review: ${path}`)
    findings.push(...inspectFile(path, bytes))
    files.push({ path, sha256: hash(bytes), size: bytes.length, executable: Boolean(lstatSync(source).mode & 0o111), bytes })
  }
  if (findings.length) throw new Error(formatFindings(findings))
  for (const required of ['VERSION', 'LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.md', 'third_party/dependencies.json'])
    if (!files.some(file => file.path === required)) throw new Error(`Required release file missing: ${required}`)
  mkdirSync(destination, { recursive: true, mode: 0o700 })
  for (const {path, bytes, executable} of files) {
    const target = resolve(destination, path)
    mkdirSync(dirname(target), { recursive: true })
    writeFileSync(target, bytes, { mode: executable ? 0o755 : 0o644 })
    chmodSync(target, executable ? 0o755 : 0o644)
  }
  const manifest = files.map(({bytes, ...entry}) => entry)
  const fingerprint = hash(JSON.stringify(manifest))
  // No old refs, hooks, objects, identities or remotes are copied.
  const gitEnv = { ...process.env }
  for (const key of Object.keys(gitEnv)) if (key.startsWith('GIT_')) delete gitEnv[key]
  const run = args => execFileSync('git', ['-c', 'user.name=NSC Release Candidate', '-c', 'user.email=release@example.invalid', '-c', 'commit.gpgsign=false', '-c', 'core.hooksPath=/dev/null', ...args], { cwd: destination, env: gitEnv, stdio: ['ignore', 'pipe', 'pipe'] })
  run(['init', '-q', '--initial-branch=main', '--template='])
  run(['add', '--all'])
  run(['commit', '-qm', 'Initial community release candidate v0.1.0'])
  const count = git(destination, ['rev-list', '--all', '--count']).trim()
  if (count !== '1' || git(destination, ['tag']).trim() || git(destination, ['remote']).trim() || /^parent /m.test(git(destination, ['cat-file', '-p', 'HEAD']).split('\n\n')[0])) throw new Error('Fresh history invariant failed')
  return { schema: 1, version: readFileSync(resolve(root, 'VERSION'), 'utf8').trim(), fingerprint, commit: git(destination, ['rev-parse', 'HEAD']).trim(), history: 'FRESH_ROOT', status: 'UNVERIFIED_CANDIDATE', files: manifest, excluded }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [destination, report] = process.argv.slice(2)
    if (!destination || !report) throw new Error('Usage: export.mjs <new-candidate-directory> <manifest-path>')
    const manifest = exportCandidate(resolve(dirname(fileURLToPath(import.meta.url)), '../..'), destination)
    writeFileSync(report, JSON.stringify(manifest, null, 2) + '\n', {mode: 0o600})
    console.log(JSON.stringify({status: manifest.status, fingerprint: manifest.fingerprint, files: manifest.files.length}))
  } catch (error) { console.error(error.message); process.exitCode = 1 }
}
