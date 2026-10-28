import { asRows, request, type Row } from '../shared/api/client'
import type { paths } from '../shared/api/schema'
export async function allPages(
  path: keyof paths,
  params: Record<string, string> = {},
  signal?: AbortSignal,
  query: Record<string, unknown> = {},
): Promise<Row[]> {
  const rows: Row[] = []
  let cursor = ''
  const seen = new Set<string>()
  do {
    const result = await request('get', path, {
      params: { path: params, query: { ...query, limit: 200, ...(cursor ? { cursor } : {}) } },
      ...(signal ? { signal } : {}),
    })
    rows.push(...asRows(result.data))
    cursor = result.pagination?.next_cursor ?? ''
    if (cursor && seen.has(cursor)) throw new Error('Repeated pagination cursor')
    seen.add(cursor)
  } while (cursor)
  return rows
}
