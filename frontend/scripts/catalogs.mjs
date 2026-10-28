import { readFile, mkdir, writeFile } from 'node:fs/promises'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
const root = fileURLToPath(new URL('..', import.meta.url))
export const locales = JSON.parse(await readFile(resolve(root, '../internal/i18n/locales.json'), 'utf8')).map(({ code }) => code)
export async function catalogs() {
  return Object.fromEntries(
    await Promise.all(
      locales.map(async (locale) => {
        const read = async (path) => JSON.parse(await readFile(resolve(root, path), 'utf8'))
        return [
          locale,
          {
            ...(await read(`../internal/i18n/catalogs/${locale}.json`)),
            ...(await read(`src/shared/i18n/locales/${locale}.json`)),
          },
        ]
      }),
    ),
  )
}
if (process.argv.includes('--generate')) {
  const all = await catalogs()
  await mkdir(resolve(root, 'src/shared/i18n/generated'), { recursive: true })
  for (const [locale, flat] of Object.entries(all)) {
    const tree = {}
    for (const [key, message] of Object.entries(flat)) {
      const parts = key.split('.')
      const last = parts.pop()
      let node = tree
      for (const part of parts) node = node[part] ??= {}
      node[last] = key.startsWith('archive.')
        ? message.replace(/(\{[A-Za-z_]\w*\})|([{}@|])/g, (match, placeholder, literal) => placeholder || `{'${literal}'}`)
        : message
    }
    await writeFile(resolve(root, `src/shared/i18n/generated/${locale}.json`), JSON.stringify(tree))
  }
}
