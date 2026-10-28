import { describe, expect, it } from 'vitest'
import { parse } from 'yaml'
import { readableCompose, serviceChanges, lineDiff, diffSource } from '../../src/compose/source'

describe('persisted Compose source and changes', () => {
 it('shows a large JSON source as YAML without changing types or the original', () => {
  const config = { services: { web: { image: 'web:1', environment: { CODE: '0123', FLAG: 'false', QUOTE: 'yes', LARGE: 'x'.repeat(85000) }, deploy: { replicas: 2 } } } }
  const raw = JSON.stringify(config), shown = readableCompose(raw)
  expect(raw.split('\n')).toHaveLength(1)
  expect(shown).toContain('services:\n')
  expect(parse(shown)).toEqual(config)
  expect(readableCompose(shown)).toBe(shown)
 })
 it('preserves YAML comments, anchors and invalid editing drafts verbatim', () => {
  for (const raw of ['# keep\nx-base: &base {image: web:1}\nservices: {web: *base}\n', 'services:\n [', '{ bad']) expect(readableCompose(raw)).toBe(raw)
 })
 it('does not classify omitted services as deletions', () => {
  expect(serviceChanges('services: {old: {image: old:1}}', 'services: {next: {image: next:1}}')).toEqual([{ name: 'next', kind: 'added' }, { name: 'old', kind: 'retained' }])
 })
 it('compares JSON and YAML semantically and exposes credential-only changes in the summary', () => {
  const a = '{"services":{"web":{"image":"web:1","environment":{"PASSWORD":"secret-one"}}}}'
  const b = readableCompose(a).replace('secret-one', 'secret-two')
  expect(serviceChanges(a, readableCompose(a))[0].kind).toBe('unchanged')
  expect(serviceChanges(a, b)[0].kind).toBe('updated')
  expect(diffSource(a)).not.toContain('secret-one')
  expect(diffSource(b)).not.toContain('secret-two')
 })
 it('returns real line changes for files longer than 600 lines', () => {
  const a = Array.from({ length: 3000 }, (_, i) => `line ${i}`).join('\n')
  const result = lineDiff(a, a.replace('line 1800\n', 'replacement\n'))
  expect(result.filter(r => r.type === 'remove')).toEqual([{ type: 'remove', text: 'line 1800', old: 1801, next: null }])
  expect(result.filter(r => r.type === 'add')).toEqual([{ type: 'add', text: 'replacement', old: null, next: 1801 }])
  expect(result.filter(r => r.type === 'same')).toHaveLength(2999)
 })
})
it('handles completely different long inputs without allocating a quadratic trace', () => {
 const a = Array.from({ length: 10000 }, (_, i) => `before ${i}`).join('\n')
 const b = Array.from({ length: 10000 }, (_, i) => `after ${i}`).join('\n')
 const rows = lineDiff(a, b)
 expect(rows.filter(r => r.type === 'remove')).toHaveLength(10000)
 expect(rows.filter(r => r.type === 'add')).toHaveLength(10000)
})
it('retains shared context in a large rewrite with bounded trace memory', () => {
 const a = Array.from({ length: 10000 }, (_, i) => i === 5000 ? 'shared context' : `old ${i}`)
 const b = Array.from({ length: 10000 }, (_, i) => i === 5100 ? 'shared context' : `new ${i}`)
 const rows = lineDiff(a.join('\n'), b.join('\n'))
 expect(rows.filter(r => r.type === 'same')).toEqual([{ type: 'same', text: 'shared context', old: 5001, next: 5101 }])
 expect(rows.filter(r => r.type !== 'add').map(r => r.text)).toEqual(a)
 expect(rows.filter(r => r.type !== 'remove').map(r => r.text)).toEqual(b)
})
