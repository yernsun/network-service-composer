import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { identity } from './gate.mjs'
const validDigest = value => /^sha256:[a-f0-9]{64}$/.test(value)
const validRepository = value => /^ghcr\.io\/[a-z0-9._-]+\/nsc-(controller|gateway)$/.test(value)
export function selectArchitectures(index, digest, repository) {
  if (!validDigest(digest) || !validRepository(repository) || !Array.isArray(index.manifests)) throw new Error('release.invalid_image_index')
  const architectures = {}
  for (const arch of ['amd64', 'arm64']) {
    const matches = index.manifests.filter(m => m.platform?.os === 'linux' && m.platform.architecture === arch)
    if (matches.length !== 1 || !validDigest(matches[0].digest)) throw new Error('release.missing_or_ambiguous_architecture')
    architectures[arch] = matches[0].digest
  }
  if (architectures.amd64 === architectures.arm64) throw new Error('release.duplicate_architecture_digest')
  return { repository, digest, digest_type: 'oci-image-index', architectures }
}
export function promote(report, expected, version, scans, run) {
  if (JSON.stringify(report.source) !== JSON.stringify(expected.source) || JSON.stringify(report.contracts) !== JSON.stringify(expected.contracts)) throw new Error('release.source_contract_mismatch')
  const selected = selectArchitectures({ manifests: Object.entries(report.architectures || {}).map(([architecture, digest]) => ({ platform: { os: 'linux', architecture }, digest })) }, report.digest, report.repository)
  if (!/^v[0-9]+\.[0-9]+\.[0-9]+(?:-[a-zA-Z0-9.-]+)?$/.test(version)) throw new Error('release.invalid_version')
  if (scans.amd64 !== 'success' || scans.arm64 !== 'success') throw new Error('release.architecture_scan_not_passed')
  const tag = selected.repository + ':' + version
  run(['buildx', 'imagetools', 'create', '--tag', tag, selected.repository + '@' + selected.digest])
  const actual = run(['buildx', 'imagetools', 'inspect', tag, '--format', '{{.Manifest.Digest}}']).trim()
  if (actual !== selected.digest) throw new Error('release.promoted_digest_mismatch')
  return { ...report, version, status: 'PASS', scan_results: { amd64: 'PASS', arm64: 'PASS' } }
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const [command, path, digest, repository, output] = process.argv.slice(2)
    if (command === 'select' && output) {
      const selected = selectArchitectures(JSON.parse(readFileSync(path)), digest, repository)
      writeFileSync(output, JSON.stringify({ schema: 1, ...identity(process.cwd()), ...selected, status: 'PENDING' }) + '\n', { mode: 0o600 })
      for (const [arch, digest] of Object.entries(selected.architectures)) console.log(`${arch}=${repository}@${digest}`)
    } else if (command === 'promote' && path) {
      const report = JSON.parse(readFileSync(path))
      const version = 'v' + readFileSync('VERSION', 'utf8').trim()
      if (process.env.GITHUB_REF_NAME !== version) throw new Error('release.version_mismatch')
      const run = args => {
        try { return execFileSync('docker', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], timeout: 120000, maxBuffer: 1024 * 1024 }) }
        catch { throw new Error('release.registry_operation_failed') }
      }
      const result = promote(report, identity(process.cwd()), version, JSON.parse(process.env.NSC_IMAGE_SCAN_RESULTS || '{}'), run)
      writeFileSync(path, JSON.stringify(result) + '\n', { mode: 0o600 })
    } else throw new Error('Usage: release-manifest.mjs select <index> <digest> <repository> <report>|promote <report>')
  } catch (error) { console.error(error.message); process.exitCode = 1 }
}
