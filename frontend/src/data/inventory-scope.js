// Directory rows are shared by the sidebar, aggregate lists and deep links.
// Expensive details follow explicit environment membership and the active view.
export function inventoryScope(path, { projects, environments, applications, services, domains, routes, runtimes, operations, environmentFor }, options = {}) {
  const { action } = options
  let projectID = options.projectID, environmentID = options.environmentID
  const url = new URL(path, 'https://nsc.invalid')
  let parts
  try { parts = url.pathname.split('/').filter(Boolean).map(decodeURIComponent) }
  catch { parts = [] }
  let page = parts[0] || 'projects'
  let applicationID, serviceID, runtimeID
  if (parts[0] === 'projects') {
    projectID = parts[1] === 'all' ? undefined : parts[1]
    environmentID = parts[2] === 'environments' && parts[3] !== 'all' ? parts[3] : undefined
    page = parts[4] || parts[2] || (parts[1] ? 'project' : 'projects')
    if (page === 'applications' && parts[5]) { applicationID = parts[5]; page = parts[6] === 'compose' ? 'compose' : 'application' }
    if (page === 'services' && parts[5]) { serviceID = parts[5]; page = 'service' }
    if (page === 'services' && url.searchParams.get('scope') === 'all') { projectID = undefined; environmentID = undefined }
    if (page === 'services' && url.searchParams.get('scope') === 'project') environmentID = undefined
  } else if (parts[0] === 'services') {
    page = 'service'; serviceID = parts[1]
    const env = environmentFor(services.find(service => service.id === serviceID) || {})
    projectID = env?.project_id; environmentID = env?.id
  } else if (parts[0] === 'operations' && parts[1] === 'activity') {
    page = 'activity'
  } else if (parts[0] === 'operations') {
    const operation = operations.find(operation => operation.id === parts[1])
    const env = environmentFor(operation || {})
    projectID = env?.project_id; environmentID = env?.id; applicationID = operation?.application_id
  } else if (parts[0] === 'infrastructure') {
    page = parts[1]; runtimeID = parts[2]
  }
  if (action === 'install') {
    // The wizard can select a different concrete environment before advancing.
    projectID = options.projectID || projectID
    environmentID = options.environmentID || environmentID
  }
  const selectedEnvironments = serviceID && !environmentID ? [] : environments.filter(env => (!projectID || env.project_id === projectID) && (!environmentID || env.id === environmentID))
  const selectedIDs = new Set(selectedEnvironments.map(env => env.id))
  const belongs = row => selectedIDs.has(environmentFor(row)?.id)
  const globalScope = parts[0] === 'projects' && (parts[1] === 'all' || page === 'services' && url.searchParams.get('scope') === 'all')
  const resourceBelongs = row => parts[0] !== 'projects' || globalScope || belongs(row)
  const selectedServices = serviceID ? services.filter(service => service.id === serviceID && resourceBelongs(service)) : applicationID ? services.filter(service => service.application_id === applicationID && resourceBelongs(service)) : globalScope ? services : services.filter(belongs)
  const selectedApplications = applicationID ? applications.filter(app => app.id === applicationID && resourceBelongs(app)) : serviceID ? applications.filter(app => selectedServices.some(service => service.application_id === app.id)) : globalScope ? applications : applications.filter(belongs)
  const workload = ['applications', 'application', 'services', 'service', 'compose', 'operations', 'workloadHistory', 'deployments'].includes(page)
  const domainDetails = ['project', 'environments', 'overview', 'domains', 'routes', 'configs', 'gateways', 'compose', 'service', 'application'].includes(page)
  const selectedServiceIDs = new Set(selectedServices.map(service => service.id))
  const selectedDomainIDs = new Set(routes.filter(route => selectedServiceIDs.has(route.backend?.service_id)).map(route => route.domain_id))
  const selectedDomains = globalScope ? domains : domains.filter(domain => selectedDomainIDs.has(domain.id))
  const runtimeIDs = action === 'install'
    ? [...new Set(selectedEnvironments.map(env => env.runtime_id))].filter(Boolean)
    : page === 'runtimes'
    ? runtimes.filter(runtime => !runtimeID || runtime.id === runtimeID).map(runtime => runtime.id)
    : workload ? [...new Set([...(!serviceID && !applicationID ? selectedEnvironments.map(env => env.runtime_id) : []), ...selectedServices.map(service => service.runtime_id), ...selectedApplications.map(app => app.runtime_id)])].filter(Boolean) : []
  const variableEnvironments = page === 'compose'
    ? environments.filter(env => env.project_id === projectID && (env.id === environmentID || env.id === url.searchParams.get('previewEnv')))
    : selectedEnvironments
  const variables = !!projectID && (page === 'variables' || page === 'compose' || action === 'install')
  const variableProjects = variables ? projects.filter(project => project.id === projectID || variableEnvironments.some(env => env.project_id === project.id)) : []
  const projectVariablesOnly = parts[0] === 'projects' && parts[2] === 'variables'
  return {
    applications: workload ? selectedApplications : [],
    documents: ['applications', 'application', 'services', 'service', 'compose'].includes(page) ? selectedApplications.filter(app => app.managed && !app.system) : [],
    draftEnvironments: page === 'applications' || action === 'install' ? selectedEnvironments : [],
    domainEnvironments: domainDetails ? selectedEnvironments : [],
    pools: domainDetails ? [...new Set([...selectedDomains.map(domain => domain.pool_id), ...selectedEnvironments.map(env => runtimes.find(runtime => runtime.id === env.runtime_id)?.pool_id)])].filter(Boolean) : [],
    endpointServices: domainDetails ? selectedServices : [],
    variableOwners: [
      ...variableProjects.map(project => ({ layer: 'project', id: project.id, path: '/projects/{id}/variables' })),
      ...(variables && !projectVariablesOnly ? variableEnvironments.map(env => ({ layer: 'environment', id: env.id, path: '/environments/{id}/variables' })) : []),
    ],
    runtimeIDs,
    catalog: page === 'market' || action === 'install',
  }
}
