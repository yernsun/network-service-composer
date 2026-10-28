import { view as __nscVueView, add as __nscVueAdd, join as __nscVueJoin, interpolateView as __nscVueInterpolate } from '../../components/view';
export function navEntry_34(__viewContext, page, label, icon, count = '') {
  return __nscVueView(__viewContext.templates[179], [__viewContext.ui.page === page || page === 'domains' && __viewContext.ui.page === 'domain' ? 'active' : '', page, __viewContext.I(icon), label, count !== '' ? __nscVueView(__viewContext.templates[178], [count]) : '']);
}
export function projectSidebar_35(__viewContext) {
  return __nscVueView(__viewContext.templates[183], [__viewContext.ui.menu ? 'open' : '', __viewContext.I('back', 'sm'), __viewContext.db.projects.length, __viewContext.I('folder'), __viewContext.allProjectView() ? 'selected' : '', __nscVueJoin(__viewContext.db.projects.map(p => __nscVueView(__viewContext.templates[180], [p.id, !__viewContext.allProjectView() && p.id === __viewContext.db.project ? 'selected' : '', __viewContext.esc(p.name)])), ''), __viewContext.I('chevron', 'sm'), __viewContext.esc(__viewContext.P()?.key || ''), __viewContext.navEntry('project', '项目总览', 'grid'), __viewContext.navEntry('environments', '环境与域名', 'layers', __viewContext.projectEnvs().length), __viewContext.E()?.kind === 'production' ? '' : 'gray', __viewContext.allEnvironmentView() ? 'selected' : '', __nscVueJoin((__viewContext.allProjectView() ? [] : __viewContext.projectEnvs()).map(e => __nscVueView(__viewContext.templates[181], [e.id, !__viewContext.allEnvironmentView() && e.id === __viewContext.db.env ? 'selected' : '', __viewContext.esc(e.name)])), ''), __viewContext.I('chevron', 'sm'), __viewContext.projectEnvs().length === 1 ? __nscVueView(__viewContext.templates[182], []) : '', __viewContext.navEntry('overview', '环境总览', 'activity'), __viewContext.navEntry('domains', '域名', 'globe', __viewContext.listDomains().length), __viewContext.navEntry('routes', '路由规则', 'route'), __viewContext.navEntry('services', '服务', 'box'), __viewContext.navEntry('configs', 'Nginx 配置', 'code'), __viewContext.navEntry('certificates', 'TLS / 证书', 'shield'), __viewContext.navEntry('deployments', '发布记录', 'send'), __viewContext.navEntry('gateways', '可用网关', 'server'), __viewContext.I('play', 'sm'), __viewContext.badge('1.1'), __viewContext.TB('演示场景', 'scenarios', 'right'), __viewContext.I('help')]);
}
export function contextBar_36(__viewContext) {
  if (['projects', 'project', 'environments'].includes(__viewContext.ui.page)) return '';
  if (__viewContext.allProjectView()) return __viewContext.globalScopeContext();
  return __nscVueView(__viewContext.templates[185], [__viewContext.I('folder'), __viewContext.esc(__viewContext.P()?.name), __viewContext.I('right', 'sm'), __viewContext.allEnvironmentView() ? 'selected' : '', __viewContext.I('layers', 'sm'), __nscVueJoin(__viewContext.projectEnvs().map(e => __nscVueView(__viewContext.templates[184], [e.id, !__viewContext.allEnvironmentView() && e.id === __viewContext.db.env ? 'selected' : '', e.kind === 'production' ? '' : 'gray', __viewContext.esc(e.name)])), ''), __viewContext.db.env, __viewContext.I('settings', 'sm'), __viewContext.I('plus', 'sm')]);
}
export function breadcrumb_37(__viewContext, items) {
  return __nscVueView(__viewContext.templates[189], [__nscVueJoin(items.map((it, i) => __nscVueInterpolate(["", "", ""], [i ? __nscVueView(__viewContext.templates[186], []) : '', it.action ? __nscVueView(__viewContext.templates[187], [it.action, it.attrs || '', __viewContext.esc(it.label)]) : __nscVueView(__viewContext.templates[188], [i === items.length - 1 ? 'current' : '', __viewContext.esc(it.label)])])), '')]);
}
export function projectCrumb_38(__viewContext, last = '') {
  return __viewContext.breadcrumb([{
    label: '项目',
    action: 'nav',
    attrs: 'data-page="projects"'
  }, {
    label: __viewContext.P().name,
    action: 'nav',
    attrs: 'data-page="project"'
  }, ...(last ? [{
    label: last
  }] : [])]);
}
export function contextCrumb_39(__viewContext, last = '') {
  if (__viewContext.allProjectView()) return __viewContext.breadcrumb([{
    label: '全部项目',
    action: 'nav',
    attrs: 'data-page="projects"'
  }, {
    label: '所有环境'
  }, {
    label: last
  }]);
  return __viewContext.breadcrumb([{
    label: __viewContext.P().name,
    action: 'nav',
    attrs: 'data-page="project"'
  }, {
    label: __viewContext.allEnvironmentView() ? '所有环境' : __viewContext.E().name,
    action: 'nav',
    attrs: __viewContext.allEnvironmentView() ? 'data-page="applications"' : 'data-page="overview"'
  }, {
    label: last
  }]);
}
export function envCard_40(__viewContext, e) {
  const ds = __viewContext.scopeDomains(e.id, e.project),
    svc = __viewContext.scopeServices(e.id, e.project),
    n = ds.filter(d => d.draft).length;
  return __nscVueView(__viewContext.templates[193], [e.kind === 'production' ? 'production' : '', e.kind, __viewContext.I(e.kind === 'production' ? 'shield' : e.kind === 'sandbox' ? 'box' : 'layers'), __viewContext.esc(e.name), __viewContext.esc(e.slug), __viewContext.badge(__viewContext.KIND_NAMES[e.kind], e.kind === 'production' ? 'green' : 'blue'), ds.length ? __nscVueJoin(ds.slice(0, 3).map(d => __nscVueView(__viewContext.templates[190], [d.id, __viewContext.I('globe', 'sm'), __viewContext.esc(d.name), __viewContext.I('right', 'sm')])), '') : __nscVueView(__viewContext.templates[191], []), ds.length > 3 ? __nscVueView(__viewContext.templates[192], [ds.length - 3]) : '', ds.length, svc.length, n, __viewContext.I('server', 'sm'), e.gateways.length ? __nscVueJoin(e.gateways.map(g => __viewContext.esc(__viewContext.G(g)?.name)), ' / ') : '尚未选择发布网关', e.id, __viewContext.I('settings', 'sm'), __viewContext.B('进入环境', 'p-env', 'right', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [e.id]))]);
}
export function projectPage_41(__viewContext) {
  const p = __viewContext.P(),
    es = __viewContext.projectEnvs(),
    ds = __viewContext.db.domains.filter(d => d.project === p.id),
    svc = __viewContext.db.services.filter(s => s.project === p.id);
  return __nscVueView(__viewContext.templates[195], [__viewContext.projectCrumb(), __viewContext.I('folder'), __viewContext.esc(p.name), __viewContext.badge(p.key), __viewContext.esc(p.description || '在项目中组织环境，以及各环境的域名、服务与路由。'), __viewContext.B('复制配置到其他环境', 'p-copy', 'copy', '', es.length < 2 ? 'disabled title="先添加另一个环境"' : ''), __viewContext.B('添加环境', 'p-env-add', 'plus', 'primary'), __viewContext.metric(es.length, '环境', 'layers', es.length === 1 ? '单环境即可使用' : '配置、草稿与发布分别保存'), __viewContext.metric(ds.length, '域名入口', 'globe', '由你指定所属环境'), __viewContext.metric(svc.length, '环境服务', 'box', '同名服务可以部署到不同环境'), __viewContext.metric(ds.filter(d => d.draft).length, '待发布草稿', 'edit', '跨环境切换不会丢失'), es.length === 1 ? '目前只有生产环境。不需要为了使用产品而创建其他环境。' : '每个环境拥有自己的配置与发布记录。域名可以使用不同根域名，不按名称自动归类。', __viewContext.TB('查看全部绑定', 'nav', 'right', 'data-page="environments"'), __nscVueJoin(es.map(__viewContext.envCard), ''), es.length === 1 ? __nscVueView(__viewContext.templates[194], [__viewContext.I('plus')]) : '', __viewContext.cardHead('资源归属', '先选择项目，再选择环境，最后操作具体资源。', '', 'folder'), __viewContext.I('folder'), __viewContext.esc(p.name), __viewContext.I('layers'), __viewContext.I('globe'), __viewContext.cardHead('常用操作', '选择目标后再修改，不将配置变化自动推广到其他环境。', '', 'bolt'), __viewContext.I('globe'), __viewContext.I('right', 'sm'), __viewContext.I('copy'), __viewContext.I('right', 'sm')]);
}
export function projectsPage_42(__viewContext) {
  return __nscVueView(__viewContext.templates[197], [__viewContext.B('查看全部项目资源', 'scope-all-projects', 'layers'), __viewContext.B('创建项目', 'p-project-add', 'plus', 'primary'), __nscVueJoin(__viewContext.db.projects.map(p => {
    const es = __viewContext.projectEnvs(p.id),
      ds = __viewContext.db.domains.filter(d => d.project === p.id);
    return __nscVueView(__viewContext.templates[196], [__viewContext.I('folder'), __viewContext.esc(p.name), __viewContext.esc(p.key), __viewContext.badge(es.length === 1 ? '单环境项目' : '多环境项目', es.length === 1 ? '' : 'green'), __viewContext.esc(p.description || '尚未添加描述'), __nscVueJoin(es.map(e => __viewContext.badge(e.name, e.kind === 'production' ? 'green' : '')), ''), es.length, ds.length, __viewContext.B('进入项目', 'p-project', 'right', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [p.id]))]);
  }), ''), __viewContext.I('info', 'sm')]);
}
export function environmentsPage_43(__viewContext) {
  return __nscVueView(__viewContext.templates[201], [__viewContext.projectCrumb('环境与域名'), __viewContext.B('添加环境', 'p-env-add', 'plus', 'primary'), __nscVueJoin(__viewContext.projectEnvs().map(__viewContext.envCard), ''), __viewContext.cardHead('域名绑定表', '显示项目内全部环境，不在这个表中直接移动已发布域名。', __viewContext.B('绑定域名', 'p-env-domain-add', 'plus', 'sm'), 'globe'), __nscVueJoin(__viewContext.db.domains.filter(d => d.project === __viewContext.db.project).map(d => __nscVueView(__viewContext.templates[199], [__viewContext.esc(d.name), __viewContext.badge(__viewContext.E(d.env).name, __viewContext.E(d.env).kind === 'production' ? 'green' : 'blue'), __nscVueJoin(d.gateways.map(id => __viewContext.esc(__viewContext.G(id).name)), __nscVueView(__viewContext.templates[198], [])), (d.draft?.config || d.active).mode === 'native' ? '直接编辑文件' : '表单 + 自定义指令', __viewContext.domainStatus(d), __viewContext.TB('打开', 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\""], [d.id]))])), '') || __nscVueView(__viewContext.templates[200], [])]);
}
export function environmentOverview_44(__viewContext) {
  const es = __viewContext.E(),
    ds = __viewContext.scopeDomains(),
    svc = __viewContext.scopeServices();
  return __nscVueView(__viewContext.templates[203], [__viewContext.contextCrumb('环境总览'), __viewContext.esc(es.name), __viewContext.badge(__viewContext.KIND_NAMES[es.kind], es.kind === 'production' ? 'green' : 'blue'), __viewContext.esc(__viewContext.P().name), __viewContext.esc(es.slug), __viewContext.B('环境设置', 'p-env-settings', 'settings', '', __nscVueInterpolate(["data-id=\"", "\""], [es.id])), __viewContext.B('绑定域名', 'domain-add', 'plus', 'primary'), __viewContext.metric(ds.length, '域名', 'globe'), __viewContext.metric(svc.length, '服务', 'box'), __viewContext.metric(ds.reduce((n, d) => __nscVueAdd(n, (d.draft?.config || d.active).routes.length), 0), '路由', 'route'), __viewContext.metric(ds.filter(d => d.draft).length, '待发布草稿', 'edit'), !ds.length ? __nscVueView(__viewContext.templates[202], [__viewContext.I('layers'), es.id, __nscVueAdd(es.gateways.length, ' 个已选择'), __nscVueAdd(svc.length, ' 个服务')]) : __viewContext.domainsPage(ds), __viewContext.I('info', 'sm'), es.gateways.length ? __nscVueJoin(es.gateways.map(id => __viewContext.esc(__viewContext.G(id)?.name)), ' / ') : '未选择']);
}
