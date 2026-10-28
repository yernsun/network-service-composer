import assert from 'node:assert/strict'

// The approved Compose validation work changes only the production wizard's
// button admission predicate and HTTP entrypoint summary among the archive-
// frozen functions. TCP/UDP publication is independent of HTTP domain access.
export function adaptComposeWizard(source) {
  const before = "w.step === 4 && (issues.errors.length || !w.reviewAck || issues.warnings.length && !w.warningAck) ? 'disabled' : ''"
  const after = "(w.step === 4 && (issues.errors.length || !w.reviewAck || issues.warnings.length && !w.warningAck || !deploymentReady(w) || !composeSession(w).review || composeSession(w).reviewPending) || w.step === 1 && w.source === 'file' && !validateComposeDocument(w.raw).schema_valid) ? 'disabled' : ''"
  assert.equal(source.split(before).length, 2, 'Compose wizard reference predicate drifted')
  const internal = "w.access === 'internal' ? '仅内部运行'"
  assert.equal(source.split(internal).length, 2, 'HTTP entrypoint summary reference drifted')
  return source.replace(before, after).replace(internal, "w.access === 'internal' ? '不创建 HTTP 域名入口'")
}
