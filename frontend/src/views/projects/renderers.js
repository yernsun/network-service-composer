import { translate as _t, translatedAttribute as _a } from '../../i18n';
import { view as __nscVueView, add as __nscVueAdd, join as __nscVueJoin, interpolateView as __nscVueInterpolate } from '../../components/view';
export function navEntry_34(__viewContext, page, label, icon, count = '') {
  return __nscVueView(__viewContext.templates[179], [__viewContext.ui.page === page || page === 'domains' && __viewContext.ui.page === 'domain' ? 'active' : '', page, __viewContext.I(icon), label, count !== '' ? __nscVueView(__viewContext.templates[178], [count]) : '']);
}
export function projectSidebar_35(__viewContext) {
  return __nscVueView(__viewContext.templates[183], [__viewContext.ui.menu ? 'open' : '', __viewContext.I('back', 'sm'), __viewContext.db.projects.length, __viewContext.I('folder'), __viewContext.allProjectView() ? 'selected' : '', __nscVueJoin(__viewContext.db.projects.map((p) => __nscVueView(__viewContext.templates[180], [p.id, !__viewContext.allProjectView() && p.id === __viewContext.db.project ? 'selected' : '', __viewContext.esc(p.name)])), ''), __viewContext.I('chevron', 'sm'), __viewContext.esc(__viewContext.P()?.key || ''), __viewContext.navEntry('project', _t("prototype.project_overview"), 'grid'), __viewContext.navEntry('environments', _t("prototype.environments_domains"), 'layers', __viewContext.projectEnvs().length), __viewContext.E()?.kind === 'production' ? '' : 'gray', __viewContext.allEnvironmentView() ? 'selected' : '', __nscVueJoin((__viewContext.allProjectView() ? [] : __viewContext.projectEnvs()).map((e) => __nscVueView(__viewContext.templates[181], [e.id, !__viewContext.allEnvironmentView() && e.id === __viewContext.db.env ? 'selected' : '', __viewContext.esc(e.name)])), ''), __viewContext.I('chevron', 'sm'), __viewContext.projectEnvs().length === 1 ? __nscVueView(__viewContext.templates[182], []) : '', __viewContext.navEntry('overview', _t("prototype.environment_overview"), 'activity'), __viewContext.navEntry('domains', _t("bindingUI.host"), 'globe', __viewContext.listDomains().length), __viewContext.navEntry('routes', _t("prototype.routing_rules"), 'route'), __viewContext.navEntry('services', _t("compose.services"), 'box'), __viewContext.navEntry('configs', _t("archive.message_3f90761c6e4a"), 'code'), __viewContext.navEntry('certificates', _t("prototype.tls_certificates"), 'shield'), __viewContext.navEntry('deployments', _t("archive.message_7290d89a6d74"), 'send'), __viewContext.navEntry('gateways', _t("prototype.available_gateways"), 'server'), __viewContext.I('play', 'sm'), __viewContext.badge('1.1'), __viewContext.TB(_t("archive.message_546805b0c669"), 'scenarios', 'right'), __viewContext.I('help')]);
}
export function contextBar_36(__viewContext) {
  if (['projects', 'project', 'environments'].includes(__viewContext.ui.page)) return '';
  if (__viewContext.allProjectView()) return __viewContext.globalScopeContext();
  return __nscVueView(__viewContext.templates[185], [__viewContext.I('folder'), __viewContext.esc(__viewContext.P()?.name), __viewContext.I('right', 'sm'), __viewContext.allEnvironmentView() ? 'selected' : '', __viewContext.I('layers', 'sm'), __nscVueJoin(__viewContext.projectEnvs().map((e) => __nscVueView(__viewContext.templates[184], [e.id, !__viewContext.allEnvironmentView() && e.id === __viewContext.db.env ? 'selected' : '', e.kind === 'production' ? '' : 'gray', __viewContext.esc(e.name)])), ''), __viewContext.db.env, __viewContext.I('settings', 'sm'), __viewContext.I('plus', 'sm')]);
}
export function breadcrumb_37(__viewContext, items) {
  return __nscVueView(__viewContext.templates[189], [__nscVueJoin(items.map((it, i) => __nscVueInterpolate(["", "", ""], [i ? __nscVueView(__viewContext.templates[186], []) : '', it.action ? __nscVueView(__viewContext.templates[187], [it.action, it.attrs || '', __viewContext.esc(it.label)]) : __nscVueView(__viewContext.templates[188], [i === items.length - 1 ? 'current' : '', __viewContext.esc(it.label)])])), '')]);
}
export function projectCrumb_38(__viewContext, last = '') {
  return __viewContext.breadcrumb([{
    label: _t("prototype.project_group"),
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
    label: _t("workspace.all_projects"),
    action: 'nav',
    attrs: 'data-page="projects"'
  }, {
    label: _t("workspace.all_environments")
  }, {
    label: last
  }]);
  return __viewContext.breadcrumb([{
    label: __viewContext.P().name,
    action: 'nav',
    attrs: 'data-page="project"'
  }, {
    label: __viewContext.allEnvironmentView() ? _t("workspace.all_environments") : __viewContext.E().name,
    action: 'nav',
    attrs: __viewContext.allEnvironmentView() ? 'data-page="applications"' : 'data-page="overview"'
  }, {
    label: last
  }]);
}
export function envCard_40(__viewContext, e) {
  const ds = __viewContext.scopeDomains(e.id, e.project),
    svc = __viewContext.scopeServices(e.id, e.project),
    n = ds.filter((d) => d.draft).length;
  return __nscVueView(__viewContext.templates[193], [e.kind === 'production' ? 'production' : '', e.kind, __viewContext.I(e.kind === 'production' ? 'shield' : e.kind === 'sandbox' ? 'box' : 'layers'), __viewContext.esc(e.name), __viewContext.esc(e.slug), __viewContext.badge(__viewContext.KIND_NAMES[e.kind], e.kind === 'production' ? 'green' : 'blue'), ds.length ? __nscVueJoin(ds.slice(0, 3).map((d) => __nscVueView(__viewContext.templates[190], [d.id, __viewContext.I('globe', 'sm'), __viewContext.esc(d.name), __viewContext.I('right', 'sm')])), '') : __nscVueView(__viewContext.templates[191], []), ds.length > 3 ? __nscVueView(__viewContext.templates[192], [ds.length - 3]) : '', ds.length, svc.length, n, __viewContext.I('server', 'sm'), e.gateways.length ? __nscVueJoin(e.gateways.map((g) => __viewContext.esc(__viewContext.G(g)?.name)), ' / ') : _t("archive.message_527783f4eb2f"), e.id, __viewContext.I('settings', 'sm'), __viewContext.B(_t("prototype.enter_environment"), 'p-env', 'right', 'sm', __nscVueAdd(__nscVueInterpolate(["data-id=\"", "\""], [e.id]), " data-card-primary"))]);
}
export function projectPage_41(__viewContext) {
  const p = __viewContext.P(),
    es = __viewContext.projectEnvs(),
    ds = __viewContext.db.domains.filter((d) => d.project === p.id),
    svc = __viewContext.db.services.filter((s) => s.status !== 'REMOVED' && s.project === p.id);
  return __nscVueView(__viewContext.templates[195], [__viewContext.projectCrumb(), __viewContext.I('folder'), __viewContext.esc(p.name), __viewContext.badge(p.key), __viewContext.esc(p.description || _t("prototype.project_overview_help")), __viewContext.B(_t("prototype.copy_configuration"), 'p-copy', 'copy', '', es.length < 2 ? _a("disabled title=\"", "archive.message_60045a5b5adf", "\"") : ''), __viewContext.B(_t("archive.message_a2a1229f025d"), 'p-env-add', 'plus', 'primary'), __viewContext.metric(es.length, _t("prototype.environment_group"), 'layers', es.length === 1 ? _t("archive.message_fffea17422ae") : _t("archive.message_80ce47b09b4f"), "data-action=\"nav\" data-page=\"environments\""), __viewContext.metric(ds.length, _t("archive.message_62f345897b3a"), 'globe', _t("archive.message_c5369faf24ef"), "data-action=\"nav\" data-page=\"domains\""), __viewContext.metric(svc.length, _t("archive.message_16356b120797"), 'box', _t("archive.message_428fbf538000"), "data-action=\"nav\" data-page=\"services\""), __viewContext.metric(ds.filter((d) => d.draft).length, _t("prototype.pending_drafts"), 'edit', _t("archive.message_7908539937fa"), "data-action=\"nav\" data-page=\"domains\""), es.length === 1 ? _t("archive.message_3b01372c772f") : _t("archive.message_8c65f07d5ab6"), __viewContext.TB(_t("prototype.view_all_bindings"), 'nav', 'right', 'data-page="environments"'), __nscVueJoin(es.map(__viewContext.envCard), ''), es.length === 1 ? __nscVueView(__viewContext.templates[194], [__viewContext.I('plus')]) : '', __viewContext.cardHead(_t("archive.message_33a3ccb27972"), _t("archive.message_ef683b384378"), '', 'folder'), __viewContext.I('folder'), __viewContext.esc(p.name), __viewContext.I('layers'), __viewContext.I('globe'), __viewContext.cardHead(_t("archive.message_e0782ab38fa3"), _t("archive.message_6d3d4d3458af"), '', 'bolt'), __viewContext.I('globe'), __viewContext.I('right', 'sm'), __viewContext.I('copy'), __viewContext.I('right', 'sm')]);
}
export function projectsPage_42(__viewContext) {
  return __nscVueView(__viewContext.templates[197], [__viewContext.B(_t("workspace.browse_all"), 'scope-all-projects', 'layers'), __viewContext.B(_t("archive.message_43d7d2b51d75"), 'p-project-add', 'plus', 'primary'), __nscVueJoin(__viewContext.db.projects.map((p) => {
    const es = __viewContext.projectEnvs(p.id),
      ds = __viewContext.db.domains.filter((d) => d.project === p.id);
    return __nscVueView(__viewContext.templates[196], [__viewContext.I('folder'), __viewContext.esc(p.name), __viewContext.esc(p.key), __viewContext.badge(es.length === 1 ? _t("archive.message_a3e236748012") : _t("archive.message_c40675cc5749"), es.length === 1 ? '' : 'green'), __viewContext.esc(p.description || _t("archive.message_45115b166f77")), __nscVueJoin(es.map((e) => __viewContext.badge(e.name, e.kind === 'production' ? 'green' : '')), ''), es.length, ds.length, __nscVueAdd(__viewContext.B(_t("prototype.enter_project"), 'p-project', 'right', 'sm', __nscVueAdd(__nscVueInterpolate(["data-id=\"", "\""], [p.id]), " data-card-primary")), __viewContext.B(_t("projectLifecycle.settings"), "p-project-settings", "settings", "sm", __nscVueInterpolate(["data-id=\"", "\""], [p.id])))]);
  }), ''), __viewContext.I('info', 'sm')]);
}
export function environmentsPage_43(__viewContext) {
  return __nscVueView(__viewContext.templates[201], [__viewContext.projectCrumb(_t("prototype.environments_domains")), __viewContext.B(_t("archive.message_a2a1229f025d"), 'p-env-add', 'plus', 'primary'), __nscVueJoin(__viewContext.projectEnvs().map(__viewContext.envCard), ''), __viewContext.cardHead(_t("archive.message_b084ba9c31ec"), _t("archive.message_ad9a760f6b1e"), __viewContext.B(_t("bindingUI.add"), 'p-env-domain-add', 'plus', 'sm'), 'globe'), __nscVueJoin(__viewContext.db.domains.filter((d) => d.project === __viewContext.db.project).map((d) => __nscVueView(__viewContext.templates[199], [__viewContext.esc(d.name), __viewContext.badge(__viewContext.E(d.env).name, __viewContext.E(d.env).kind === 'production' ? 'green' : 'blue'), __nscVueJoin(d.gateways.map((id) => __viewContext.esc(__viewContext.G(id).name)), __nscVueView(__viewContext.templates[198], [])), (d.draft?.config || d.active).mode === 'native' ? _t("archive.message_debe95b98471") : _t("archive.message_3465da773f5a"), __viewContext.domainStatus(d), __viewContext.TB(_t("archive.message_c771248e511f"), 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\""], [d.id]))])), '') || __nscVueView(__viewContext.templates[200], [])]);
}
export function environmentOverview_44(__viewContext) {
  const es = __viewContext.E(),
    ds = __viewContext.scopeDomains(),
    svc = __viewContext.scopeServices();
  return __nscVueView(__viewContext.templates[203], [__viewContext.contextCrumb(_t("prototype.environment_overview")), __viewContext.esc(es.name), __viewContext.badge(__viewContext.KIND_NAMES[es.kind], es.kind === 'production' ? 'green' : 'blue'), __viewContext.esc(__viewContext.P().name), __viewContext.esc(es.slug), __viewContext.B(_t("archive.message_f812dc4f80ae"), 'p-env-settings', 'settings', '', __nscVueInterpolate(["data-id=\"", "\""], [es.id])), __viewContext.B(_t("bindingUI.add"), 'domain-add', 'plus', 'primary'), __viewContext.metric(ds.length, _t("bindingUI.host"), 'globe', "", "data-action=\"nav\" data-page=\"domains\""), __viewContext.metric(svc.length, _t("compose.services"), 'box', "", "data-action=\"nav\" data-page=\"services\""), __viewContext.metric(ds.reduce((n, d) => __nscVueAdd(n, (d.draft?.config || d.active).routes.length), 0), _t("archive.message_b9670c85a4ab"), 'route', "", "data-action=\"nav\" data-page=\"routes\""), __viewContext.metric(ds.filter((d) => d.draft).length, _t("prototype.pending_drafts"), 'edit', "", "data-action=\"nav\" data-page=\"domains\""), !ds.length ? __nscVueView(__viewContext.templates[202], [__viewContext.I('layers'), es.id, __nscVueAdd(es.gateways.length, _t("archive.message_0ebc410c386d")), __nscVueAdd(svc.length, _t("archive.message_97827a9c3766"))]) : __viewContext.domainsPage(ds), __viewContext.I('info', 'sm'), es.gateways.length ? __nscVueJoin(es.gateways.map((id) => __viewContext.esc(__viewContext.G(id)?.name)), ' / ') : _t("archive.message_1182c5454f11")]);
}
