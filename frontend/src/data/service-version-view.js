import { translate } from '../i18n'

export function serviceRevisionLabel(value, emptyKey = 'service.version_not_recorded') {
  return Number.isInteger(value) && value > 0 ? `r${value}` : translate(emptyKey)
}

export function serviceVersionSummary(service, runtimeOnline) {
  if (!service.specVersion) return translate('service.version_not_recorded')
  const desired = `${translate('service.desired_version')} ${serviceRevisionLabel(service.specVersion)}`
  const observed = runtimeOnline
    ? serviceRevisionLabel(service.observedVersion, 'service.version_not_confirmed')
    : translate('community.evidence_state.UNKNOWN')
  return `${desired} / ${translate('service.applied_version')} ${observed}`
}
