import type { components } from '../shared/api/schema'

type Credentials = components['schemas']['DNSProviderCredentials']
export type CredentialKey = Exclude<keyof Credentials, 'tokens'>
export const providerFields: Record<string, CredentialKey[]> = {
  cloudflare: ['api_token', 'zone_api_token', 'email', 'api_key'],
  alidns: ['access_key', 'secret_key', 'security_token', 'ram_role'],
  tencentcloud: ['secret_id', 'secret_key', 'session_token'],
  ovh: ['application_key', 'application_secret', 'consumer_key'],
  hurricane: [],
}
export const credentialLabels: Record<CredentialKey, string> = {
  api_token: 'API Token', zone_api_token: 'Zone API Token', email: 'Email', api_key: 'API Key',
  access_key: 'Access Key', secret_key: 'Secret Key', security_token: 'Security Token', ram_role: 'RAM Role',
  secret_id: 'Secret ID', session_token: 'Session Token', application_key: 'Application Key',
  application_secret: 'Application Secret', consumer_key: 'Consumer Key',
}
export function authMode(provider: string, fields: string[]): string {
  if (provider === 'cloudflare') return fields.includes('api_token') || !fields.includes('api_key') ? 'token' : 'key'
  if (provider === 'alidns') return fields.includes('ram_role') ? 'role' : 'key'
  return ''
}
export function visibleFields(provider: string, mode: string): CredentialKey[] {
  if (provider === 'cloudflare') return mode === 'key' ? ['email', 'api_key'] : ['api_token', 'zone_api_token']
  if (provider === 'alidns') return mode === 'role' ? ['ram_role'] : ['access_key', 'secret_key', 'security_token']
  return providerFields[provider] || []
}
