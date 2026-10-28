import { readFile, readdir } from 'node:fs/promises'
import { extname, join, relative } from 'node:path'

const root = new URL('../', import.meta.url)
const targets = ['package.json', 'package-lock.json', 'src', 'tests']
const sourceExtensions = new Set(['.css', '.json', '.ts', '.tsx', '.vue'])
const forbidden = [
  ['prime', 'vue'].join(''),
  ['prime', 'ui'].join(''),
  ['prime', 'icons'].join(''),
  ['prime', 'locale'].join(''),
  ['@prime', 'uix'].join(''),
  /\.p-[a-z]/,
]

async function files(path) {
  const entries = await readdir(path, { withFileTypes: true })
  const found = []
  for (const entry of entries) {
    if (['node_modules', 'coverage', 'test-results'].includes(entry.name)) continue
    const child = join(path, entry.name)
    if (entry.isDirectory()) found.push(...(await files(child)))
    else if (sourceExtensions.has(extname(child))) found.push(child)
  }
  return found
}

const paths = []
for (const target of targets) {
  const path = new URL(target, root).pathname
  if (target.includes('.')) paths.push(path)
  else paths.push(...(await files(path)))
}

const violations = []
for (const path of paths) {
  const source = (await readFile(path, 'utf8')).toLocaleLowerCase()
  if (forbidden.some((value) => (value instanceof RegExp ? value.test(source) : source.includes(value))))
    violations.push(relative(root.pathname, path))
}
if (violations.length) {
  console.error(`Forbidden legacy UI coupling: ${violations.join(', ')}`)
  process.exitCode = 1
} else {
  console.log(`Native UI dependency boundary passed (${paths.length} files scanned)`)
}
