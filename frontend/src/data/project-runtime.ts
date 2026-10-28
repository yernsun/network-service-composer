interface ProjectRuntimeChoice { id: string; name: string; canWrite: boolean; enabled?: boolean }
const eligible = (runtimes: ProjectRuntimeChoice[]) => runtimes.filter(runtime => runtime.enabled !== false && runtime.canWrite)
export function demoProjectRuntime(runtimes: ProjectRuntimeChoice[], selected = ''): string {
  const options = eligible(runtimes)
  return options.find(runtime => runtime.id === selected)?.id || options[0]?.id || ''
}
