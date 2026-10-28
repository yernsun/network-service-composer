// Generate a conservative inventory and retain verbatim dependency notices.
// Package metadata is evidence, not a legal compatibility determination.
import { execFileSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const out = resolve(root, 'third_party/licenses')
const sha = data => createHash('sha256').update(data).digest('hex')
const modules = execFileSync(process.env.GO || 'go', ['list', '-m', '-json', 'all'], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
const go = JSON.parse('[' + modules.trim().replace(/}\s*\n{/g, '},{') + ']')
const packages = execFileSync(process.env.GO || 'go', ['list', '-deps', '-json', './cmd/nsc-controller', './cmd/nsc-agent'], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], maxBuffer: 64 << 20 })
const linked = new Set(JSON.parse('[' + packages.trim().replace(/}\s*\n{/g, '},{') + ']').map(pkg => pkg.Module?.Path).filter(Boolean))
const lock = JSON.parse(readFileSync(resolve(root, 'frontend/package-lock.json')))
const bundled = JSON.parse(readFileSync(resolve(root, 'internal/api/frontend-dist/nsc-bundled-dependencies.json')))
const entries = []
function notices(directory, key) {
  if (!directory || !existsSync(directory)) return []
  return readdirSync(directory, { withFileTypes: true }).filter(item => item.isFile() && (/^(?:licen[cs]e|copying|notice|copyright)(?:[._-].*)?$/i.test(item.name) || /^readme/i.test(item.name) && /(?:Permission is hereby granted, free of charge|Redistribution and use in source and binary forms)/.test(readFileSync(resolve(directory, item.name), 'utf8')))).sort((a,b) => a.name.localeCompare(b.name)).map(item => {
    const bytes = readFileSync(resolve(directory, item.name))
    const path = `third_party/licenses/${key}/${item.name}`
    mkdirSync(dirname(resolve(root, path)), { recursive: true })
    writeFileSync(resolve(root, path), bytes)
    return { path, sha256: sha(bytes) }
  })
}
for (const module of go.filter(item => !item.Main)) {
  const effective = module.Replace || module
  const key = 'go-' + sha(`${module.Path}@${module.Version}`).slice(0, 20)
  const copied = notices(effective.Dir, key)
  if (!copied.length && module.Path === 'github.com/go-acme/tencentclouddnspod' && module.Version === 'v1.1.10') {
    const headers = ['modifiedclient.go', 'models.go', 'errors.go'].map(name => readFileSync(resolve(effective.Dir, 'v20210323', name), 'utf8').split('package ')[0])
    if (headers.some(header => !header.includes('Licensed under the Apache License, Version 2.0'))) throw new Error('Tencent generated-source license changed')
    const texts = { LICENSE: readFileSync(resolve(root, 'LICENSE')), NOTICE: Buffer.from([...new Set(headers)].join('\n')) }
    for (const [name, bytes] of Object.entries(texts)) {
      const path = `third_party/licenses/${key}/${name}`
      mkdirSync(dirname(resolve(root, path)), {recursive:true}); writeFileSync(resolve(root,path),bytes)
      copied.push({path,sha256:sha(bytes)})
    }
  }
  entries.push({ ecosystem: 'go', name: module.Path, version: module.Version, linked: linked.has(module.Path), scope: linked.has(module.Path) ? 'binary dependency' : 'module-graph only (not linked on this platform)', checksum: module.Sum || null,
    replacement: module.Replace ? { name: effective.Path, version: effective.Version || null } : undefined,
    notices: copied })
}
for (const [path, pkg] of Object.entries(lock.packages)) {
  if (!path) continue
  const name = pkg.name || path.split('node_modules/').at(-1)
  const key = 'npm-' + sha(`${name}@${pkg.version}`).slice(0, 20)
  entries.push({ ecosystem: 'npm', name, version: pkg.version, bundled: bundled.packages.some(item => item.name === name && item.version === pkg.version), scope: pkg.dev ? 'development' : 'runtime-or-build', optional: Boolean(pkg.optional),
    declaredLicense: pkg.license || null, integrity: pkg.integrity || null, installed: existsSync(resolve(root, 'frontend', path, 'package.json')),
    notices: notices(resolve(root, 'frontend', path), key) })
}
entries.sort((a,b) => `${a.ecosystem}:${a.name}:${a.version}`.localeCompare(`${b.ecosystem}:${b.name}:${b.version}`))
const missing = entries.filter(item => !item.notices.length && (item.linked || item.bundled))
mkdirSync(out, { recursive: true })
mkdirSync(resolve(root, 'third_party/compose-spec'), {recursive: true})
for (const [source, target] of [['SCHEMA-LICENSE', 'LICENSE'], ['SCHEMA.md', 'PROVENANCE.md']])
  writeFileSync(resolve(root, 'third_party/compose-spec', target), readFileSync(resolve(root, 'internal/composecheck', source)))
const inputs = Object.fromEntries(['go.mod', 'go.sum', 'frontend/package.json', 'frontend/package-lock.json', 'VERSION'].map(path => [path, sha(readFileSync(resolve(root, path)))]))
writeFileSync(resolve(root, 'third_party/bundled-frontend.json'), JSON.stringify(bundled, null, 2) + '\n')
writeFileSync(resolve(root, 'third_party/dependencies.json'), JSON.stringify({ schema: 1, inputs, policy: 'Conservative dependency inventory; missing notices require review. Referenced marketplace application images are not embedded in NSC.', entries, unresolved: missing.map(({ecosystem,name,version,scope}) => ({ecosystem,name,version,scope})) }, null, 2) + '\n')
const components = entries.map(item => ({ type: 'library', name: item.name, version: item.version, purl: `pkg:${item.ecosystem === 'go' ? 'golang' : 'npm'}/${item.name.replace('@', '%40')}@${item.version}`,
  ...(item.declaredLicense ? { licenses: [{ expression: item.declaredLicense }] } : {}) }))
writeFileSync(resolve(root, 'third_party/sbom.cdx.json'), JSON.stringify({ bomFormat: 'CycloneDX', specVersion: '1.6', version: 1, metadata: { component: { type: 'application', name: 'network-service-composer', version: readFileSync(resolve(root, 'VERSION'), 'utf8').trim() } }, components }, null, 2) + '\n')
console.log(JSON.stringify({ components: entries.length, unresolvedNoticeCopies: missing.length, inventory: 'third_party/dependencies.json' }))
