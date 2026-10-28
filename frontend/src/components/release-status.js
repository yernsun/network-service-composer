import { h } from 'vue'
import { translate as t } from '../i18n'
import { View } from './view'
import { statusTone } from '../data/status-tone'

const knownStatuses = new Set(['PENDING', 'RUNNING', 'PUBLISHING', 'SUCCEEDED', 'FAILED', 'PARTIAL', 'UNKNOWN', 'STOPPED'])
const releaseTone = status => statusTone(status || 'UNKNOWN', 'operation')

function statusLabel(status) {
  if (knownStatuses.has(status)) return t(`community.state.${status}`)
  return status ? String(status) : t('community.evidence_state.UNKNOWN')
}

function statusBadge(status) {
  return h('span', { class: ['badge', releaseTone(status)] }, statusLabel(status))
}

export function releaseStatusView(release, note = '', noteError = false) {
  const targets = Array.isArray(release.targets) ? release.targets : []
  return new View([h('section', { class: 'pool-release-result', 'data-tone': releaseTone(release.status), role: 'status', 'aria-live': 'polite' }, [
    h('div', { class: 'pool-release-overall' }, [
      h('strong', t('domainPublication.releaseResult')),
      statusBadge(release.status),
    ]),
    note ? h('p', { class: ['pool-release-note', noteError && 'is-error'] }, note) : null,
    h('h3', { class: 'pool-release-target-label' }, t('domainPublication.targetGateways')),
    h('ul', { class: 'pool-release-targets' }, targets.map(target => h('li', { key: target.gateway_id || target.name, 'data-tone': releaseTone(target.status) }, [
      h('span', { class: 'pool-release-gateway' }, target.name || target.gateway_id || '—'),
      statusBadge(target.status),
      target.error ? h('p', { class: 'pool-release-error' }, target.error) : null,
    ]))),
  ])])
}
