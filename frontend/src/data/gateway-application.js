const digestPattern = /^[0-9a-f]{64}$/

const matchingDigest = (left, right) =>
  digestPattern.test(left || '') && digestPattern.test(right || '') && left === right

/**
 * A running Gateway only proves that its control connection is available.
 * Count a pool generation as loaded after the Agent has confirmed that exact
 * generation and the in-memory, rendered and on-disk digests still agree.
 */
export function confirmsCurrentPoolConfiguration(gateway, poolRevision) {
  const evidence = gateway.evidence
  if (!evidence || !Number.isSafeInteger(poolRevision) || poolRevision < 1) return false

  return gateway.enabled === true &&
    gateway.status === 'READY' &&
    gateway.desired_config_revision === poolRevision &&
    gateway.applied_config_revision === poolRevision &&
    evidence.loading_confirmed === true &&
    evidence.startup_state === 'CONFIRMED' &&
    evidence.configuration_state === 'CONFIRMED' &&
    evidence.confirmed_revision === poolRevision &&
    evidence.last_confirmed_revision === poolRevision &&
    typeof evidence.last_confirmed_at === 'string' &&
    matchingDigest(gateway.model_digest, evidence.model_digest) &&
    matchingDigest(gateway.rendered_digest, evidence.rendered_digest) &&
    matchingDigest(evidence.disk_digest, evidence.rendered_digest)
}

export function loadedDomainRevisions(domain, gateways, poolRevision) {
  return Object.fromEntries(gateways
    .filter(gateway => gateway.pool_id === domain.pool_id && confirmsCurrentPoolConfiguration(gateway, poolRevision))
    .map(gateway => [gateway.id, domain.revision]))
}
