import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, lstatSync, readFileSync, readlinkSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

export const historicalNotice = '# Historical record\n\nInstallation-specific deployment observations have been removed from the public\nsource tree. Original evidence is retained in restricted, ignored local storage.\nThis notice is a documentation index entry, not a deployment or acceptance claim.\n\nSee [repository privacy](repository-privacy.md) for handling operator records.\n'
const record = /(?:^|\/)[\w-]*(?:deployment|rebuild|regression|rollout|service-update)[\w-]*-\d{4}-\d{2}-\d{2}\.(?:md|json|log)$/i
const privateFile = /(?:^|\/)(?:\.env(?:\.(?!example$)[^/]+)?|\.aws|\.ssh|\.kube|operator|local|backups|reports|deployment-records|__pycache__)(?:\/|$)|\.(?:pem|key|p12|pfx|kubeconfig|dump|sql\.gz|sqlite3?|tar(?:\.gz)?|tgz|zip|db(?:-(?:shm|wal))?|py[co])$/i
const privateArtifact = /^(?:coverage|pki|cookbooks|reviews)\/|^frontend\/(?:coverage|test-results|playwright-report)\//
const privateSecret = path => /^deploy\/secrets\//.test(path) && !/^deploy\/secrets\/(?:controller|gateway)\/\.gitkeep$/.test(path)
const rawService = /^deploy\/(?:controller|gateway)-global\.service\.json$/
const operatorPath = /\/(?:home|Users)\/[\w.-]+(?=\/|\b)|\b[A-Za-z]:\\+(?:Users|home)\\+[\w.-]+/g
const privateIP = /\b(?:10(?:\.\d{1,3}){3}|192\.168\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3})\b/g
const publicHosts = new Set(['github.com', 'docs.github.com', 'raw.githubusercontent.com', 'git-scm.com', 'docs.docker.com', 'hub.docker.com', 'nginx.org', 'www.apache.org', 'www.w3.org', 'json-schema.org', 'docs.phpmyadmin.net', 'www.pgadmin.org', 'pkg.go.dev', 'quic-go.net', 'martinfowler.com', 'testing.googleblog.com', 'acme-v02.api.letsencrypt.org', 'gitlab.com', 'your-account.github.io'])
const reservedHost = host => /(?:^|\.)example\.(?:com|net|org)$|\.(?:example|test|invalid|localhost)$/.test(host) || ['localhost', '127.0.0.1', '0.0.0.0', '::1'].includes(host)
const publishingSurface = path => /^(?:docs\/|config\/|deploy\/|README\.md$|ARCHITECTURE\.md$|compose[^/]*\.ya?ml$)/.test(path) && !/\.(?:png|svg)$/.test(path)
// These exact public seeds belong to disposable fixtures, never operator config.
const fixtureSeedHashes = new Map([
  ['session_key', '861009ec4d599fab1f40abc76e6f89880cff5833c79c548c99f9045f191cd90b'],
  ['encryption_key_b64', '2cf5e6ec387461b4bf954f587ad4d957753fcbc48bf892b5e49996b90cf3b476'],
])
const fixtureSurface = path => /^\.github\/workflows\/(?:ci|release)\.yml$|^scripts\/e2e-(?:acme|compose|delivery|ha|management|swarm|ui|workspace)\.(?:sh|py)$/.test(path)

