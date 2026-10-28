declare module '@workspace-runtime' {
  import type { Database, ModalSpec, RouteRule } from '../types/domain'
  export interface VariableRow {
    name: string
    value: string
    type: string
    note: string
    source: string
    own: boolean
    references: number
  }
  export interface VariableState {
    layer: string
    app: string
    apps: { id: string; name: string }[]
    query: string
    version: number
    dirty: boolean
    ownCount: number
    projectName: string
    environmentName: string
    rows: VariableRow[]
  }
  export interface VariableFormData { key: string; type: string; value: string; note: string }
  export interface VariableDialogState {
    layer: string
    removeKey: string
    removeReferences: number
    usageKey: string
    usage: { id: string; application: string; environment: string; source: string; shadowed: boolean }[]
    history: { version: number; time: string; note: string; rows: string }[]
  }
  export interface ComposeEditorState {
    id: string
    name: string
    projectName: string
    environmentName: string
    runtimeName: string
    runtimeType: string
    managed: boolean
    environment: string
    previewEnvironment: string
    environments: { id: string; name: string }[]
    view: string
    file: string
    source: string
    dirty: boolean
    savedDraft: boolean
    lastRendered: string
    compiled: {
      text: string
      errors: string[]
      warnings: string[]
      refs: { key: string; value: string; source: string }[]
      runtime: { service: string; env: Record<string, { value: string; source: string }> }[]
    }
    versions: { project: number; environment: number; application: number | null }
  }
  export interface WorkspaceRuntime {
    readonly state: Database
    readonly ui: Record<string, unknown>
    readonly workspace: Record<string, unknown>
    readonly workload: Record<string, unknown>
    readonly modal: ModalSpec | null
    readonly pageTitle: string
    readonly dirty: boolean
    readonly storageWarning: string
    acceptRoute(path: string): void
    acceptPerspective(path: string): void
    sameEditor(a: string, b: string): boolean
    guard(next: () => void, cancel?: () => void): void
    link(attrs: Record<string, unknown>): string | null
    toggleMenu(): void
    openPalette(): void
    paletteItems(query: string): { scope: string; domains: { id: string; name: string; scope: string }[]; services: { id: string; name: string; scope: string }[] }
    selectPaletteItem(kind: string, id?: string): void
    openHelp(): void
    openAbout(): void
    navigatePage(page: string): void
    navigateServices(health?: string): void
    variableState(): VariableState
    variableEditor(): VariableFormData | null
    selectVariableLayer(layer: string): void
    selectVariableApplication(id: string): void
    updateVariableSearch(value: string): void
    saveVariables(): void
    openVariableEditor(key?: string): void
    stageVariable(form: VariableFormData): string
    requestVariableRemoval(key: string): void
    openVariableUsage(key: string): void
    openVariableHistory(): void
    openVariableImport(): void
    variableDialogState(): VariableDialogState
    analyzeVariableImport(raw: string): { errors: string[]; names: string[]; overwrite: number }
    invalidateVariableImport(): void
    applyVariableImport(): void
    confirmVariableRemoval(key: string): void
    openComposeDocument(id: string): void
    composeEditorState(): ComposeEditorState | null
    updateComposeSource(value: string): void
    setComposeView(view: string): void
    setComposeFile(file: string): void
    setComposePreviewEnvironment(id: string): void
    saveComposeDocument(): void
    reviewComposeDocument(): void
    openComposeVariables(): void
    updateServiceSearch(value: string): void
    updateServiceFilter(field: 'scope' | 'runtime' | 'health', value: string): void
    setServiceLayout(value: 'grouped' | 'flat'): void
    stepServicePage(step: number): void
    toggleRemovedServices(): void
    showAllServices(): void
    openService(id: string, tab?: string): void
    selectProject(id: string): void
    selectEnvironment(id: string): void
    selectContextEnvironment(id: string): void
    createEnvironment(): void
    editEnvironment(id: string): void
    addDomainFromEnvironments(): void
    copyProjectConfiguration(): void
    openDomain(id: string): void
    domainStatusView(id: string): unknown
    routeEditor(): { route: RouteRule & { externalUrl?: string }; existing: boolean; targetMode: string; supportsCustomTarget: boolean; error: string; domainName: string; matches: { value: string; label: string }[]; kinds: { value: string; label: string }[]; services: { id: string; name: string; port: number; ready: number }[] } | null
    updateRouteForm(patch: Partial<RouteRule> & { externalUrl?: string }): void
    updateRouteTargetMode(value: string): void
    applyRouteForm(): void
    requestRouteDelete(): void
    domainCoverage(id: string): { known: number; total: number }
    domainHasIssue(id: string): boolean
    updateDomainListSearch(value: string): void
    updateDomainListFilter(value: string): void
    addDomain(): void
    addService(): void
    openApplication(id: string): void
    beginApplicationInstall(source?: string, template?: string): void
    marketTemplates(): { id: string; name: string; cat: string; color: string; glyph: string; desc: string; modes: string[]; version: string; services: unknown[]; stateful: boolean; secret?: boolean }[]
    updateMarketSearch(value: string): void
    updateMarketCategory(value: string): void
    openMarketTemplate(id: string): void
    installMarketTemplate(id: string): void
    resumeApplicationDraft(): void
    applicationState(id: string): { name: string; tone: string }
    serviceState(id: string): { name: string; tone: string; key: string; detail: string }
    openProjectForm(): void
    enterProject(id: string): void
    browseAllProjects(): void
    createProject(form: { name: string; key: string; description: string; runtime: string }): void
    saveEnvironment(form: { name: string; slug: string; kind: string; gateways: string[] }): void
    deleteEnvironment(id: string): void
    readonly formErrorMessage: string
    openScenarios(): void
    openDemoSettings(): void
    iconView(name: string, className?: string): unknown
    closeModal(): void
    setPerspective(view: 'cloud' | 'ops'): void
    view(name: string): unknown
    refresh(): void
    route(): string
    compile(): unknown
    effective(project: string, environment: string, application: string): unknown
    dispose(): void
  }
  export function createMockService(hooks: {
    navigate(path: string): Promise<unknown>
    syncRoute(path: string, replace: boolean): void
    render(): void
    toast(message: string, error: boolean): void
    data?: unknown
  }): WorkspaceRuntime
}
