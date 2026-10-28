// Production confirmation describes the real Controller action; the demo keeps
// its original simulation text and the supplied blueprint remains unmodified.
export function deploymentBlueprints(value) {
  if (Array.isArray(value)) return value.map(deploymentBlueprints)
  if (!value || typeof value !== 'object') return value
  const copy = Object.fromEntries(Object.entries(value).map(([key, item]) => [key, deploymentBlueprints(item)]))
  if (copy.i18n === 'archive.message_9ef91f682b7a') copy.i18n = 'compose.confirmDeployment'
  if (copy.i18n === 'archive.message_ccb178a59049') copy.i18n = 'compose.credentialsHint'
  if (copy.i18n === 'archive.message_ce1dacf1a2a7') copy.i18n = 'ports.noDomainDefault'
  return copy
}
