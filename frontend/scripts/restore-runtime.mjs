import { adaptBrand } from './brand.mjs'
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { parse } from '@babel/parser'
import traverseModule from '@babel/traverse'
import { adaptEmptyProjectScope } from './project-scope.mjs'
import { adaptActionableNavigation } from './actionable-cards.mjs'
import { adaptProjectRuntime } from './project-lifecycle.mjs'
const traverse = traverseModule.default
const source = adaptBrand(readFileSync(new URL('../reference/workspace.js', import.meta.url), 'utf8'))
const root = new URL('../src/', import.meta.url)
const ast = parse(source, { sourceType: 'module' })
const edits = []
const change = (start, end, text) => edits.push({ start, end, text })
const frozen = ['selectScope', 'switchScope', 'navigate', 'jump', 'routePath', 'parse', 'setScope', 'environmentHref', 'environmentAction', 'projectHref', 'scopeListPage', 'concreteScopeHref', 'linkFor', 'guard', 'editorKey']
const hashes = {}
const unsupported = new Set(['saveDraft', 'startRelease', 'simulateDeployment', 'retryDeployment', 'makeDraftDomain', 'createDomain', 'createService', 'finishCopy', 'saveDomainTargets', 'saveWizard', 'newInstance', 'appendEvent', 'applyServiceSnapshot', 'maybeCreateAccessDraft', 'createOperation', 'runAttempt', 'retryOperation', 'verifiedResult', 'initializeImportedApp', 'setScenario'])
const commands = new Set(['createProject', 'saveEnvironment', 'saveVars', 'saveDoc', 'deployWizard', 'confirmOp', 'saveRestore', 'deployRestore', 'saveConnection', 'reviewCompose', 'deployCompose', 'validationModal', 'runDiagnostic'])
traverse(ast, {
  FunctionDeclaration(path) {
    const name = path.node.id.name
    if (frozen.includes(name)) hashes[`${name}:${path.node.start}`] = createHash('sha256').update(source.slice(path.node.start, path.node.end)).digest('hex')
    let body
    if (unsupported.has(name)) body = `return hooks.data.unsupported(${JSON.stringify(name)}, formError);`
    if (commands.has(name)) body = `return hooks.data.command(${JSON.stringify(name)}, arguments);`
    if (name === 'seedV10' || name === 'seedWorkloads' || name === 'seed') body = 'return hooks.data.state;'
    if (name === 'persist' || name === 'event') body = 'return;'
    if (name === 'composePage') body = 'return hooks.data.composePage();'
    if (name === 'runtimeEventsPage') body = 'return hooks.data.timelinePage ? hooks.data.timelinePage() : __applicationsViews.runtimeEventsPage_62(__renderContext1, ...args);'
    if (name === 'eventsTable') body = `if (hooks.data.timelinePage && ui.page === 'application' && x.appTab === 'events') return hooks.data.timelinePage({ applicationID: x.app });
      if (hooks.data.timelinePage && ui.page === 'service' && x.serviceTab === 'events') return hooks.data.timelinePage({ serviceID: x.service });
      return __applicationsViews.eventsTable_61(__renderContext1, ...args);`
    if (name === 'ensureDoc') body = `return a ? db.parameters13.documents[a.id] : null;`
    if (name === 'offline') body = `return db.gateways.find(g => g.id === gid)?.available !== true;`
    if (name === 'syncInfo') body = `return { known: 0, total: d.gateways.length, mixed: false, unknown: true };`
    if (name === 'appSpec') body = `return hooks.data.applicationSpec(a);`
    if (body !== undefined) change(path.node.body.start + 1, path.node.body.end - 1, `\n    ${body}\n  `)
  },
  VariableDeclarator(path) {
    if (path.node.id.name === 'TEMPLATES') change(path.node.init.start, path.node.init.end, 'hooks.data.templates')
    if (path.node.id.name === 'tpl') change(path.node.init.start, path.node.init.end, 'id => hooks.data.template(id)')
  },
  AssignmentExpression(path) {
    const name = path.node.left.name
    if (path.node.right.type === 'FunctionExpression' && unsupported.has(name)) {
      const body = path.node.right.body
      change(body.start + 1, body.end - 1, `\n    return hooks.data.unsupported(${JSON.stringify(name)}, formError);\n  `)
    }
  },
  SwitchCase(path) {
    const action = path.node.test?.value
    if (action === 'p-env-delete-confirm') {
      const first = path.node.consequent[0], last = path.node.consequent.at(-1)
      change(first.start, last.end, 'hooks.data.command("deleteEnvironment", [id]); break;')
    }
    if (['reset', 'restore-confirm', 'drift-adopt', 'drift-restore-confirm', 'conflict-rebase', 'x-demo-save', 'x-runtime-offline', 'x-runtime-online', 'x-runtime-toggle'].includes(action)) {
      const first = path.node.consequent[0], last = path.node.consequent.at(-1)
      if (first) change(first.start, last.end, `hooks.data.unsupported(${JSON.stringify(action)}, formError); break;`)
    }
  },
})
// Data initialization is separate from the original navigation state machine.
const begin = source.indexOf('  try {\n    const raw = localStorage.getItem(KEY);')
const end = source.indexOf('  let ui =', begin)
change(begin, end, '  db = hooks.data.state;\n')
const storageStart = source.lastIndexOf("  listen(window, 'storage'")
if (storageStart >= 0) {
  const storageEnd = source.indexOf('\n  function emptyConfig', storageStart)
  change(storageStart, storageEnd, '')
}
const recoveryStart = source.indexOf('    // A browser refresh is not evidence')
const recoveryEnd = source.indexOf('    // Test/read-only inspection hook', recoveryStart)
change(recoveryStart, recoveryEnd, '')
// Expose only existing controller operations to asynchronous data completion.
const attach = source.indexOf('  booting = false;\n  return {')
change(attach, attach, `  hooks.data.attach({
    get db() { return db }, get ui() { return ui }, get workspace() { return NSCWorkspace },
    get workload() { return NSCBridge }, get modal() { return modal }, get work() { return work },
    get guardState() { return guardState }, get ENV() { return ENV },
    render: () => render(), showModal, closeModal, formError, toast, selectScope, updateHash, redrawDiag,
    restoreFields, baseClose, renderModal,
  });\n`)
