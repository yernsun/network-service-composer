import assert from 'node:assert/strict'
import { parse } from '@babel/parser'
import traverseModule from '@babel/traverse'
import generateModule from '@babel/generator'
import * as t from '@babel/types'
const traverse = traverseModule.default, generate = generateModule.default

// Explicit lifecycle extensions retain the archive's navigation and modal steps.
export function adaptProjectLifecycle(source, module) {
  if (module !== 'projects') return source
  const ast = parse(source, { sourceType: 'module' }); let matches = 0
  traverse(ast, { CallExpression(path) {
    const call = path.node
    if (!t.isMemberExpression(call.callee) || call.callee.property.name !== 'B' || call.arguments[1]?.value !== 'p-project') return
    assert.equal(call.arguments.length, 5)
    const settings = t.callExpression(t.cloneNode(call.callee), [
      t.callExpression(t.identifier('_t'), [t.stringLiteral('projectLifecycle.settings')]),
      t.stringLiteral('p-project-settings'), t.stringLiteral('settings'), t.stringLiteral('sm'), t.cloneNode(call.arguments[4], true),
    ])
    path.replaceWith(t.callExpression(t.identifier('__nscVueAdd'), [call, settings])); path.skip(); matches++
  } })
  assert.equal(matches, 1, 'Original project card footer changed')
  return generate(ast, { comments: true }).code
}

export function adaptProjectRuntime(source, demo = false) {
  const ast = parse(source, { sourceType: 'module' }); let forms = 0, projects = 0, environments = 0
  traverse(ast, {
    CallExpression(path) {
      if (path.node.callee.name !== '__nscVueView' || path.node.arguments[0]?.property?.value !== 219) return
      assert.equal(path.getFunctionParent().node.id.name, 'projectModal')
      path.replaceWith(t.callExpression(t.identifier('_projectRuntimeForm'), [path.node, t.memberExpression(t.identifier('db'), t.identifier('runtimes'))]))
      path.skip(); forms++
    },
    VariableDeclarator(path) {
      if (!demo || path.getFunctionParent()?.node.id?.name !== 'createProject') return
      const id = path.node.id.name
      if (!['p', 'e'].includes(id)) return
      assert.equal(path.node.init.type, 'ObjectExpression')
      path.node.init.properties.push(t.objectProperty(t.identifier(id === 'p' ? 'default_runtime_id' : 'runtime_id'), id === 'p'
        ? t.callExpression(t.identifier('_demoProjectRuntime'), [t.memberExpression(t.identifier('db'), t.identifier('runtimes'))])
        : t.memberExpression(t.identifier('p'), t.identifier('default_runtime_id'))))
      if (id === 'p') projects++; else environments++
    },
  })
  assert.equal(forms, 1, 'Original project creation form changed')
  assert.equal(projects, demo ? 1 : 0); assert.equal(environments, demo ? 1 : 0)
  ast.program.body.unshift(t.importDeclaration([
    t.importSpecifier(t.identifier('_projectRuntimeForm'), t.identifier('projectRuntimeForm')),
    ...(demo ? [t.importSpecifier(t.identifier('_demoProjectRuntime'), t.identifier('demoProjectRuntime'))] : []),
  ], t.stringLiteral('../data/project-runtime')))
  return generate(ast, { comments: true }).code
}
