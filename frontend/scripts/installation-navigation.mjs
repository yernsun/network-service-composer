import assert from 'node:assert/strict'

// Add the approved system-maintenance route to the archived interaction contract
// without changing scope selection, editor guards or any existing destination.
export function adaptInstallationNavigation(source) {
  const edits = [
    ["} else if (ui.page === 'ha')", "} else if (ui.page === 'installation') { path = '/infrastructure/installation'; q = Y.installationJob ? {job:Y.installationJob} : {}; } else if (ui.page === 'ha')"],
    ["if (a[0] === 'infrastructure') {", "if (a[0] === 'infrastructure') { if (a[1] === 'installation' && a.length === 2) { ui.page = 'installation'; Y.installationJob = q.get('job') || ''; return; }"],
    ["if (page === 'runtimes') return", "if (page === 'installation') return '/infrastructure/installation'; if (page === 'runtimes') return"],
  ]
  for (const [before, after] of edits) {
    assert.equal(source.split(before).length, 2, `Installation navigation anchor changed: ${before}`)
    source = source.replace(before, after)
  }
  return source
}
