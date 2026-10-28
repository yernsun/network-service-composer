import { translate as _t } from '../../i18n';
import { serviceName as _serviceName, serviceSearch as _serviceSearch } from '../../data/service-references';
import { view as __nscVueView, add as __nscVueAdd, join as __nscVueJoin, interpolateView as __nscVueInterpolate } from '../../components/view';
export function navigation_3(__viewContext) {
  const nav = [['overview', 'grid', _t("cluster.overview")], ['domains', 'globe', 'Domains'], ['routes', 'route', 'Routes'], ['services', 'box', 'Services'], ['gateways', 'server', 'Gateways'], ['certificates', 'shield', _t("archive.message_69eb41708bcb")], ['configs', 'code', _t("archive.message_845e79578dbc")], ['deployments', 'send', _t("archive.message_e2901f5a3da5")], ['events', 'activity', _t("archive.message_f98dfe0b4d54")]];
  return __nscVueJoin(nav.map(([id, icon, text], i) => __nscVueView(__viewContext.templates[60], [i === 6 ? __nscVueView(__viewContext.templates[57], []) : '', __viewContext.ui.page === id || id === 'domains' && __viewContext.ui.page === 'domain' ? 'active' : '', id, __viewContext.I(icon), text, id === 'domains' ? __nscVueView(__viewContext.templates[58], [__viewContext.db.domains.filter((d) => d.env === __viewContext.db.env && d.project === __viewContext.db.project).length]) : id === 'deployments' && __viewContext.db.deployments.some((d) => d.status === 'partial') ? __nscVueView(__viewContext.templates[59], []) : ''])), '');
}
export function historyPage_20(__viewContext) {
  return __nscVueView(__viewContext.templates[112], [__viewContext.cardHead(_t("archive.message_3d083c8dd3f8"), _t("archive.message_9938d0f2f34a"), __viewContext.B(_t("archive.message_1e6e3b5a0f73"), 'export-state', 'download', 'sm'), 'history'), __nscVueJoin([...__viewContext.D().revisions].sort((a, b) => b.v - a.v).map((r) => __nscVueView(__viewContext.templates[111], [r.v, r.v === __viewContext.D().rev ? __viewContext.badge(_t("archive.message_3d5fc9065141"), 'green') : '', r.config.mode === 'native' ? _t("archive.message_b85a16d528cd") : __nscVueAdd(r.config.routes.length, _t("archive.message_94116c2e3919")), r.time, __viewContext.esc(r.note), __viewContext.B(_t("archive.message_3b8ae0dc32ad"), 'revision-view', 'code', 'sm', __nscVueInterpolate(["data-version=\"", "\""], [r.v])), __viewContext.B(_t("delivery.restore"), 'restore-revision', 'history', 'sm', __nscVueInterpolate(["data-version=\"", "\" ", ""], [r.v, r.v === __viewContext.D().rev ? 'disabled' : '']))])), ''), __viewContext.cardHead(_t("archive.message_bd0428eed3d2"), _t("archive.message_c75331610c76"), '', 'activity'), __viewContext.timeline(__viewContext.db.events.filter((e) => e.domain === __viewContext.ui.domain).slice(0, 12))]);
}
export function globalPage_21(__viewContext) {
  const name = __viewContext.PAGE_NAMES[__viewContext.ui.page] || 'Domains';
  const ds = __viewContext.listDomains();
  const subtitle = {
    overview: _t("archive.message_79854c87e08f"),
    domains: _t("archive.message_dd040c6c3e30"),
    routes: _t("archive.message_32a6af8f1441"),
    services: _t("archive.message_17545a5bce42"),
    gateways: _t("archive.message_b6fc96fea65c"),
    certificates: _t("archive.message_d37da2b961b1"),
    configs: _t("archive.message_650d31b7e6f5"),
    deployments: _t("archive.message_70ba91c2eab6"),
    events: _t("archive.message_c3c0e308b1d4")
  }[__viewContext.ui.page];
  return __nscVueView(__viewContext.templates[113], [__viewContext.ENV[__viewContext.db.env], name, name, subtitle, __viewContext.ui.page === 'domains' ? __viewContext.B(_t("archive.message_d28c9d6f5ce9"), 'domain-add', 'plus', 'primary') : __viewContext.ui.page === 'overview' ? __viewContext.B(_t("archive.message_7a55763a9390"), 'open-example', 'play', 'primary') : __viewContext.B(_t("archive.message_546805b0c669"), 'scenarios', 'settings'), __viewContext.ui.page === 'domains' ? __viewContext.domainsPage(ds) : __viewContext.ui.page === 'overview' ? __viewContext.workspaceOverview(ds) : __viewContext.ui.page === 'routes' ? __viewContext.allRoutes(ds) : __viewContext.ui.page === 'services' ? __viewContext.servicesPage() : __viewContext.ui.page === 'gateways' ? __viewContext.gatewaysPage() : __viewContext.ui.page === 'certificates' ? __viewContext.certificatesPage(ds) : __viewContext.ui.page === 'configs' ? __viewContext.configsPage(ds) : __viewContext.ui.page === 'deployments' ? __viewContext.deploymentsPage(ds) : __viewContext.eventsPage(ds)]);
}
export function paletteResults_33(__viewContext, q) {
  q = q.toLowerCase();
  const domains = __viewContext.db.domains.filter((d) => d.name.includes(q)).map((d) => __nscVueView(__viewContext.templates[173], [d.id, __viewContext.I('globe'), __viewContext.esc(d.name), __viewContext.ENV[d.env], __viewContext.I('right', 'sm')]));
  const services = __viewContext.db.services.filter((s) => s.status !== 'REMOVED' && _serviceSearch(__viewContext.db.services, s.id).includes(q)).map((s) => __nscVueView(__viewContext.templates[174], [s.id, __viewContext.I('box'), __viewContext.esc(_serviceName(__viewContext.db.services, s.id)), __viewContext.ENV[s.env]]));
  const commands = [[_t("archive.message_845e79578dbc"), 'palette-config', 'code'], [_t("prototype.operations_view"), 'go-ops', 'server'], [_t("archive.message_546805b0c669"), 'scenarios', 'play'], [_t("archive.message_b6e77060db53"), 'help', 'help']].filter(([t]) => !q || t.includes(q)).map(([t, a, ic]) => __nscVueView(__viewContext.templates[175], [a, __viewContext.I(ic), t]));
  return __nscVueJoin([...domains, ...services, ...commands].slice(0, 14), '') || __nscVueView(__viewContext.templates[176], []);
}
export function historyPage_65(__viewContext) {
  const os = __viewContext.db.appDeployments.filter(__viewContext.inListScope);
  return __nscVueInterpolate(["", "", "", "", "", ""], [__viewContext.xCrumb(_t("prototype.deployments_changes")), __viewContext.xHead(_t("prototype.deployments_changes"), _t("archive.message_cfe9739e7176"), __viewContext.B(_t("archive.message_3a4a165a145b"), 'x-install', 'plus', 'primary'), 'CHANGE HISTORY'), __viewContext.tabs([['application', _t("archive.message_32c6041eabd8"), 'box'], ['gateway', _t("archive.message_dd302f2178a3"), 'code']], __viewContext.x.historyTab, 'x-history-tab'), __viewContext.x.historyTab === 'application' ? __viewContext.deployTable(os) : __viewContext.deploymentsPage(__viewContext.listDomains()), __viewContext.xFooter()]);
}
export function topbar_79(__viewContext) {
  return __nscVueView(__viewContext.templates[452], [__viewContext.I('menu'), __viewContext.I('search', 'sm'), __viewContext.allProjectView() ? _t("archive.message_94114d55e162") : _t("prototype.search_project"), __viewContext.I('help'), __viewContext.I('bell')]);
}
