import type { useWorkspaceStore } from './stores/workspace'

type ReadTool = { name: string; description: string; inputSchema: object; annotations: { readOnlyHint: boolean }; execute: (input: unknown) => unknown }
type ToolContext = { registerTool: (tool: ReadTool, options: { signal: AbortSignal }) => void | Promise<void> }

/** Optional, read-only access to the same local workspace visible in the UI. */
export function registerWorkspaceTools(store: ReturnType<typeof useWorkspaceStore>) {
  const context = (document as Document & { modelContext?: ToolContext }).modelContext
  if (!context?.registerTool) return () => {}
  const lifecycle = new AbortController()
  const tools: ReadTool[] = [
    {
      name: 'list_composer_projects',
      description: '列出当前浏览器 Mock 工作区的项目、环境和资源数量。',
      inputSchema: { type: 'object', properties: {}, additionalProperties: false },
      annotations: { readOnlyHint: true },
      execute(input) {
        if (!input || typeof input !== 'object' || Object.keys(input).length) throw new Error('不接受筛选参数。')
        const db = store.state()
        return db.projects.map(project => ({ id: project.id, name: project.name, environments: db.environments.filter(e => e.project === project.id).map(e => ({ id: e.id, name: e.name })), applications: db.apps.filter(a => a.project === project.id).length, domains: db.domains.filter(d => d.project === project.id).length }))
      },
    },
    {
      name: 'read_composer_deployment',
      description: '读取已有 Mock 部署的固定版本、逐目标结果和尝试次数，不执行发布或重试。',
      inputSchema: { type: 'object', properties: { id: { type: 'string' } }, required: ['id'], additionalProperties: false },
      annotations: { readOnlyHint: true },
      execute(input) {
        if (!input || typeof input !== 'object' || !('id' in input) || typeof input.id !== 'string' || Object.keys(input).some(k => k !== 'id')) throw new Error('需要部署记录 id。')
        const operation = store.state().appDeployments.find(o => o.id === input.id)
        if (!operation) throw new Error('部署记录不存在。')
        return { id: operation.id, version: operation.version, state: operation.state, results: { ...operation.results }, attempts: operation.attempts.length }
      },
    },
  ]
  for (const tool of tools) {
    try { void Promise.resolve(context.registerTool(tool, { signal: lifecycle.signal })).catch(() => {}) } catch { /* Optional browser capability. */ }
  }
  return () => lifecycle.abort()
}
