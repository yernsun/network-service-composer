import assert from 'node:assert/strict'

// Approved system-resource labels only; the archive's navigation, tabs and
// action order remain intact. The shared output serves real and demo modes.
export function adaptSystemApplications(source, module) {
  if (module !== 'applications') return source
  const edits = [
    ["a.managed ? 'NSC 管理' : '外部管理 · 只读'", "a.system ? _t('systemApplication.label') : a.managed ? 'NSC 管理' : '外部管理 · 只读'"],
    ["a.managed ? '通过 NSC 部署与维护' : '原工具继续管理，NSC 只读观察'", "a.system ? _t('systemApplication.protected') : a.managed ? '通过 NSC 部署与维护' : '原工具继续管理，NSC 只读观察'"],
    ["__viewContext.callout('只观察此应用，不会从发现信息反推完整部署文件。取得原始定义并通过接管预检后，才能在 NSC 更新。', 'amber')", "__viewContext.callout(a.system ? _t('systemApplication.protected') : '只观察此应用，不会从发现信息反推完整部署文件。取得原始定义并通过接管预检后，才能在 NSC 更新。', 'amber')"],
  ]
  for (const [from, to] of edits) {
    assert.ok(source.includes(from), `System application adaptation drift: ${from}`)
    source = source.replaceAll(from, to)
  }
  source = source.replaceAll('__viewContext.appState(a)', "(a.system && a.status === 'UNKNOWN' ? { name: _t('community.evidence_state.UNKNOWN'), tone: 'amber' } : __viewContext.appState(a))")
  source = source.replaceAll('__viewContext.esc(a.source)', "__viewContext.esc(a.system ? _t('systemApplication.label') : a.source)")
  source = source.replaceAll('a.version', "(a.system ? '—' : a.version)")
  return source
}
