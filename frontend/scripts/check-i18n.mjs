import { readFile, readdir } from 'node:fs/promises'
import { resolve } from 'node:path'
import { catalogs, locales } from './catalogs.mjs'
const all = await catalogs()
const english = all.en
const errors = []
const placeholders = (value) =>
  [...value.matchAll(/\{([^{}]+)\}/g)]
    .map((m) => m[1])
    .sort()
    .join(',')
for (const locale of locales) {
  if (Object.keys(all[locale]).sort().join() !== Object.keys(english).sort().join())
    errors.push(`${locale}: keys differ`)
  for (const [key, value] of Object.entries(all[locale])) {
    if (!value.trim() || placeholders(value) !== placeholders(english[key] ?? ''))
      errors.push(`${locale}: ${key}`)
  }
}
async function inspect(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = resolve(dir, entry.name)
    if (entry.isDirectory()) await inspect(path)
    else if (/\.(vue|ts)$/.test(path)) {
      const source = await readFile(path, 'utf8')
      for (const match of source.matchAll(/\b(?:t|\$t)\(\s*['"]([^'"]+)['"]/g)) {
        if (match[1].endsWith('.')) continue
        if (!(match[1] in english)) errors.push(`${path}: missing ${match[1]}`)
      }
    }
  }
}
await inspect(resolve('src'))
if (errors.length) throw new Error(errors.join('\n'))
console.log(
  `Verified ${locales.length} languages, ${Object.keys(english).length} keys and placeholders`,
)
