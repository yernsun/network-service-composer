import { translate as _t, translatedAttribute as _a, localizeModal as _modal } from '../../i18n';
import { serviceLifecycle as _serviceLifecycle } from '../../data/service-lifecycle';
import { serviceRevisionLabel as _serviceRevisionLabel, serviceVersionSummary as _serviceVersionSummary } from '../../data/service-version-view';
import { observationUnknown as _observationUnknown, instanceTimeRows as _instanceTimeRows } from '../../data/service-instances';
import { nodeHealth as _nodeHealth, nodeMetric as _nodeMetric } from '../../data/node-health';
import { serviceName as _serviceName } from '../../data/service-references';
import { view as __nscVueView, add as __nscVueAdd, join as __nscVueJoin, interpolateView as __nscVueInterpolate } from '../../components/view';
export function xMetric_45(__viewContext, value, label, desc = '', tone = '', action = "") {
  return __nscVueView(__viewContext.templates[255], [tone, __viewContext.esc(label), __viewContext.esc(value), __viewContext.esc(desc), action]);
}
export function xHead_46(__viewContext, title, sub, actions = '', eyebrow = '') {
  return __nscVueView(__viewContext.templates[257], [eyebrow ? __nscVueView(__viewContext.templates[256], [__viewContext.esc(eyebrow)]) : '', __viewContext.esc(title), __viewContext.esc(sub), actions]);
}
export function xCrumb_47(__viewContext, last, global = false) {
  return global ? __viewContext.breadcrumb([{
    label: _t("archive.message_42bdbb2f93a2")
  }, {
    label: last
  }]) : __viewContext.contextCrumb(last);
}
export function xFooter_48(__viewContext) {
  return __nscVueView(__viewContext.templates[258], []);
}
export function appTable_49(__viewContext, apps) {
  return __nscVueView(__viewContext.templates[265], [__nscVueJoin(apps.map((a) => {
    const st = a.system && a.status === 'UNKNOWN' ? {
      name: _t('community.evidence_state.UNKNOWN'),
      tone: 'amber'
    } : __viewContext.appState(a);
    return __nscVueView(__viewContext.templates[263], [a.id, __viewContext.esc(a.name), __viewContext.MODE[a.mode], __viewContext.badge(__viewContext.envName(a.env), __viewContext.E(a.env)?.kind === 'production' ? 'green' : 'blue'), __viewContext.appServices(a).length, __viewContext.esc(__viewContext.RT(a.runtime)?.name), __viewContext.badge(st.name, st.tone), a.managed ? _t("archive.message_5853e55306ea") : _t("archive.message_175bd630e1df"), __viewContext.TB(_t("archive.message_87d504f157cc"), 'x-app', 'right', __nscVueInterpolate(["data-id=\"", "\""], [a.id]))]);
  }), '') || __nscVueView(__viewContext.templates[264], [])]);
}
export function applicationsPage_50(__viewContext) {
  const as = __viewContext.db.apps.filter(__viewContext.inListScope);
  const ss = __viewContext.db.services.filter((s) => __viewContext.inListScope(s) && s.app && s.status !== 'REMOVED');
  const run = ss.flatMap(__viewContext.currentInstances).filter((t) => t.state === 'RUNNING').length;
  return __nscVueView(__viewContext.templates[269], [__viewContext.xCrumb(_t("archive.message_63c73c4730f4")), __viewContext.xHead(_t("archive.message_63c73c4730f4"), _t("archive.message_a89c4c939f74"), __nscVueAdd(__viewContext.B(_t("archive.message_bf198256f2ac"), 'x-nav', 'grid', '', 'data-page="market"'), __viewContext.B(_t("archive.message_3a4a165a145b"), 'x-install', 'plus', 'primary')), 'APPLICATIONS'), __viewContext.xMetric(as.length, _t("archive.message_63c73c4730f4"), __nscVueInterpolate(["", " / ", ""], [__viewContext.allProjectView() ? _t("workspace.all_projects") : __viewContext.P().name, __viewContext.allEnvironmentView() ? _t("workspace.all_environments") : __viewContext.E().name]), "", "data-action=\"x-nav\" data-page=\"applications\""), __viewContext.xMetric(ss.length, _t("archive.message_79b491ade724"), _t("archive.message_4747c4dd0fdf"), "", "data-action=\"x-nav\" data-page=\"services\""), __viewContext.xMetric(run, _t("cluster.running"), _t("archive.message_7450121a1072"), "", "data-action=\"x-nav\" data-page=\"services\" data-health=\"running\""), __viewContext.xMetric(as.filter((a) => (a.system && a.status === 'UNKNOWN' ? {
    name: _t('community.evidence_state.UNKNOWN'),
    tone: 'amber'
  } : __viewContext.appState(a)).tone === 'amber').length, _t("community.active_issue"), _t("archive.message_f64ca4539852"), "", "data-action=\"x-nav\" data-page=\"services\" data-health=\"issue\""), __viewContext.db.appDrafts.filter(__viewContext.inListScope).length ? __nscVueView(__viewContext.templates[266], [__viewContext.I('edit'), __viewContext.db.appDrafts.filter(__viewContext.inListScope).length, __viewContext.TB(_t("archive.message_ff5cbaf2d08d"), 'x-resume-wizard', 'right')]) : '', __nscVueJoin(as.map((a) => {
    const s = __viewContext.appServices(a),
      st = a.system && a.status === 'UNKNOWN' ? {
        name: _t('community.evidence_state.UNKNOWN'),
        tone: 'amber'
      } : __viewContext.appState(a);
    return __nscVueView(__viewContext.templates[267], [a.mode === 'compose' ? 'blue' : '', __viewContext.I('box', 'lg'), __viewContext.esc(a.name), __viewContext.allEnvironmentView() ? __viewContext.badge(__viewContext.resourceScopeName(a), __viewContext.E(a.env)?.kind === 'production' ? 'green' : 'blue') : '', __viewContext.esc(a.key), a.system ? '—' : a.version, __viewContext.badge(st.name, st.tone), __viewContext.MODE[a.mode], __viewContext.esc(__viewContext.RT(a.runtime)?.name), a.managed ? _t("archive.message_5409a6d1a936") : _t("archive.message_028e66d9d4b9"), __nscVueJoin(s.slice(0, 4).map((s) => __viewContext.badge(_serviceName(__viewContext.db.services, s.id), __viewContext.serviceState(s).key === 'issue' ? 'amber' : '')), ''), s.length, s.flatMap(__viewContext.currentInstances).filter((t) => t.state === 'RUNNING').length, new Set(s.flatMap(__viewContext.refsFor).map((r) => r.d.id)).size, a.managed ? _t("archive.message_54afafe42f30") : _t("archive.message_242d7f7ffc53"), __viewContext.B(_t("archive.message_8924b9035a5d"), 'x-app', 'right', 'sm', __nscVueAdd(__nscVueInterpolate(["data-id=\"", "\""], [a.id]), " data-card-primary"))]);
  }), '') || __nscVueView(__viewContext.templates[268], [__viewContext.I('box'), __viewContext.allEnvironmentView() ? _t("archive.message_05b29782b4a8") : _t("archive.message_b52461f6e013"), __viewContext.B(_t("archive.message_153841e57e59"), 'x-install', 'plus', 'primary')]), __viewContext.xFooter()]);
}
export function marketPage_51(__viewContext) {
  const ts = __viewContext.TEMPLATES.filter((t) => (__viewContext.x.marketCategory === 'all' || t.cat === __viewContext.x.marketCategory) && __nscVueInterpolate(["", " ", ""], [t.name, t.desc]).toLowerCase().includes(__viewContext.x.marketSearch.toLowerCase()));
  return __nscVueView(__viewContext.templates[273], [__viewContext.breadcrumb([{
    label: _t("archive.message_f2e7caa9f119")
  }]), __viewContext.xHead(_t("archive.message_f2e7caa9f119"), _t("archive.message_117975376b1b"), __nscVueAdd(__viewContext.B(_t("archive.message_432866e592b3"), 'x-install-file', 'file'), __viewContext.B(_t("archive.message_0318ec3e3a1e"), 'x-install-image', 'box')), 'APPLICATION CATALOG'), __viewContext.I('layers', 'lg'), __viewContext.badge(_t("archive.message_ceb463b82d43"), 'green'), __viewContext.I('search'), __viewContext.esc(__viewContext.x.marketSearch), __nscVueJoin([['all', _t("archive.message_5c55a67935af")], ...[_t("archive.message_d834c184c0e7"), _t("archive.message_c7253a80c714"), _t("archive.message_263200d7d529"), _t("archive.message_373febf03109")].map((c) => [c, c])].map(([k, n]) => __nscVueView(__viewContext.templates[270], [__viewContext.x.marketCategory === k ? 'active' : '', k, n])), ''), __nscVueJoin(ts.map((t) => __nscVueView(__viewContext.templates[271], [t.color, t.glyph, __viewContext.esc(t.name), __viewContext.esc(t.cat), t.version, t.stateful ? __viewContext.badge(_t("archive.message_9cf2e244f3a6"), 'amber') : __viewContext.badge(_t("archive.message_27b7a80b46d8"), 'blue'), __viewContext.esc(t.desc), t.modes.includes('compose') ? __viewContext.badge(_t("archive.message_ce2e77adba77")) : '', t.modes.includes('stack') ? __viewContext.badge(_t("archive.message_2fdc66106e25")) : '', t.modes.includes('container') ? __viewContext.badge(_t("archive.message_95e86cd83827")) : '', t.services.length, t.secret ? _t("archive.message_b9012e9131d0") : _t("archive.message_b8253afae454"), __viewContext.TB(_t("console.details"), 'x-template-info', '', __nscVueAdd(__nscVueInterpolate(["data-id=\"", "\""], [t.id]), " data-card-primary")), __viewContext.B(_t("archive.message_f75619a46f2f"), 'x-template', 'plus', 'sm primary', __nscVueInterpolate(["data-id=\"", "\""], [t.id]))])), '') || __nscVueView(__viewContext.templates[272], []), __viewContext.xFooter()]);
}
export function serviceTable_52(__viewContext, ss, showScope = true) {
  return __nscVueView(__viewContext.templates[280], [showScope ? __nscVueView(__viewContext.templates[274], []) : '', __nscVueJoin(ss.map((s) => {
    const st = __viewContext.serviceState(s),
      events = __viewContext.db.runtimeEvents.filter((e) => e.service === s.id && e.category === 'fault');
    return __nscVueView(__viewContext.templates[278], [s.id, __viewContext.I('box'), s.id, __viewContext.esc(_serviceName(__viewContext.db.services, s.id)), s.workloadKind === 'job' ? _t("archive.message_3950bad5b197") : s.workloadKind === 'global' ? _t("archive.message_bb1d2cdf0559") : s.mode === 'container' ? _t("archive.message_95e86cd83827") : s.mode === 'compose' ? _t("cluster.kind.compose") : 'Swarm Service', s.managed ? _t("archive.message_bd50041740c9") : _t("archive.message_e654a5974688"), showScope ? __nscVueView(__viewContext.templates[275], [__viewContext.esc(__viewContext.projectName(s.project)), __viewContext.esc(__viewContext.envName(s.env))]) : '', s.app ? __viewContext.TB(__viewContext.esc(__viewContext.APP(s.app)?.name), 'x-app', '', __nscVueInterpolate(["data-id=\"", "\""], [s.app])) : __nscVueView(__viewContext.templates[276], []), __viewContext.esc(__viewContext.RT(s.runtimeId)?.name || s.runtime), _serviceVersionSummary(s, __viewContext.RT(s.runtimeId)?.connection === 'online'), __viewContext.badge(st.name, st.tone), __viewContext.esc(st.detail), events.length ? __viewContext.TB(__nscVueAdd(__nscVueAdd(_t("archive.message_aafb70b7aab2"), events.length), _t("archive.message_f81526efce19")), 'x-service-events', '', __nscVueInterpolate(["data-id=\"", "\""], [s.id])) : __nscVueView(__viewContext.templates[277], []), __viewContext.currentInstances(s).some((t) => t.kind === 'container') ? _t("archive.message_db6c542ca496") : _t("archive.message_c78704a5b8f9"), __viewContext.TB(_t("console.details"), 'x-service', 'right', __nscVueInterpolate(["data-id=\"", "\""], [s.id])), _serviceLifecycle(s).primary, _serviceLifecycle(s).secondary]);
  }), '') || __nscVueView(__viewContext.templates[279], [showScope ? 8 : 7])]);
}
export function servicesWorkspace_53(__viewContext) {
  const ss = __viewContext.serviceFiltered();
  let content = '';
  if (__viewContext.x.layout === 'flat') {
    const start = (__viewContext.x.svcPage - 1) * 12;
    content = __nscVueView(__viewContext.templates[281], [__viewContext.serviceTable(ss.slice(start, __nscVueAdd(start, 12))), ss.length ? __nscVueAdd(start, 1) : 0, Math.min(__nscVueAdd(start, 12), ss.length), ss.length, __viewContext.B(_t("console.previous"), 'x-page-prev', '', 'sm', __viewContext.x.svcPage === 1 ? 'disabled' : ''), __viewContext.B(_t("console.next_page"), 'x-page-next', '', 'sm', __nscVueAdd(start, 12) >= ss.length ? 'disabled' : '')]);
  } else {
    const pids = [...new Set(ss.map((s) => s.project))];
    content = __nscVueJoin(pids.map((pid) => __nscVueView(__viewContext.templates[283], [__viewContext.I('folder'), __viewContext.esc(__viewContext.projectName(pid)), __viewContext.badge(__nscVueAdd(ss.filter((s) => s.project === pid && s.status !== 'REMOVED').length, _t("archive.message_97827a9c3766"))), __nscVueJoin([...new Set(ss.filter((s) => s.project === pid).map((s) => s.env))].map((eid) => {
      const group = ss.filter((s) => s.project === pid && s.env === eid);
      return __nscVueView(__viewContext.templates[282], [__viewContext.I('layers', 'sm'), __viewContext.badge(__viewContext.envName(eid), __viewContext.E(eid)?.kind === 'production' ? 'green' : 'blue'), new Set(group.filter((s) => s.status !== 'REMOVED').map((s) => s.app).filter(Boolean)).size, group.filter((s) => s.status !== 'REMOVED').length, __viewContext.serviceTable(group, false)]);
    }), '')])), '') || __nscVueView(__viewContext.templates[284], []);
  }
  return __nscVueView(__viewContext.templates[287], [__viewContext.x.svcScope === 'all' ? __viewContext.breadcrumb([{
    label: _t("archive.message_21734c193385")
  }, {
    label: _t("archive.message_956e54cf334e")
  }]) : __viewContext.xCrumb(_t("compose.services")), __viewContext.xHead(_t("compose.services"), _t("archive.message_53a2dc6770ff"), __nscVueAdd(__nscVueAdd(__viewContext.B(_t('services.show_removed'), 'x-removed-toggle', '', __viewContext.x.includeRemoved ? 'active' : '', `aria-pressed="${__viewContext.x.includeRemoved}"`), __viewContext.B(_t("archive.message_f0f1d33cd7ba"), 'x-all-services', 'layers')), __viewContext.B(_t("archive.message_3a4a165a145b"), 'x-install', 'plus', 'primary')), 'SERVICES'), __viewContext.x.svcScope === 'all' ? __nscVueView(__viewContext.templates[285], [__viewContext.I('globe', 'sm')]) : '', __viewContext.I('search'), __viewContext.esc(__viewContext.x.svcSearch), __viewContext.x.svcScope === 'environment' ? 'selected' : '', __viewContext.x.svcScope === 'project' ? 'selected' : '', __viewContext.x.svcScope === 'all' ? 'selected' : '', __nscVueJoin(__viewContext.db.runtimes.map((r) => __nscVueView(__viewContext.templates[286], [r.id, __viewContext.x.svcRuntime === r.id ? 'selected' : '', __viewContext.esc(r.name)])), ''), __viewContext.x.svcHealth === 'issue' ? 'selected' : '', __viewContext.x.svcHealth === 'external' ? 'selected' : '', __viewContext.x.layout === 'grouped' ? 'active' : '', __viewContext.x.layout === 'grouped', __viewContext.I('folder', 'sm'), __viewContext.x.layout === 'flat' ? 'active' : '', __viewContext.x.layout === 'flat', __viewContext.I('layers', 'sm'), content, __viewContext.xFooter(), __viewContext.x.svcHealth === 'running' ? 'selected' : '']);
}
export function tabs_54(__viewContext, items, active, action) {
  return __nscVueView(__viewContext.templates[289], [__nscVueJoin(items.map(([k, n, i]) => __nscVueView(__viewContext.templates[288], [active === k ? 'active' : '', action, k, __viewContext.I(i || 'file', 'sm'), n])), '')]);
}
export function applicationPage_55(__viewContext) {
  const a = __viewContext.APP(__viewContext.x.app);
  if (!a) return __nscVueView(__viewContext.templates[290], []);
  const ss = __viewContext.appServices(a),
    st = a.system && a.status === 'UNKNOWN' ? {
      name: _t('community.evidence_state.UNKNOWN'),
      tone: 'amber'
    } : __viewContext.appState(a);
  let body = '';
  if (__viewContext.x.appTab === 'overview') body = __nscVueView(__viewContext.templates[293], [__viewContext.xMetric(ss.length, _t("compose.services"), _t("archive.message_10b349734c7a"), "", "data-action=\"x-app-tab\" data-id=\"overview\""), __viewContext.xMetric(ss.flatMap(__viewContext.currentInstances).filter((t) => t.state === 'RUNNING').length, _t("cluster.running"), _t("archive.message_d370ca2ad61a"), "", "data-action=\"x-app-tab\" data-id=\"overview\""), __viewContext.xMetric(new Set(ss.flatMap(__viewContext.refsFor).map((r) => r.d.id)).size, _t("archive.message_324153ade43b"), _t("archive.message_7b9363cf36bc"), "", "data-action=\"x-app-tab\" data-id=\"overview\""), __viewContext.xMetric(__viewContext.db.runtimeEvents.filter((e) => e.app === a.id && e.category === 'fault').length, _t("archive.message_7328179ca46e"), _t("archive.message_712d4cf95261"), "", "data-action=\"x-app-tab\" data-id=\"events\""), __viewContext.cardHead(_t("archive.message_79b491ade724"), _t("archive.message_f6ca8153cc0e"), __viewContext.B(_t("archive.message_154c912dc02c"), 'x-app-tab', 'history', 'sm', 'data-id="history"'), 'box'), __viewContext.serviceTable(ss, false), __nscVueJoin(ss.flatMap(__viewContext.refsFor).map(({
    d,
    r
  }) => __nscVueView(__viewContext.templates[291], [__viewContext.I('globe'), __viewContext.esc(d.name), __viewContext.esc(r.path), __viewContext.esc(_serviceName(__viewContext.db.services, r.service)), __viewContext.badge(d.rev ? _t("archive.message_c0c692080e88") : _t("archive.message_5f8aa2050974"), d.rev ? 'green' : 'blue'), __viewContext.TB(_t("archive.message_3f90761c6e4a"), 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\" data-tab=\"config\""], [d.id]))])), '') || __nscVueView(__viewContext.templates[292], []), a.system ? '—' : a.version, __viewContext.esc(__viewContext.RT(a.runtime)?.name), a.system ? _t('systemApplication.protected') : a.managed ? _t("archive.message_a3c46500f88a") : _t("archive.message_2217a650e2ae")]);
  if (__viewContext.x.appTab === 'config') body = __nscVueView(__viewContext.templates[295], [__viewContext.cardHead(__nscVueAdd(_t("archive.message_cd726c11ea66"), a.system ? '—' : a.version), _t("archive.message_4e4727ab2c9f"), __viewContext.B(_t("archive.message_024259e7e16f"), 'x-export-app', 'download', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [a.id])), 'file'), a.raw ? __nscVueView(__viewContext.templates[294], [__viewContext.esc(a.raw)]) : '', __viewContext.esc(__viewContext.specText(__viewContext.appSpec(a)))]);
  if (__viewContext.x.appTab === 'history') body = __nscVueAdd(__viewContext.deployTable(__viewContext.db.appDeployments.filter((o) => o.app === a.id)), __nscVueView(__viewContext.templates[297], [__viewContext.cardHead(_t("archive.message_feb7d97d77a4"), _t("archive.message_064e531029b6"), '', 'history'), __nscVueJoin(a.revisions.slice().reverse().map((r) => __nscVueView(__viewContext.templates[296], [r.v, __viewContext.esc(r.note), __viewContext.esc(r.time), r.v !== (a.system ? '—' : a.version) && a.managed ? __viewContext.B(_t("archive.message_969fccd58984"), 'x-restore', 'history', 'sm', __nscVueInterpolate(["data-id=\"", "\" data-version=\"", "\""], [a.id, r.v])) : __viewContext.badge(_t("delivery.current_version"), 'green')])), '')]));
  if (__viewContext.x.appTab === 'events') body = __viewContext.eventsTable(__viewContext.db.runtimeEvents.filter((e) => e.app === a.id));
  return __nscVueView(__viewContext.templates[300], [__viewContext.xCrumb(__nscVueAdd(_t("archive.message_11e10b2ffa5e"), a.name)), __viewContext.xHead(a.name, __nscVueAdd(__nscVueAdd(__viewContext.scopeName(a.project, a.env), ' · '), __viewContext.MODE[a.mode]), __nscVueAdd(__viewContext.B(_t("archive.message_08ff1d942120"), 'x-nav', 'back', '', 'data-page="applications"'), __viewContext.B(_t("archive.message_083259ac4fb6"), 'x-runtime', 'server', '', __nscVueInterpolate(["data-id=\"", "\""], [a.runtime]))), 'APPLICATION'), __viewContext.I('box', 'lg'), __viewContext.esc(a.key), a.id, __viewContext.badge(st.name, st.tone), __viewContext.badge(a.system ? _t('systemApplication.label') : a.managed ? _t("archive.message_bd50041740c9") : _t("archive.message_175bd630e1df"), a.managed ? 'green' : ''), __viewContext.esc(__viewContext.envName(a.env)), a.system ? '—' : a.version, __viewContext.esc(__viewContext.RT(a.runtime)?.name), __viewContext.esc(a.system ? _t('systemApplication.label') : a.source), !a.managed ? __nscVueView(__viewContext.templates[298], [__viewContext.callout(a.system ? _t('systemApplication.protected') : _t("archive.message_e334915a6214"), 'amber')]) : '', a.draft ? __nscVueView(__viewContext.templates[299], [__viewContext.callout(__nscVueAdd(__nscVueAdd(__nscVueAdd(_t("archive.message_45a6e152aa0a"), a.draft.sourceVersion), _t("archive.message_859c2a36fbca")), __viewContext.TB(_t("archive.message_1696bde43162"), 'x-restore-review', 'right', __nscVueInterpolate(["data-id=\"", "\""], [a.id]))), 'amber')]) : '', __viewContext.tabs([['overview', _t("archive.message_17826325a886"), 'activity'], ['config', _t("archive.message_670f0a1d31c0"), 'file'], ['history', _t("archive.message_f8100de57eea"), 'history'], ['events', _t("prototype.runtime_records"), 'clock']], __viewContext.x.appTab, 'x-app-tab'), body, __viewContext.xFooter()]);
}
export function servicePage_56(__viewContext) {
  const s = __viewContext.ST(__viewContext.x.service);
  if (!s) return __nscVueView(__viewContext.templates[301], []);
  const st = __viewContext.serviceState(s),
    a = __viewContext.APP(s.app);
  let body = '';
  if (__viewContext.x.serviceTab === 'instances') body = __viewContext.instanceTable(s);
  if (__viewContext.x.serviceTab === 'events') body = __viewContext.eventsTable(__viewContext.db.runtimeEvents.filter((e) => e.service === s.id));
  if (__viewContext.x.serviceTab === 'logs') body = __viewContext.serviceLogsPage(s.id, __viewContext.RT(s.runtimeId)?.type);
  if (__viewContext.x.serviceTab === 'routes') body = __nscVueView(__viewContext.templates[304], [__nscVueJoin(__viewContext.refsFor(s).map(({
    d,
    r
  }) => __nscVueView(__viewContext.templates[302], [__viewContext.esc(d.name), __viewContext.esc(r.path), __viewContext.scopeName(d.project, d.env), __viewContext.TB(_t("archive.message_664ee29b844d"), 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\" data-tab=\"config\""], [d.id]))])), '') || __nscVueView(__viewContext.templates[303], [])]);
  if (__viewContext.x.serviceTab === 'config') body = __nscVueView(__viewContext.templates[306], [__viewContext.esc(s.image), s.workloadKind === 'global' ? _t("archive.message_abe7930c8759") : s.workloadKind === 'job' ? _t("archive.message_3950bad5b197") : s.desired, s.memory ? __nscVueAdd(s.memory, ' MiB') : _t("archive.message_0db3db44213b"), s.port || _t("archive.message_db7681b55987"), s.health === 'not-configured' ? _t("archive.message_c95d1fbd6323") : _t("archive.message_d1a0d368297b"), a ? __nscVueAdd(_t("archive.message_5d40b207c9d7"), __viewContext.esc(a.name)) : _t("archive.message_250483332cbf"), __viewContext.B(_t("archive.message_35720eddb2e6"), 'x-op-form', 'edit', '', __nscVueInterpolate(["data-id=\"", "\" data-kind=\"image\" ", ""], [s.id, !s.managed || s.workloadKind === 'job' ? 'disabled' : ''])), __viewContext.B(_t("archive.message_7361d7463742"), 'x-op-form', 'layers', '', __nscVueInterpolate(["data-id=\"", "\" data-kind=\"scale\" ", ""], [s.id, !s.managed || s.mode === 'container' || s.workloadKind !== 'replicated' ? 'disabled' : ''])), !s.managed ? __nscVueView(__viewContext.templates[305], []) : '']);
  return __nscVueView(__viewContext.templates[307], [s.project ? __viewContext.xCrumb(__nscVueAdd(_t("archive.message_8f7521b61dbd"), _serviceName(__viewContext.db.services, s.id))) : __viewContext.breadcrumb([{
    label: _t("archive.message_21734c193385")
  }, {
    label: _t("archive.message_6c2d0640c7b7")
  }, {
    label: _serviceName(__viewContext.db.services, s.id)
  }]), __viewContext.xHead(_serviceName(__viewContext.db.services, s.id), __nscVueAdd(__nscVueAdd(__viewContext.scopeName(s.project, s.env), ' · '), a ? a.name : _t("archive.message_ec2435c76826")), __nscVueAdd(__viewContext.B(_t("archive.message_083259ac4fb6"), 'x-runtime', 'server', '', __nscVueInterpolate(["data-id=\"", "\""], [s.runtimeId])), __viewContext.B(__viewContext.RT(s.runtimeId)?.type === 'swarm' ? _t("archive.message_5466c072293c") : _t("archive.message_f7d763a12cbf"), 'x-op-form', 'refresh', '', __nscVueInterpolate(["data-id=\"", "\" data-kind=\"restart\" ", ""], [s.id, !s.managed || s.workloadKind === 'job' ? _a("disabled title=\"", "archive.message_d77be447c551", "\"") : '']))), 'SERVICE'), __viewContext.I('layers', 'lg'), __viewContext.esc(s.id), __viewContext.esc(s.image), __viewContext.badge(st.name, st.tone), __viewContext.esc(st.detail), s.managed ? _t("archive.message_8ce8dd70d9f7") : _t("archive.message_175bd630e1df"), _serviceRevisionLabel(s.specVersion), __viewContext.RT(s.runtimeId)?.connection === 'online' ? _serviceRevisionLabel(s.observedVersion, 'service.version_not_confirmed') : _t('community.evidence_state.UNKNOWN'), __viewContext.tabs([['instances', _t("archive.message_71180fe9650e"), 'layers'], ['events', _t("archive.message_8d95f1639f99"), 'history'], ['logs', _t('serviceLogs.title'), 'terminal'], ['config', _t("archive.message_d87e47801b05"), 'settings'], ['routes', _t("archive.message_5cd7a6cb0fe7"), 'route']], __viewContext.x.serviceTab, 'x-service-tab'), body, __viewContext.xFooter(), _serviceLifecycle(s).detail]);
}
export function instanceTable_57(__viewContext, s) {
  const ins = __viewContext.currentInstances(s, __viewContext.x.includeHistory).sort((a, b) => b.started.localeCompare(a.started));
  return __nscVueView(__viewContext.templates[310], [__viewContext.RT(s.runtimeId)?.type === 'swarm' ? _t("archive.message_96abbeecaabc") : _t("archive.message_3d122b7e28d2"), __viewContext.x.includeHistory ? 'checked' : '', __nscVueJoin(ins.map((t) => {
    const unknown = _observationUnknown(__viewContext.RT(t.runtime));
    return __nscVueView(__viewContext.templates[308], [t.id, __viewContext.esc(t.id), t.kind === 'task' ? 'Task' : 'Container', t.current ? _t("archive.message_cb62ebd689ee") : _t("archive.message_b0385cfe4b42"), __viewContext.esc(t.slot), t.node, __viewContext.esc(__viewContext.NODE(t.node)?.name || t.node), __viewContext.esc(t.containerId), __viewContext.badge(unknown ? _t("community.evidence_state.UNKNOWN") : __viewContext.statusCN[t.state], unknown ? 'amber' : ['FAILED', 'REJECTED', 'ORPHANED'].includes(t.state) ? 'red' : t.state === 'RUNNING' || t.state === 'COMPLETE' ? 'green' : ''), t.desiredState, unknown ? __nscVueAdd(__nscVueAdd(_t("archive.message_5fe4c4e98992"), t.state), '）') : t.state, t.exitCode === null ? t.health === 'healthy' ? _t("archive.message_ee7ce3b31354") : t.health === 'not-configured' ? _t("archive.message_046f11a70d69") : '—' : __nscVueAdd(_t("archive.message_4bb5be8356b9"), t.exitCode), t.oomKilled === true ? _t("archive.message_7c2cdc7ec74a") : t.oomKilled === false ? _t("archive.message_4f7b434e0929") : _t("archive.message_208b67ebe471"), _instanceTimeRows(t).primary, _instanceTimeRows(t).secondary, __viewContext.esc(t.cause), t.release ? _t("archive.message_ab645bf8a041") : _t("archive.message_ff86c6fa211a"), __viewContext.TB(_t("console.details"), 'x-instance', 'right', __nscVueInterpolate(["data-id=\"", "\""], [t.id]))]);
  }), '') || __nscVueView(__viewContext.templates[309], [])]);
}
export function runtimesPage_58(__viewContext) {
  return __nscVueView(__viewContext.templates[317], [__viewContext.xCrumb(_t("prototype.clusters_hosts"), true), __viewContext.xHead(_t("prototype.clusters_hosts"), _t("archive.message_d69f406c46dc"), __viewContext.B(_t("archive.message_65242153c831"), 'x-connect', 'plus', 'primary'), 'INFRASTRUCTURE'), __nscVueJoin(__viewContext.db.runtimes.map((r) => {
    const c = __viewContext.clusterSummary(r);
    return __nscVueView(__viewContext.templates[316], [r.type === 'docker' ? 'blue' : '', __viewContext.I('server', 'lg'), __viewContext.esc(r.name), r.type === 'swarm' ? 'Docker Swarm' : _t("archive.message_152c5ce428b7"), __viewContext.badge(!c.known ? _t("archive.message_c306f7fafb4f") : !c.quorum ? _t("archive.message_88b329d81549") : c.ready < c.total ? _t("archive.message_1c70903e63c1") : _t("archive.message_da0bfef256bc"), !c.known || c.ready < c.total || !c.quorum ? 'amber' : 'green'), c.known ? c.ready : '?', c.total, c.known ? c.apps.length : '?', c.known ? c.services.length : '?', r.canWrite ? _t("archive.message_2c031bc67cbf") : _t("archive.message_ef42b5f3a3e6"), r.type === 'swarm' ? __nscVueAdd(__nscVueAdd(__nscVueAdd(' · Manager ', c.known ? c.reachable : '?'), '/'), c.managers.length) : '', c.known ? r.nodes.filter((n) => n.agent).length : '?', r.nodes.length, r.gaps.length ? _t("archive.message_90c6a41917c9") : '', r.observed, __viewContext.B(_t("archive.message_945f32e86238"), 'x-runtime', 'right', 'sm primary', __nscVueAdd(__nscVueInterpolate(["data-id=\"", "\""], [r.id]), " data-card-primary"))]);
  }), ''), __viewContext.xFooter()]);
}
export function runtimePage_59(__viewContext) {
  const r = __viewContext.RT(__viewContext.x.runtime);
  if (!r) return __nscVueView(__viewContext.templates[318], []);
  const c = __viewContext.clusterSummary(r);
  let body = '';
  if (__viewContext.x.runtimeTab === 'overview') body = __nscVueView(__viewContext.templates[319], [__viewContext.xMetric(c.known ? __nscVueAdd(__nscVueAdd(c.ready, ' / '), c.total) : _t("community.evidence_state.UNKNOWN"), _t("archive.message_1ef941b4e346"), r.type === 'swarm' ? __nscVueInterpolate(["", _t("archive.message_6d4b826f9d87"), _t("archive.message_32234427065b")], [c.managers.length, c.total - c.managers.length]) : _t("archive.message_7f6d4fbcd004"), c.ready < c.total ? 'warn' : '', "data-action=\"x-runtime-tab\" data-id=\"nodes\""), __viewContext.xMetric(c.known ? c.services.length : _t("community.evidence_state.UNKNOWN"), _t("compose.services"), __nscVueInterpolate(["", _t("archive.message_3d5fd490d7a4")], [c.services.filter((s) => !s.project).length]), "", "data-action=\"x-runtime-tab\" data-id=\"services\""), __viewContext.xMetric(c.known ? c.apps.length : _t("community.evidence_state.UNKNOWN"), _t("archive.message_63c73c4730f4"), __nscVueInterpolate(["", _t("archive.message_96d6762b2353")], [new Set(c.apps.map((a) => a.project)).size]), "", "data-action=\"x-runtime-tab\" data-id=\"overview\""), __viewContext.xMetric(c.known ? __viewContext.db.instances.filter((t) => t.runtime === r.id && t.current && t.state === 'RUNNING').length : _t("community.evidence_state.UNKNOWN"), _t("cluster.running"), _t("archive.message_7781609351b2"), "", "data-action=\"x-runtime-tab\" data-id=\"services\""), __viewContext.badge(_t("archive.message_1049329cd553"), 'blue'), __viewContext.badge(c.known ? _t("archive.message_b9086662b1df") : _t("archive.message_481cf3adf449"), c.known ? 'green' : 'amber'), r.canWrite ? _t("archive.message_3790631d6a26") : _t("archive.message_ef42b5f3a3e6"), r.type === 'swarm' ? c.known ? __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__viewContext.badge(c.quorum ? _t("archive.message_cc7ee1212d3d") : _t("archive.message_82fb0a966a10"), c.quorum ? 'green' : 'red'), ' · '), c.reachable), '/'), c.managers.length) : _t("archive.message_93e474c68ed5") : _t("archive.message_1c098afa30dc"), c.known ? __nscVueAdd(c.services.filter((s) => __viewContext.serviceState(s).key === 'issue').length, _t("archive.message_b7665047b361")) : _t("archive.message_5b24aae152b0"), c.known ? r.nodes.filter((n) => n.agent).length : _t("community.evidence_state.UNKNOWN"), r.nodes.length, r.gaps.length ? __nscVueAdd(' · ', __viewContext.TB(_t("archive.message_a83362e7718c"), 'x-runtime-tab', 'right', 'data-id="events"')) : '', c.known ? r.nodes.filter((n) => n.cpu !== null).length : _t("community.evidence_state.UNKNOWN"), r.nodes.length, r.observed, __viewContext.cardHead(_t("archive.message_24ba23758659"), _t("archive.message_09e072365f0c"), '', 'box'), __viewContext.appTable(c.apps)]);
  if (__viewContext.x.runtimeTab === 'nodes') body = __nscVueView(__viewContext.templates[321], [__viewContext.cardHead(_t("cluster.nodes"), _t("archive.message_cd63f756a3e1"), '', 'server'), __nscVueJoin(r.nodes.map((n) => __nscVueView(__viewContext.templates[320], [n.id, __viewContext.esc(n.name), __viewContext.esc(n.id), n.role === 'manager' ? _t("cluster.state_manager") : n.role === 'host' ? _t("archive.message_b5445fb7a1b9") : _t("cluster.state_worker"), n.manager || '—', __viewContext.badge(c.known ? n.status : _t("community.evidence_state.UNKNOWN"), c.known && n.status === 'Ready' ? 'green' : 'amber'), n.availability, n.availability === 'Drain' ? _t("archive.message_156fb6d14fbc") : n.availability === 'Pause' ? _t("archive.message_5e8faaa10177") : _t("archive.message_184a4f27243f"), c.known ? __viewContext.db.instances.filter((t) => t.node === n.id && t.current && t.state === 'RUNNING').length : _t("community.evidence_state.UNKNOWN"), _nodeMetric(n, 'cpu'), _nodeMetric(n, 'memory'), __viewContext.badge(c.known && n.agent ? _t("archive.message_5552873aec4f") : _t("archive.message_b08e2d9f317c"), c.known && n.agent ? 'green' : 'amber'), n.sample, __viewContext.esc(_nodeHealth(n, 'controller')), __viewContext.esc(_nodeHealth(n, 'collector')), __viewContext.esc(_nodeHealth(n, 'gateway')), __viewContext.esc(_nodeHealth(n, 'disk'))])), '')]);
  if (__viewContext.x.runtimeTab === 'services') body = __nscVueView(__viewContext.templates[322], [__viewContext.cardHead(_t("archive.message_6e437216911b"), _t("archive.message_ef65ca1a7bec"), '', 'layers'), __viewContext.serviceTable(c.services)]);
  if (__viewContext.x.runtimeTab === 'events') body = __nscVueView(__viewContext.templates[324], [__nscVueJoin(r.gaps.map((g) => __nscVueView(__viewContext.templates[323], [__viewContext.callout(__nscVueInterpolate(["", " \xB7 ", "\u2014", "\uFF1A", _t("archive.message_ca2ae82c1ee0")], [__viewContext.esc(__viewContext.NODE(g.node)?.name || g.node), g.from, g.to, __viewContext.esc(g.reason)]), 'amber')])), ''), __viewContext.eventsTable(__viewContext.db.runtimeEvents.filter((e) => e.runtime === r.id))]);
  return __nscVueView(__viewContext.templates[325], [__viewContext.breadcrumb([{
    label: _t("prototype.clusters_hosts"),
    action: 'x-nav',
    attrs: 'data-page="runtimes"'
  }, {
    label: r.name
  }]), __viewContext.xHead(r.name, __nscVueInterpolate(["", " \xB7 ", _t("archive.message_55b5c535e9fe")], [r.type === 'swarm' ? 'Docker Swarm' : _t("archive.message_152c5ce428b7"), r.engineId]), __nscVueAdd(__viewContext.B(_t("archive.message_82479cb6ca73"), 'x-runtime-info', 'info', '', __nscVueInterpolate(["data-id=\"", "\""], [r.id])), __viewContext.B(c.known ? _t("archive.message_8df2e53806e5") : _t("archive.message_cc765bddf4b8"), 'x-runtime-toggle', 'refresh', '', __nscVueInterpolate(["data-id=\"", "\""], [r.id]))), 'RUNTIME OVERVIEW'), __viewContext.I('globe', 'sm'), r.observed, c.known ? '' : _t("archive.message_badaf4f6eb50"), !c.known ? __viewContext.callout(_t("archive.message_ca909683b08a"), 'amber') : '', __viewContext.tabs([['overview', _t("cluster.overview"), 'grid'], ['nodes', _t("cluster.nodes"), 'server'], ['services', _t("archive.message_d59d60a54b95"), 'box'], ['events', _t("cluster.events"), 'activity'], ['resources', _t("dockerResources.title"), 'box']], __viewContext.x.runtimeTab, 'x-runtime-tab'), body, __viewContext.xFooter()]);
}
export function eventLabel_60(__viewContext, e) {
  return {
    fault: _t("archive.message_d194bf5c23df"),
    planned: _t("archive.message_a5752150a4aa"),
    gap: _t("archive.message_c16495c5dd4b"),
    completed: _t("archive.message_904586ca4cf1"),
    operation: _t("archive.message_145c3c1358c5"),
    restart: _t("archive.message_b4e9fd1fee18")
  }[e.category] || _t("archive.message_ebe1629d7e50");
}
export function eventsTable_61(__viewContext, es) {
  return __nscVueView(__viewContext.templates[328], [__nscVueJoin(es.slice().sort((a, b) => b.time.localeCompare(a.time)).map((e) => __nscVueView(__viewContext.templates[326], [__viewContext.esc(e.time), __viewContext.esc(e.id), e.id, __viewContext.esc(e.title), __viewContext.eventLabel(e), __viewContext.esc(e.actor), __viewContext.esc(__viewContext.projectName(e.project)), __viewContext.esc(__viewContext.envName(e.env)), e.service ? __viewContext.esc(_serviceName(__viewContext.db.services, e.service)) : __viewContext.esc(__viewContext.RT(e.runtime)?.name), __viewContext.esc(__viewContext.NODE(e.node)?.name || '—'), __viewContext.badge(e.state === 'recovered' ? _t("archive.message_0f66d88ce2b7") : e.state === 'open' ? _t("archive.message_999a459c3f3f") : _t("archive.message_5ebeeb543bc6"), e.state === 'recovered' ? 'green' : e.state === 'open' ? 'amber' : 'blue'), e.confidence === 'confirmed' ? _t("archive.message_28e2e3707fc4") : e.confidence === 'inferred' ? _t("archive.message_8a5a0d2e7529") : _t("archive.message_cd55f4f2d260"), __viewContext.TB(_t("archive.message_11da28dc7f84"), 'x-event', 'right', __nscVueInterpolate(["data-id=\"", "\""], [e.id]))])), '') || __nscVueView(__viewContext.templates[327], [])]);
}
export function runtimeEventsPage_62(__viewContext) {
  const es = __viewContext.db.runtimeEvents.filter((e) => __viewContext.x.eventFilter === 'all' || e.category === __viewContext.x.eventFilter);
  return __nscVueView(__viewContext.templates[330], [__viewContext.xCrumb(_t("prototype.runtime_records"), true), __viewContext.xHead(_t("prototype.runtime_records"), _t("archive.message_cddb754f16e3"), __nscVueAdd(__viewContext.B(_t("archive.message_b4fbe589c50e"), 'x-export-events', 'download'), __viewContext.B(_t("archive.message_30e0de5d8d8a"), 'x-event', 'activity', 'primary', 'data-id="INC-018"')), 'OPERATIONS TIMELINE'), __viewContext.I('globe', 'sm'), __nscVueJoin([['all', _t("archive.message_5c55a67935af")], ['fault', _t("archive.message_30922f82efcd")], ['planned', _t("archive.message_a5752150a4aa")], ['restart', _t("archive.message_b4e9fd1fee18")], ['completed', _t("archive.message_904586ca4cf1")], ['gap', _t("archive.message_c16495c5dd4b")]].map(([k, n]) => __nscVueView(__viewContext.templates[329], [__viewContext.x.eventFilter === k ? 'active' : '', k, n])), ''), __viewContext.eventsTable(es), __viewContext.xFooter()]);
}
export function deployState_63(__viewContext, o) {
  return {
    success: [_t("archive.message_173e9809e175"), 'green'],
    partial: [_t("archive.message_7e214ed64ae3"), 'amber'],
    unknown: [_t("archive.message_fb7e71452c04"), 'amber'],
    running: [_t("archive.message_096fbd860a9f"), 'blue'],
    failed: [_t("community.state.FAILED"), 'red']
  }[o.state] || [_t("archive.message_251bc40e7027"), ''];
}
export function deployTable_64(__viewContext, os) {
  return __nscVueView(__viewContext.templates[338], [__nscVueJoin(os.slice().sort((a, b) => b.time.localeCompare(a.time)).map((o) => __nscVueView(__viewContext.templates[336], [o.id, __viewContext.esc(o.action), __viewContext.esc(o.id), __viewContext.esc(__viewContext.APP(o.app)?.name || o.snapshot?.name), __viewContext.scopeName(o.project, o.env), o.version, o.targets.length, __viewContext.esc(__viewContext.RT(o.runtime)?.name), __viewContext.esc(o.actor), o.time, __viewContext.badge(...__viewContext.deployState(o)), o.attempts.length, __viewContext.TB(_t("console.details"), 'x-deployment', 'right', __nscVueInterpolate(["data-id=\"", "\""], [o.id]))])), '') || __nscVueView(__viewContext.templates[337], [])]);
}
export function specText_66(__viewContext, spec) {
  return __nscVueAdd('# 应用定义快照（原型展示，不是可直接投产的 Compose 编译器）\n', JSON.stringify(spec, null, 2));
}
export function templateInfo_67(__viewContext, id) {
  const t = __viewContext.tpl(id);
  __viewContext.showModal(_modal(() => ({
    kind: 'x-template-info',
    size: 'small',
    title: t.name,
    sub: __nscVueAdd(t.version, _t("archive.message_a41fbf977cfc")),
    body: __nscVueView(__viewContext.templates[377], [__viewContext.esc(t.desc), __nscVueJoin(t.services.map((s) => __viewContext.esc(s.name)), ' + '), __nscVueJoin(t.modes.map((m) => __viewContext.MODE[m]), __nscVueView(__viewContext.templates[376], [])), __viewContext.esc(t.notes)]),
    footer: __nscVueAdd(__viewContext.B(_t("console.close"), 'close'), __viewContext.B(_t("archive.message_7c9032975bfb"), 'x-template', 'plus', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [id])))
  })));
}
export function runtimeInfo_68(__viewContext, id) {
  const r = __viewContext.RT(id);
  if (!r) return;
  __viewContext.showModal(_modal(() => ({
    kind: 'x-runtime-info',
    size: 'small',
    title: _t("archive.message_4819b2f737bf"),
    sub: r.name,
    body: __nscVueView(__viewContext.templates[379], [r.engineId, r.type === 'swarm' ? 'Docker Swarm · Manager' : _t("archive.message_152c5ce428b7"), r.canWrite ? _t("archive.message_015f2f7590d8") : _t("archive.message_fbc8359f5168"), __nscVueJoin(r.allowedEnvs.map((id) => __viewContext.scopeName(__viewContext.E(id)?.project, id)), __nscVueView(__viewContext.templates[378], [])), r.historySince, __viewContext.callout(_t("archive.message_6bf96c06c36e"), 'amber')]),
    footer: __viewContext.B(_t("console.close"), 'close')
  })));
}