const bridge = source.indexOf('    NSCBridge = {')
change(bridge, bridge, '')
const xentry = source.indexOf('      x,\n', bridge)
change(xentry, xentry, '      renderWizard, wizardServices, previewWizard, wizardMode, wizardIssues, collectWizard,\n')
// Discard nested replacements when their containing data function was replaced.
const outer = edits.filter(e => !edits.some(p => p !== e && p.start <= e.start && p.end >= e.end && (p.start < e.start || p.end > e.end)))
let real = source
for (const e of outer.sort((a, b) => b.start - a.start)) real = real.slice(0, e.start) + e.text + real.slice(e.end)
real = real.replace("from './compiler.js'", "from '../mock/compiler.js'")
real = real.replace('clone(db.domains[0].draft?.config || db.domains[0].active)', 'clone(db.domains.find(d => d.project === db.project && d.env === db.env)?.active || emptyConfig())')
real = real.replace("domain: 'd1',", "domain: null,")
real = real.replace('storageAvailable = true', 'storageAvailable = false')
real = real.replaceAll('db.projects[0].id', "(db.projects[0]?.id || '')").replaceAll('projectEnvs()[0].id', "(projectEnvs()[0]?.id || '')")
// Shared original view modules resolve relative to both runtime directories.
mkdirSync(new URL('runtime/', root), { recursive: true })
writeFileSync(new URL('runtime/real.js', root), adaptActionableNavigation(adaptProjectRuntime(adaptEmptyProjectScope(real))))
writeFileSync(new URL('../reference/navigation-hashes.json', import.meta.url), JSON.stringify(hashes, null, 2) + '\n')
console.log(`Restored API controller from ${Object.keys(hashes).length} recorded navigation functions with the explicit empty-project scope adaptation`)
