import type { ClientIPSource, DomainConfig } from '../types/domain'
export interface IPAccessEditorAdapter {
  ipAccessEditorState(): { domainID: string; domainName: string; work: DomainConfig } | null
  updateIPAccess(routeID: string, list: 'access' | 'forbidden', raw: string): void
  updateClientIP(source: ClientIPSource | null): void
  previewIPAccess(): Promise<string | null>
}
