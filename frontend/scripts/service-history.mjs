import assert from 'node:assert/strict'
import { parse, parseExpression } from '@babel/parser'
import traverseModule from '@babel/traverse'
import generateModule from '@babel/generator'
const traverse = traverseModule.default, generate = generateModule.default

// Explicitly approved service history and Runtime-scoped instance selection.
// Explicitly approved history navigation and current-inventory counts. Keep the
// archived sources immutable and continue comparing all other interactions.
export function adaptHistoryNavigation(source) {
  const edits = [
    ["if (ui.page === 'services') q = {", "if (ui.page === 'services') q = { include_removed: W.x.includeRemoved ? 'true' : '',"],
    ["W.x.svcSearch = q.get('q') || '';", "W.x.svcSearch = q.get('q') || ''; W.x.includeRemoved = q.get('include_removed') === 'true';"],
    ["q: W.x.svcSearch\n", "q: W.x.svcSearch, include_removed: W.x.includeRemoved ? 'true' : ''\n"],
    ["if (db.view !== 'cloud') q.perspective = db.view;\n    return __nscVueAdd(path, query(q));", "if (ui.page === 'service' && W.ST(W.x.service)?.status === 'REMOVED') q.include_removed = 'true'; if (db.view !== 'cloud') q.perspective = db.view; return __nscVueAdd(path, query(q));"],
  ]
  for (const [before, after] of edits) {
    assert.ok(source.includes(before), `History navigation anchor changed: ${before}`)
    source = source.replaceAll(before, after)
  }
  return source
}

export function adaptHistoryViews(source, module) {
  const edits = {
    applications: [
      ["__viewContext.RT(t.runtime)?.connection !== 'online'", "_observationUnknown(__viewContext.RT(t.runtime))"],
      ['__viewContext.db.instances.filter(t => t.service === s.id && (__viewContext.x.includeHistory || t.current))', '__viewContext.currentInstances(s, __viewContext.x.includeHistory)'],
      ['__viewContext.inListScope(s) && s.app', "__viewContext.inListScope(s) && s.app && s.status !== 'REMOVED'"],
      ['ss.filter(s => s.project === pid).length', "ss.filter(s => s.project === pid && s.status !== 'REMOVED').length"],
      ['group.map(s => s.app)', "group.filter(s => s.status !== 'REMOVED').map(s => s.app)"],
      ['group.length', "group.filter(s => s.status !== 'REMOVED').length"],
      ["__viewContext.B('查看所有项目服务', 'x-all-services', 'layers')", "__nscVueAdd(__viewContext.B(_t('services.show_removed'), 'x-removed-toggle', '', __viewContext.x.includeRemoved ? 'active' : '', `aria-pressed=\"${__viewContext.x.includeRemoved}\"`), __viewContext.B('查看所有项目服务', 'x-all-services', 'layers'))"],
    ],
    projects: [['s.project === p.id', "s.status !== 'REMOVED' && s.project === p.id"]],
    domains: [['s.env === __viewContext.db.env && s.project === __viewContext.db.project', "s.status !== 'REMOVED' && s.env === __viewContext.db.env && s.project === __viewContext.db.project"]],
    shell: [['_serviceSearch(__viewContext.db.services, s.id).includes(q)', "s.status !== 'REMOVED' && _serviceSearch(__viewContext.db.services, s.id).includes(q)"]],
  }[module]
  if (!edits) return source
  const ast = parse(source, { sourceType: 'module' })
  const compact = node => generate(node, { compact: true, comments: false }).code
  for (const [before, after] of edits) {
    const expected = compact(parseExpression(before))
    let found = 0
    traverse(ast, { enter(path) {
      if (!path.isExpression() || compact(path.node) !== expected) return
      path.replaceWith(parseExpression(after)); path.skip(); found++
    } })
    assert.ok(found, `History view anchor changed: ${module}: ${before}`)
  }
  return (module === 'applications' ? "import { observationUnknown as _observationUnknown } from '../../data/service-instances';\n" : '') + generate(ast).code
}
