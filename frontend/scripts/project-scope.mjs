import assert from 'node:assert/strict'

// The archive seed always has an environment. Real legacy projects may have
// none: keep their project pages reachable without inventing an environment or
// weakening validation of explicit environment/resource URLs. All populated
// project navigation and the original leave guard remain unchanged.
export function adaptEmptyProjectScope(source) {
  function replace(before, after) {
    assert.equal(source.split(before).length, 2, `Empty project adaptation anchor changed: ${before}`)
    source = source.replace(before, after)
  }
  replace(
    'if (!P(project) || !e || e.project !== project) return false;\n    db.project = project;\n    db.env = env;',
    'if (!P(project) || (env ? !e || e.project !== project : projectEnvs(project).length > 0)) return false;\n    db.project = project;\n    db.env = env || "";',
  )
  replace('function switchScope(project, env = null, page = null) {', `function switchScope(project, env = null, page = null) {
    if (!env && P(project) && !projectEnvs(project).length) {
      ensureNavigate(() => hooks.navigate('/projects/' + enc(project) + query(db.view === 'ops' ? { perspective: 'ops' } : {})));
      return;
    }`)
  replace(
    "if (!e || e.project !== p.id) return bad(path, '项目与环境不匹配。');\n      setScope(p.id, e.id);",
    `const projectOnly = a.length === 2 || a.length === 3 && ['environments', 'variables'].includes(a[2]) || aggregate;
      if (e ? e.project !== p.id : !projectOnly || projectEnvs(p.id).length > 0) return bad(path, '项目与环境不匹配。');
      setScope(p.id, e?.id || '');`,
  )
  replace(
    "Y.app = q.get('app') || db.apps.find(a => a.project === p.id && a.env === e.id)?.id || '';",
    "Y.app = q.get('app') || db.apps.find(a => a.project === p.id && a.env === e?.id)?.id || '';",
  )
  replace("if (a === 'nav') {\n      const page = el.dataset.page;", `if (a === 'nav') {
      const page = el.dataset.page;
      if (!allProjectView() && P() && !projectEnvs().length) {
        if (page === 'variables') return '/projects/' + enc(db.project) + '/variables';
        if (aggregatePages.includes(page)) return projectHref(db.project, page);
      }`)
  replace("if (!p) return bad(path, '项目不存在。');", "if (!p && !globalScope) return bad(path, '项目不存在。');")
  replace("const e = a[2]", "const e = !p ? null : a[2]")
  replace("setScope(p.id, e?.id || '');", "if (p) setScope(p.id, e?.id || ''); else { db.project = ''; db.env = ''; }")
  replace("projectEnvs(p.id).length > 0", "p && projectEnvs(p.id).length > 0")
  return source
}

// Apply to shared generated views, never the immutable reference templates.
export function adaptScopeHeading(source, module) {
  if (module !== 'variables') return source
  const before = "__viewContext.esc(__nscVueAdd(__nscVueAdd(__viewContext.P()?.name, ' / '), __viewContext.E()?.name))"
  assert.equal(source.split(before).length, 2, 'Scope heading anchor changed')
  return source.replace(before, "__viewContext.esc([__viewContext.P()?.name, __viewContext.E()?.name].filter(Boolean).join(' / ') || '全部项目')")
}
