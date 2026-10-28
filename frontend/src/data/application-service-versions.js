function serviceName(service, application) {
  const prefix = `${application?.project_name || ''}_`
  if (prefix !== '_' && service.service_key?.startsWith(prefix)) return service.service_key.slice(prefix.length)
  return service.name || service.service_key || ''
}

function revisionSequence(revisionID, name, revisions) {
  if (!revisionID) return null
  const revision = revisions.find(candidate => candidate.id === revisionID)
  if (!revision) return null
  const serviceRevisionID = revision.configuration_source?.service_revisions?.[name]
  const serviceRevision = serviceRevisionID && revisions.find(candidate => candidate.id === serviceRevisionID)
  return serviceRevision?.sequence ?? revision.sequence ?? null
}

/**
 * Desired and applied revisions are different control-plane facts. Incremental
 * Stack revisions may point each service at an older immutable service head.
 */
export function applicationServiceVersions(service, application, revisions = []) {
  if (!application) return { desired: null, applied: null }
  const name = serviceName(service, application)
  return {
    desired: revisionSequence(application.desired_revision_id || application.active_revision_id, name, revisions),
    applied: revisionSequence(application.active_revision_id, name, revisions),
  }
}