export function inspectFile(path, bytes) {
  const text = bytes.toString('utf8'), findings = []
  const add = (category, index = 0) => findings.push({ path, line: text.slice(0, index).split('\n').length, category })
  if (privateFile.test(path) || privateArtifact.test(path) || privateSecret(path) || path === 'compose.override.yaml') add('private-file')
  if (rawService.test(path)) add('installation-service-export')
  if (record.test(path) && text !== historicalNotice) add('deployment-record')
  for (const match of text.matchAll(operatorPath)) add('operator-path', match.index)
  for (const match of text.matchAll(/(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,}|AKIA[A-Z0-9]{16}|xox[baprs]-[A-Za-z0-9-]{20,})/g)) add('credential-format', match.index)
  for (const match of text.matchAll(/(master_key|encryption_key_b64|session_key|admin_token|api_key|access_token|client_secret|password)["']?\s*[:=]\s*["']?([A-Za-z0-9_+/=-]{32,})/gi)) {
    const hash = createHash('sha256').update(match[2]).digest('hex')
    if (!fixtureSurface(path) || fixtureSeedHashes.get(match[1].toLowerCase()) !== hash) add('credential-assignment', match.index)
  }
  for (const match of text.matchAll(/-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----[\s\S]{40,}?-----END (?:RSA |EC |OPENSSH )?PRIVATE KEY-----/g)) add('private-key-material', match.index)
  if (publishingSurface(path)) {
    for (const match of text.matchAll(privateIP)) {
      const tail = text.slice(match.index)
      if (!/^(?:10\.0\.0\.0\/8|172\.16\.0\.0\/12|192\.168\.0\.0\/16)(?!\d)/.test(tail)) add('private-address', match.index)
    }
    for (const match of text.matchAll(/https?:\/\/([A-Za-z0-9][A-Za-z0-9.-]*)(?=[:/\s`"')]|$)/g)) {
      const host = match[1].toLowerCase()
      if (host.includes('.') && !reservedHost(host) && !publicHosts.has(host)) add('installation-endpoint', match.index)
    }
    for (const match of text.matchAll(/\b(?:domain|primary_domain|entrypoint_domain|hostname|host)["']?\s*[:=]\s*["']?([A-Za-z0-9*][A-Za-z0-9.*-]*\.[A-Za-z0-9.-]+)/g)) {
      const host = match[1].replace(/^\*\./, '').toLowerCase()
      if (!reservedHost(host) && !publicHosts.has(host)) add('installation-domain', match.index)
    }
    if (/\.(?:json|ya?ml)$/.test(path)) {
      for (const match of text.matchAll(/"(?:SecretID|ConfigID)"\s*:\s*"[a-z0-9]{25}"|"Target"\s*:\s*"[a-z0-9]{25}"/g)) add('installation-resource-id', match.index)
    }
  }
  return findings
}

function git(root, args, options = {}) {
  return execFileSync('git', ['--no-replace-objects', ...args], { cwd: root, stdio: 'pipe', maxBuffer: 256 * 1024 * 1024, ...options })
}
function treeEntries(root, revision) {
  const args = revision ? ['ls-tree', '-rz', '--full-tree', revision] : ['ls-files', '--stage', '-z']
  return git(root, args).toString().split('\0').filter(Boolean).map(line => {
    const [meta, path] = line.split('\t'), fields = meta.split(' ')
    if (!revision && fields[2] !== '0') throw new Error('Resolve index conflicts before the privacy check')
    if (fields[0] === '160000') throw new Error('Submodule content requires a separate publishing review')
    return { path, oid: fields[1 + Number(Boolean(revision))] }
  })
}
export function inspectTree(root, revision) {
  const entries = treeEntries(root, revision)
  if (!entries.length) return []
  const output = git(root, ['cat-file', '--batch'], { input: entries.map(entry => entry.oid).join('\n') + '\n' })
  let offset = 0
  return entries.flatMap(entry => {
    const end = output.indexOf(10, offset), fields = output.subarray(offset, end).toString().split(' ')
    if (end < 0 || fields[1] !== 'blob' || !/^\d+$/.test(fields[2])) throw new Error('Git object evidence is missing')
    const size = Number(fields[2]), bytes = output.subarray(end + 1, end + 1 + size)
    if (bytes.length !== size || output[end + 1 + size] !== 10) throw new Error('Git object evidence is truncated')
    offset = end + 2 + size
    return inspectFile(entry.path, bytes)
  })
}
export function inspectWorktree(root) {
  const paths = git(root, ['ls-files', '-z', '--cached', '--others', '--exclude-standard']).toString().split('\0').filter(Boolean)
  return [...new Set(paths)].flatMap(path => {
    const absolute = resolve(root, path)
    if (!existsSync(absolute)) return []
    const bytes = lstatSync(absolute).isSymbolicLink() ? Buffer.from(readlinkSync(absolute)) : readFileSync(absolute)
    return inspectFile(path, bytes)
  })
}
export function inspectPush(root, input) {
  const commits = new Set()
  for (const line of input.trim().split('\n').filter(Boolean)) {
    const [, local, , remote] = line.trim().split(/\s+/)
    if (!/^[a-f0-9]{40,64}$/.test(local || '') || !/^[a-f0-9]{40,64}$/.test(remote || '')) throw new Error('Invalid Git push input')
    if (/^0+$/.test(local)) continue
    const range = [local]
    if (!/^0+$/.test(remote)) {
      try { git(root, ['cat-file', '-e', remote + '^{commit}']) }
      catch { throw new Error('Remote base is unavailable locally; fetch it before retrying the push') }
      range.push('^' + remote)
    }
    for (const commit of git(root, ['rev-list', ...range]).toString().trim().split('\n').filter(Boolean)) commits.add(commit)
  }
  return [...commits].flatMap(commit => inspectTree(root, commit).map(finding => ({ ...finding, commit: commit.slice(0, 12) })))
}
export function formatFindings(findings) {
  return findings.map(finding => `${finding.commit ? finding.commit + ' ' : ''}${finding.path}:${finding.line} ${finding.category}`).join('\n')
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const root = resolve(dirname(fileURLToPath(import.meta.url)), '..'), mode = process.argv[2] || '--worktree'
    let findings
    if (mode === '--worktree') findings = inspectWorktree(root)
    else if (mode === '--staged') findings = inspectTree(root)
    else if (mode === '--pre-push') findings = inspectPush(root, readFileSync(0, 'utf8'))
    else throw new Error('Use --worktree, --staged or --pre-push')
    if (findings.length) {
      console.error('Repository privacy check rejected the following files:\n' + formatFindings(findings))
      process.exitCode = 1
    } else console.log('Repository privacy check passed (' + mode.slice(2) + ').')
  } catch (error) { console.error(error.message); process.exitCode = 1 }
}
