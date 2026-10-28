import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { parse } from '@babel/parser'
import traverseModule from '@babel/traverse'
import generateModule from '@babel/generator'
import { adaptServiceReferences, serviceReferenceEdits } from './service-references.mjs'
const traverse = traverseModule.default, generate = generateModule.default
const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')

test('every approved template edit is presentation text, never an identity or form attribute', () => {
  const templates = JSON.parse(read('reference/views/blueprints.json'))
  for (const rule of Object.values(serviceReferenceEdits).flat().filter(r => r.template !== undefined)) {
    const marker = `NSCEXPR${rule.index}END`
    let text = false
    function inspect(node) {
      if (Array.isArray(node)) return node.forEach(inspect)
      if (typeof node === 'string') { text ||= node.includes(marker); return }
      if (node.text) text ||= node.text.includes(marker)
      for (const [key, value] of node.attrs || []) if (value.includes(marker)) {
        assert.equal(key, 'aria-label', `Identity attribute edit: ${rule.template}:${rule.index}`)
        assert.equal(rule.attribute, key, `Undeclared attribute edit: ${rule.template}:${rule.index}`)
        text = true
      }
      for (const child of node.children || []) inspect(child)
    }
    inspect(templates[rule.template])
    assert.ok(text, `Missing text slot: ${rule.template}:${rule.index}`)
  }
})

test('name adaptation preserves route identity, submission and navigation functions', () => {
  const source = read('reference/workspace.js')
  const names = new Set(['openRoute', 'checkRoute', 'applyRoute', 'confirmOp', 'saveDomainTargets', 'selectScope', 'switchScope', 'routePath', 'linkFor', 'guard'])
  function functions(source) {
    const result = []
    traverse(parse(source, { sourceType: 'module' }), { FunctionDeclaration(path) {
      if (names.has(path.node.id.name)) result.push(generate(path.node, { comments: false, compact: true }).code)
    } })
    return result
  }
  assert.deepEqual(functions(adaptServiceReferences(source, 'workspace')), functions(source))
  const renderer = read('reference/views/domains/renderers.js')
  assert.throws(() => adaptServiceReferences(renderer.replace('esc(sel?.service', 'esc(sel?.wrong_identity'), 'domains'), /edit drifted/)
})
