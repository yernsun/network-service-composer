import { view as __nscVueView, add as __nscVueAdd, join as __nscVueJoin, interpolateView as __nscVueInterpolate } from '../../components/view';
export function navigation_3(__viewContext) {
  const nav = [['overview', 'grid', '概览'], ['domains', 'globe', 'Domains'], ['routes', 'route', 'Routes'], ['services', 'box', 'Services'], ['gateways', 'server', 'Gateways'], ['certificates', 'shield', '证书'], ['configs', 'code', '配置工作台'], ['deployments', 'send', '变更与发布'], ['events', 'activity', '活动记录']];
  return __nscVueJoin(nav.map(([id, icon, text], i) => __nscVueView(__viewContext.templates[60], [i === 6 ? __nscVueView(__viewContext.templates[57], []) : '', __viewContext.ui.page === id || id === 'domains' && __viewContext.ui.page === 'domain' ? 'active' : '', id, __viewContext.I(icon), text, id === 'domains' ? __nscVueView(__viewContext.templates[58], [__viewContext.db.domains.filter(d => d.env === __viewContext.db.env && d.project === __viewContext.db.project).length]) : id === 'deployments' && __viewContext.db.deployments.some(d => d.status === 'partial') ? __nscVueView(__viewContext.templates[59], []) : ''])), '');
}
export function historyPage_20(__viewContext) {
  return __nscVueView(__viewContext.templates[112], [__viewContext.cardHead('不可变的配置修订', '恢复旧版本会创建新草稿，绝不改写过去的发布记录。', __viewContext.B('导出工作区', 'export-state', 'download', 'sm'), 'history'), __nscVueJoin([...__viewContext.D().revisions].sort((a, b) => b.v - a.v).map(r => __nscVueView(__viewContext.templates[111], [r.v, r.v === __viewContext.D().rev ? __viewContext.badge('期望版本', 'green') : '', r.config.mode === 'native' ? '文件编辑' : __nscVueAdd(r.config.routes.length, ' 条 Route'), r.time, __viewContext.esc(r.note), __viewContext.B('查看配置', 'revision-view', 'code', 'sm', __nscVueInterpolate(["data-version=\"", "\""], [r.v])), __viewContext.B('恢复为草稿', 'restore-revision', 'history', 'sm', __nscVueInterpolate(["data-version=\"", "\" ", ""], [r.v, r.v === __viewContext.D().rev ? 'disabled' : '']))])), ''), __viewContext.cardHead('操作事件', '编辑与发布记录保留在此浏览器。', '', 'activity'), __viewContext.timeline(__viewContext.db.events.filter(e => e.domain === __viewContext.ui.domain).slice(0, 12))]);
}
export function globalPage_21(__viewContext) {
  const name = __viewContext.PAGE_NAMES[__viewContext.ui.page] || 'Domains';
  const ds = __viewContext.listDomains();
  const subtitle = {
    overview: '连接运行环境、组合入口资源、核实每一次变更。',
    domains: '以域名组织入口、路由、证书与 Nginx 配置。',
    routes: '跨 Domain 查看请求到逻辑服务的映射。',
    services: '查看服务注册与 Ready Endpoint，不接管外部应用生命周期。',
    gateways: '从目标配置到逐节点生效；保留 Last Known Good。',
    certificates: '按 Domain 关联证书，分别观察资产与节点加载状态。',
    configs: '按归属范围进入编辑，不把生成文件当成第二份配置源。',
    deployments: '固定 Revision、固定目标与逐节点结果，形成可恢复的记录。',
    events: '追踪本机演示中的编辑、校验、发布和恢复。'
  }[__viewContext.ui.page];
  return __nscVueView(__viewContext.templates[113], [__viewContext.ENV[__viewContext.db.env], name, name, subtitle, __viewContext.ui.page === 'domains' ? __viewContext.B('添加 Domain', 'domain-add', 'plus', 'primary') : __viewContext.ui.page === 'overview' ? __viewContext.B('开始演示', 'open-example', 'play', 'primary') : __viewContext.B('演示场景', 'scenarios', 'settings'), __viewContext.ui.page === 'domains' ? __viewContext.domainsPage(ds) : __viewContext.ui.page === 'overview' ? __viewContext.workspaceOverview(ds) : __viewContext.ui.page === 'routes' ? __viewContext.allRoutes(ds) : __viewContext.ui.page === 'services' ? __viewContext.servicesPage() : __viewContext.ui.page === 'gateways' ? __viewContext.gatewaysPage() : __viewContext.ui.page === 'certificates' ? __viewContext.certificatesPage(ds) : __viewContext.ui.page === 'configs' ? __viewContext.configsPage(ds) : __viewContext.ui.page === 'deployments' ? __viewContext.deploymentsPage(ds) : __viewContext.eventsPage(ds)]);
}
export function paletteResults_33(__viewContext, q) {
  q = q.toLowerCase();
  const domains = __viewContext.db.domains.filter(d => d.name.includes(q)).map(d => __nscVueView(__viewContext.templates[173], [d.id, __viewContext.I('globe'), __viewContext.esc(d.name), __viewContext.ENV[d.env], __viewContext.I('right', 'sm')]));
  const services = __viewContext.db.services.filter(s => s.id.includes(q)).map(s => __nscVueView(__viewContext.templates[174], [s.id, __viewContext.I('box'), __viewContext.esc(s.id), __viewContext.ENV[s.env]]));
  const commands = [['配置工作台', 'palette-config', 'code'], ['运维视角', 'go-ops', 'server'], ['演示场景', 'scenarios', 'play'], ['使用指南', 'help', 'help']].filter(([t]) => !q || t.includes(q)).map(([t, a, ic]) => __nscVueView(__viewContext.templates[175], [a, __viewContext.I(ic), t]));
  return __nscVueJoin([...domains, ...services, ...commands].slice(0, 14), '') || __nscVueView(__viewContext.templates[176], []);
}
export function historyPage_65(__viewContext) {
  const os = __viewContext.db.appDeployments.filter(__viewContext.inListScope);
  return __nscVueInterpolate(["", "", "", "", "", ""], [__viewContext.xCrumb('部署与变更'), __viewContext.xHead('部署与变更', '应用部署和 Nginx 入口发布各自记录；不将跨系统操作伪装成一个原子事务。', __viewContext.B('部署应用', 'x-install', 'plus', 'primary'), 'CHANGE HISTORY'), __viewContext.tabs([['application', '应用部署与操作', 'box'], ['gateway', 'Nginx 入口发布', 'code']], __viewContext.x.historyTab, 'x-history-tab'), __viewContext.x.historyTab === 'application' ? __viewContext.deployTable(os) : __viewContext.deploymentsPage(__viewContext.listDomains()), __viewContext.xFooter()]);
}
export function topbar_79(__viewContext) {
  return __nscVueView(__viewContext.templates[452], [__viewContext.I('menu'), __viewContext.I('search', 'sm'), __viewContext.allProjectView() ? '搜索全部项目资源…' : '在当前项目中搜索…', __viewContext.I('help'), __viewContext.I('bell')]);
}
