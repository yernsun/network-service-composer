export type Id = string
export type Perspective = 'cloud' | 'ops'
export type Result = 'running' | 'success' | 'partial' | 'failed' | 'unknown'
export interface Project { id: Id; key: string; name: string; description: string; created: string; revision?: number; default_runtime_id?: string; deletion_id?: string }
export interface Environment { id: Id; project: Id; slug: string; name: string; kind: string; gateways: Id[]; revision?: number }
export interface Gateway { id: Id; name: string; runtime: string; ip?: string; available?: boolean }
export interface ClientIPSource { header: string; trusted_proxies: string[] }
export interface IPAccessLists { access: string[]; forbidden: string[]; clientIP?: ClientIPSource }
export interface RouteRule { ipAccess?: IPAccessLists; id: Id; name: string; path: string; match: 'prefix' | 'exact' | 'prefer'; kind: 'proxy' | 'return' | 'redirect'; service: Id; externalUrl?: string; timeout: number; connect: number; strip: boolean; websocket: boolean; enabled: boolean; snippet: string; code: number; value: string }
export interface DomainConfig { ipAccess?: IPAccessLists; mode: 'managed' | 'native'; tls: boolean; redirect: boolean; body: string; native: string; serverSnippet: string; routes: RouteRule[] }
export interface Domain { id: Id; name: string; project: Id; env: Id; environmentIds?: Id[]; tls?: { certificate_id?: string; mode?: string; acme_challenge?: string; dns_provider_ref?: string }; gateways: Id[]; rev: number; active: DomainConfig; draft: { base: number; config: DomainConfig; savedAt: string } | null; loaded: Record<Id, number>; drift?: { gateway: string }; revisions: { v: number; config: DomainConfig; time: string; note: string }[] }
export interface Service { id: Id; name: string; project: Id; env: Id; app: Id; runtimeId: Id; managed: boolean; image: string; port: number; desired: number; health: string; status?: string; mode?: string; workloadKind?: string; runtime?: string; endpoints: [string, boolean][]; specVersion: number; observedVersion: number }
export interface Application { management_mode?: 'observed' | 'application' | 'system'; installation_id?: Id; id: Id; key: string; name: string; project: Id; env: Id; runtime: Id; mode: string; managed: boolean; version: number; draft: unknown; revisions: unknown[]; system?: boolean; status?: string }
export interface Instance { id: Id; service: Id; app: Id; runtime: Id; node: Id; kind: 'task' | 'container'; containerId: Id; state: string; current: boolean; health?: string; serviceRuntime?: Id; observed_at?: string; created?: string; updated?: string; stateChanged?: string; containerCreated?: string; started?: string; finished?: string; observed?: string; restartCount?: number | null; logs: string; previous: Id | null; replacement?: Id; exitCode: number | null; oomKilled: boolean | null }
export interface RuntimeNode { id: Id; name: string; role: string; status: string; availability: string; agent: boolean; sample: string }
export interface Runtime { id: Id; name: string; type: 'swarm' | 'docker'; connection: string; canWrite: boolean; enabled?: boolean; inventoryComplete?: boolean; inventoryObservedAt?: string; nodes: RuntimeNode[]; allowedEnvs: Id[] }
export interface Operation { id: Id; app: Id; project: Id; env: Id; runtime: Id; version: number; state: Result; targets: Id[]; results: Record<Id, Result>; snapshot: unknown; attempts: unknown[] }
export interface Variable { value: string; type: 'string' | 'integer' | 'boolean' | 'memory' | 'credential'; note: string }
export interface Database { schema: number; epoch: number; project: Id; env: Id; view: Perspective; projects: Project[]; environments: Environment[]; gateways: Gateway[]; domains: Domain[]; services: Service[]; apps: Application[]; instances: Instance[]; runtimes: Runtime[]; appDeployments: Operation[]; projectDeletionTasks?: Record<string, import('../data/project-lifecycle').ProjectDeletion>; [key: string]: unknown }
export interface ModalSpec { kind?: string; size?: string; title: string; sub?: unknown; body: unknown; footer?: unknown; locked?: boolean; envId?: string }
export interface ToastMessage { id: number; message: string; error: boolean }
