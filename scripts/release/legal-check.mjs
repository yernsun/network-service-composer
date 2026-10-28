import { createHash } from 'node:crypto'
import { readFileSync } from 'node:fs'
import { resolve, relative } from 'node:path'
import { fileURLToPath } from 'node:url'

export function legalCheck(root) {
  const read = path => {
    if (relative(root, resolve(root, path)).startsWith('..')) throw new Error('License path escapes source tree')
    return readFileSync(resolve(root, path))
  }
  const sha = bytes => createHash('sha256').update(bytes).digest('hex')
  const inventory = JSON.parse(read('third_party/dependencies.json'))
  const problems = []
  for (const path of ['go.mod', 'go.sum', 'frontend/package.json', 'frontend/package-lock.json', 'VERSION'])
    if (inventory.inputs?.[path] !== sha(read(path))) problems.push({path, category:'stale-license-inventory'})
  if (!Array.isArray(inventory.entries) || !inventory.entries.length || !Array.isArray(inventory.unresolved)) throw new Error('Missing dependency license evidence')
  for (const entry of inventory.entries) {
    for (const notice of entry.notices || [])
      if (sha(read(notice.path)) !== notice.sha256) problems.push({path:notice.path, category:'modified-third-party-notice'})
    if ((entry.linked || entry.bundled) && !entry.notices?.length)
      problems.push({component: `${entry.ecosystem}:${entry.name}@${entry.version}`, category:'missing-license-copy'})
  }
  const provenance = JSON.parse(read('third_party/provenance.json'))
  if (!Array.isArray(provenance.assets) || !provenance.assets.length) throw new Error('Missing asset provenance')
  for (const asset of provenance.assets) {
    if (asset.status !== 'CONFIRMED' || !asset.evidence) problems.push({component:asset.id, category:'unresolved-provenance'})
    else if (!read(asset.evidence).length) problems.push({component:asset.id, category:'empty-provenance-evidence'})
  }
  for (const path of ['LICENSE', 'NOTICE', 'THIRD_PARTY_NOTICES.md']) if (!read(path).length) problems.push({path,category:'empty-license-document'})
  return {status: problems.length ? 'BLOCKED' : 'PASS', problems}
}
if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { const result=legalCheck(process.cwd()); console.log(JSON.stringify(result,null,2)); if(result.status !== 'PASS') process.exitCode=1 }
  catch { console.error('License gate blocked: required evidence is missing or invalid'); process.exitCode=1 }
}
