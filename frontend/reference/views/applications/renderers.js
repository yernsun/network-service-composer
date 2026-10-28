import { view as __nscVueView, add as __nscVueAdd, join as __nscVueJoin, interpolateView as __nscVueInterpolate } from '../../components/view';
export function xMetric_45(__viewContext, value, label, desc = '', tone = '') {
  return __nscVueView(__viewContext.templates[255], [tone, __viewContext.esc(label), __viewContext.esc(value), __viewContext.esc(desc)]);
}
export function xHead_46(__viewContext, title, sub, actions = '', eyebrow = '') {
  return __nscVueView(__viewContext.templates[257], [eyebrow ? __nscVueView(__viewContext.templates[256], [__viewContext.esc(eyebrow)]) : '', __viewContext.esc(title), __viewContext.esc(sub), actions]);
}
export function xCrumb_47(__viewContext, last, global = false) {
  return global ? __viewContext.breadcrumb([{
    label: '基础设施'
  }, {
    label: last
  }]) : __viewContext.contextCrumb(last);
}
export function xFooter_48(__viewContext) {
  return __nscVueView(__viewContext.templates[258], []);
}
export function appTable_49(__viewContext, apps) {
  return __nscVueView(__viewContext.templates[265], [__nscVueJoin(apps.map(a => {
    const st = __viewContext.appState(a);
    return __nscVueView(__viewContext.templates[263], [a.id, __viewContext.esc(a.name), __viewContext.MODE[a.mode], __viewContext.badge(__viewContext.envName(a.env), __viewContext.E(a.env)?.kind === 'production' ? 'green' : 'blue'), __viewContext.appServices(a).length, __viewContext.esc(__viewContext.RT(a.runtime)?.name), __viewContext.badge(st.name, st.tone), a.managed ? '由 NSC 管理' : '外部管理 · 只读', __viewContext.TB('查看应用', 'x-app', 'right', __nscVueInterpolate(["data-id=\"", "\""], [a.id]))]);
  }), '') || __nscVueView(__viewContext.templates[264], [])]);
}
export function applicationsPage_50(__viewContext) {
  const as = __viewContext.db.apps.filter(__viewContext.inListScope);
  const ss = __viewContext.db.services.filter(s => __viewContext.inListScope(s) && s.app);
  const run = ss.flatMap(__viewContext.currentInstances).filter(t => t.state === 'RUNNING').length;
  return __nscVueView(__viewContext.templates[269], [__viewContext.xCrumb('应用'), __viewContext.xHead('应用', '按环境查看和维护整组服务。域名是可选入口，不是创建应用的前提。', __nscVueAdd(__viewContext.B('从应用市场选择', 'x-nav', 'grid', '', 'data-page="market"'), __viewContext.B('部署应用', 'x-install', 'plus', 'primary')), 'APPLICATIONS'), __viewContext.xMetric(as.length, '应用', __nscVueInterpolate(["", " / ", ""], [__viewContext.allProjectView() ? '全部项目' : __viewContext.P().name, __viewContext.allEnvironmentView() ? '所有环境' : __viewContext.E().name])), __viewContext.xMetric(ss.length, '应用服务', '逻辑服务，不按副本重复计数'), __viewContext.xMetric(run, '运行实例', '不包含已退出 Task 或已完成任务'), __viewContext.xMetric(as.filter(a => __viewContext.appState(a).tone === 'amber').length, '需要处理', '连接、健康或副本异常'), __viewContext.db.appDrafts.filter(__viewContext.inListScope).length ? __nscVueView(__viewContext.templates[266], [__viewContext.I('edit'), __viewContext.db.appDrafts.filter(__viewContext.inListScope).length, __viewContext.TB('继续编辑最近草稿', 'x-resume-wizard', 'right')]) : '', __nscVueJoin(as.map(a => {
    const s = __viewContext.appServices(a),
      st = __viewContext.appState(a);
    return __nscVueView(__viewContext.templates[267], [a.mode === 'compose' ? 'blue' : '', __viewContext.I('box', 'lg'), __viewContext.esc(a.name), __viewContext.allEnvironmentView() ? __viewContext.badge(__viewContext.resourceScopeName(a), __viewContext.E(a.env)?.kind === 'production' ? 'green' : 'blue') : '', __viewContext.esc(a.key), a.version, __viewContext.badge(st.name, st.tone), __viewContext.MODE[a.mode], __viewContext.esc(__viewContext.RT(a.runtime)?.name), a.managed ? '由 NSC 保存部署定义' : '只观察运行状态，更新仍由原工具负责', __nscVueJoin(s.slice(0, 4).map(s => __viewContext.badge(s.name || s.id, __viewContext.serviceState(s).key === 'issue' ? 'amber' : '')), ''), s.length, s.flatMap(__viewContext.currentInstances).filter(t => t.state === 'RUNNING').length, new Set(s.flatMap(__viewContext.refsFor).map(r => r.d.id)).size, a.managed ? '部署与配置可追溯' : '未接管应用 · 只读', __viewContext.B('打开应用', 'x-app', 'right', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [a.id]))]);
  }), '') || __nscVueView(__viewContext.templates[268], [__viewContext.I('box'), __viewContext.allEnvironmentView() ? '当前范围还没有应用' : '这个环境还没有应用', __viewContext.B('部署第一个应用', 'x-install', 'plus', 'primary')]), __viewContext.xFooter()]);
}
export function marketPage_51(__viewContext) {
  const ts = __viewContext.TEMPLATES.filter(t => (__viewContext.x.marketCategory === 'all' || t.cat === __viewContext.x.marketCategory) && __nscVueInterpolate(["", " ", ""], [t.name, t.desc]).toLowerCase().includes(__viewContext.x.marketSearch.toLowerCase()));
  return __nscVueView(__viewContext.templates[273], [__viewContext.breadcrumb([{
    label: '应用市场'
  }]), __viewContext.xHead('应用市场', '先选择要使用的应用，再决定部署到哪个项目、哪个环境和哪台主机或集群。', __nscVueAdd(__viewContext.B('导入编排文件', 'x-install-file', 'file'), __viewContext.B('从镜像创建', 'x-install-image', 'box')), 'APPLICATION CATALOG'), __viewContext.I('layers', 'lg'), __viewContext.badge('6 个演示模板', 'green'), __viewContext.I('search'), __viewContext.esc(__viewContext.x.marketSearch), __nscVueJoin([['all', '全部'], ...['业务应用', '内容管理', '数据库', '开发工具'].map(c => [c, c])].map(([k, n]) => __nscVueView(__viewContext.templates[270], [__viewContext.x.marketCategory === k ? 'active' : '', k, n])), ''), __nscVueJoin(ts.map(t => __nscVueView(__viewContext.templates[271], [t.color, t.glyph, __viewContext.esc(t.name), __viewContext.esc(t.cat), t.version, t.stateful ? __viewContext.badge('需保留数据', 'amber') : __viewContext.badge('无状态', 'blue'), __viewContext.esc(t.desc), t.modes.includes('compose') ? __viewContext.badge('单机 Compose') : '', t.modes.includes('stack') ? __viewContext.badge('Swarm 集群') : '', t.modes.includes('container') ? __viewContext.badge('单容器') : '', t.services.length, t.secret ? '需选择凭据' : '无需填写凭据', __viewContext.TB('详情', 'x-template-info', '', __nscVueInterpolate(["data-id=\"", "\""], [t.id])), __viewContext.B('使用模板', 'x-template', 'plus', 'sm primary', __nscVueInterpolate(["data-id=\"", "\""], [t.id]))])), '') || __nscVueView(__viewContext.templates[272], []), __viewContext.xFooter()]);
}
export function serviceTable_52(__viewContext, ss, showScope = true) {
  return __nscVueView(__viewContext.templates[280], [showScope ? __nscVueView(__viewContext.templates[274], []) : '', __nscVueJoin(ss.map(s => {
    const st = __viewContext.serviceState(s),
      events = __viewContext.db.runtimeEvents.filter(e => e.service === s.id && e.category === 'fault');
    return __nscVueView(__viewContext.templates[278], [s.id, __viewContext.I('box'), s.id, __viewContext.esc(s.name || s.id), s.workloadKind === 'job' ? '一次性任务' : s.workloadKind === 'global' ? '每个符合条件节点各 1 个' : s.mode === 'container' ? '单容器' : s.mode === 'compose' ? 'Compose 服务' : 'Swarm Service', s.managed ? 'NSC 管理' : '外部 / 只读', showScope ? __nscVueView(__viewContext.templates[275], [__viewContext.esc(__viewContext.projectName(s.project)), __viewContext.esc(__viewContext.envName(s.env))]) : '', s.app ? __viewContext.TB(__viewContext.esc(__viewContext.APP(s.app)?.name), 'x-app', '', __nscVueInterpolate(["data-id=\"", "\""], [s.app])) : __nscVueView(__viewContext.templates[276], []), __viewContext.esc(__viewContext.RT(s.runtimeId)?.name || s.runtime), s.specVersion ? __nscVueAdd(__nscVueAdd(__nscVueAdd('期望 r', s.specVersion), ' / 已观察 '), __viewContext.RT(s.runtimeId)?.connection === 'online' ? __nscVueAdd('r', s.observedVersion) : '未知') : '运行版本未登记', __viewContext.badge(st.name, st.tone), __viewContext.esc(st.detail), events.length ? __viewContext.TB(__nscVueAdd(__nscVueAdd('异常 ', events.length), ' 次'), 'x-service-events', '', __nscVueInterpolate(["data-id=\"", "\""], [s.id])) : __nscVueView(__viewContext.templates[277], []), __viewContext.currentInstances(s).some(t => t.kind === 'container') ? '容器重启单独记录' : 'Task 替换保留前后关系', __viewContext.TB('详情', 'x-service', 'right', __nscVueInterpolate(["data-id=\"", "\""], [s.id]))]);
  }), '') || __nscVueView(__viewContext.templates[279], [showScope ? 7 : 6])]);
}
export function servicesWorkspace_53(__viewContext) {
  const ss = __viewContext.serviceFiltered();
  let content = '';
  if (__viewContext.x.layout === 'flat') {
    const start = (__viewContext.x.svcPage - 1) * 12;
    content = __nscVueView(__viewContext.templates[281], [__viewContext.serviceTable(ss.slice(start, __nscVueAdd(start, 12))), ss.length ? __nscVueAdd(start, 1) : 0, Math.min(__nscVueAdd(start, 12), ss.length), ss.length, __viewContext.B('上一页', 'x-page-prev', '', 'sm', __viewContext.x.svcPage === 1 ? 'disabled' : ''), __viewContext.B('下一页', 'x-page-next', '', 'sm', __nscVueAdd(start, 12) >= ss.length ? 'disabled' : '')]);
  } else {
    const pids = [...new Set(ss.map(s => s.project))];
    content = __nscVueJoin(pids.map(pid => __nscVueView(__viewContext.templates[283], [__viewContext.I('folder'), __viewContext.esc(__viewContext.projectName(pid)), __viewContext.badge(__nscVueAdd(ss.filter(s => s.project === pid).length, ' 个服务')), __nscVueJoin([...new Set(ss.filter(s => s.project === pid).map(s => s.env))].map(eid => {
      const group = ss.filter(s => s.project === pid && s.env === eid);
      return __nscVueView(__viewContext.templates[282], [__viewContext.I('layers', 'sm'), __viewContext.badge(__viewContext.envName(eid), __viewContext.E(eid)?.kind === 'production' ? 'green' : 'blue'), new Set(group.map(s => s.app).filter(Boolean)).size, group.length, __viewContext.serviceTable(group, false)]);
    }), '')])), '') || __nscVueView(__viewContext.templates[284], []);
  }
  return __nscVueView(__viewContext.templates[287], [__viewContext.x.svcScope === 'all' ? __viewContext.breadcrumb([{
    label: '资源中心'
  }, {
    label: '所有服务'
  }]) : __viewContext.xCrumb('服务'), __viewContext.xHead('服务', '同一份服务数据，按项目归组或平铺查看。视图切换不会创建、移动或重新部署服务。', __nscVueAdd(__viewContext.B('查看所有项目服务', 'x-all-services', 'layers'), __viewContext.B('部署应用', 'x-install', 'plus', 'primary')), 'SERVICES'), __viewContext.x.svcScope === 'all' ? __nscVueView(__viewContext.templates[285], [__viewContext.I('globe', 'sm')]) : '', __viewContext.I('search'), __viewContext.esc(__viewContext.x.svcSearch), __viewContext.x.svcScope === 'environment' ? 'selected' : '', __viewContext.x.svcScope === 'project' ? 'selected' : '', __viewContext.x.svcScope === 'all' ? 'selected' : '', __nscVueJoin(__viewContext.db.runtimes.map(r => __nscVueView(__viewContext.templates[286], [r.id, __viewContext.x.svcRuntime === r.id ? 'selected' : '', __viewContext.esc(r.name)])), ''), __viewContext.x.svcHealth === 'issue' ? 'selected' : '', __viewContext.x.svcHealth === 'external' ? 'selected' : '', __viewContext.x.layout === 'grouped' ? 'active' : '', __viewContext.x.layout === 'grouped', __viewContext.I('folder', 'sm'), __viewContext.x.layout === 'flat' ? 'active' : '', __viewContext.x.layout === 'flat', __viewContext.I('layers', 'sm'), content, __viewContext.xFooter()]);
}
export function tabs_54(__viewContext, items, active, action) {
  return __nscVueView(__viewContext.templates[289], [__nscVueJoin(items.map(([k, n, i]) => __nscVueView(__viewContext.templates[288], [active === k ? 'active' : '', action, k, __viewContext.I(i || 'file', 'sm'), n])), '')]);
}
export function applicationPage_55(__viewContext) {
  const a = __viewContext.APP(__viewContext.x.app);
  if (!a) return __nscVueView(__viewContext.templates[290], []);
  const ss = __viewContext.appServices(a),
    st = __viewContext.appState(a);
  let body = '';
  if (__viewContext.x.appTab === 'overview') body = __nscVueView(__viewContext.templates[293], [__viewContext.xMetric(ss.length, '服务', '包括一次性应用任务'), __viewContext.xMetric(ss.flatMap(__viewContext.currentInstances).filter(t => t.state === 'RUNNING').length, '运行实例', '终止 Task 不计入'), __viewContext.xMetric(new Set(ss.flatMap(__viewContext.refsFor).map(r => r.d.id)).size, '关联域名', '应用部署与入口发布独立'), __viewContext.xMetric(__viewContext.db.runtimeEvents.filter(e => e.app === a.id && e.category === 'fault').length, '已采集异常', '包含已经自动恢复的事件'), __viewContext.cardHead('应用服务', '从服务进入实例与退出历史。', __viewContext.B('查看部署历史', 'x-app-tab', 'history', 'sm', 'data-id="history"'), 'box'), __viewContext.serviceTable(ss, false), __nscVueJoin(ss.flatMap(__viewContext.refsFor).map(({
    d,
    r
  }) => __nscVueView(__viewContext.templates[291], [__viewContext.I('globe'), __viewContext.esc(d.name), __viewContext.esc(r.path), __viewContext.esc(__viewContext.ST(r.service)?.name || r.service), __viewContext.badge(d.rev ? '已发布入口' : '入口草稿', d.rev ? 'green' : 'blue'), __viewContext.TB('Nginx 配置', 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\" data-tab=\"config\""], [d.id]))])), '') || __nscVueView(__viewContext.templates[292], []), a.version, __viewContext.esc(__viewContext.RT(a.runtime)?.name), a.managed ? '通过 NSC 部署与维护' : '原工具继续管理，NSC 只读观察']);
  if (__viewContext.x.appTab === 'config') body = __nscVueView(__viewContext.templates[295], [__viewContext.cardHead(__nscVueAdd('应用定义 · r', a.version), '查看完整服务配置；从服务发起更改后，仍记录为这份应用的新版本。', __viewContext.B('导出定义快照', 'x-export-app', 'download', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [a.id])), 'file'), a.raw ? __nscVueView(__viewContext.templates[294], [__viewContext.esc(a.raw)]) : '', __viewContext.esc(__viewContext.specText(__viewContext.appSpec(a)))]);
  if (__viewContext.x.appTab === 'history') body = __nscVueAdd(__viewContext.deployTable(__viewContext.db.appDeployments.filter(o => o.app === a.id)), __nscVueView(__viewContext.templates[297], [__viewContext.cardHead('配置版本', '旧版本只能生成新的恢复草稿，不改写历史。', '', 'history'), __nscVueJoin(a.revisions.slice().reverse().map(r => __nscVueView(__viewContext.templates[296], [r.v, __viewContext.esc(r.note), __viewContext.esc(r.time), r.v !== a.version && a.managed ? __viewContext.B('从此版本创建草稿', 'x-restore', 'history', 'sm', __nscVueInterpolate(["data-id=\"", "\" data-version=\"", "\""], [a.id, r.v])) : __viewContext.badge('当前版本', 'green')])), '')]));
  if (__viewContext.x.appTab === 'events') body = __viewContext.eventsTable(__viewContext.db.runtimeEvents.filter(e => e.app === a.id));
  return __nscVueView(__viewContext.templates[300], [__viewContext.xCrumb(__nscVueAdd('应用 / ', a.name)), __viewContext.xHead(a.name, __nscVueAdd(__nscVueAdd(__viewContext.scopeName(a.project, a.env), ' · '), __viewContext.MODE[a.mode]), __nscVueAdd(__viewContext.B('返回应用列表', 'x-nav', 'back', '', 'data-page="applications"'), __viewContext.B('运行位置', 'x-runtime', 'server', '', __nscVueInterpolate(["data-id=\"", "\""], [a.runtime]))), 'APPLICATION'), __viewContext.I('box', 'lg'), __viewContext.esc(a.key), a.id, __viewContext.badge(st.name, st.tone), __viewContext.badge(a.managed ? 'NSC 管理' : '外部管理 · 只读', a.managed ? 'green' : ''), __viewContext.esc(__viewContext.envName(a.env)), a.version, __viewContext.esc(__viewContext.RT(a.runtime)?.name), __viewContext.esc(a.source), !a.managed ? __nscVueView(__viewContext.templates[298], [__viewContext.callout('只观察此应用，不会从发现信息反推完整部署文件。取得原始定义并通过接管预检后，才能在 NSC 更新。', 'amber')]) : '', a.draft ? __nscVueView(__viewContext.templates[299], [__viewContext.callout(__nscVueAdd(__nscVueAdd(__nscVueAdd('有一份来自 r', a.draft.sourceVersion), ' 的恢复草稿。 '), __viewContext.TB('检查并部署恢复草稿', 'x-restore-review', 'right', __nscVueInterpolate(["data-id=\"", "\""], [a.id]))), 'amber')]) : '', __viewContext.tabs([['overview', '运行概览', 'activity'], ['config', '应用配置', 'file'], ['history', '部署历史', 'history'], ['events', '运行记录', 'clock']], __viewContext.x.appTab, 'x-app-tab'), body, __viewContext.xFooter()]);
}
export function servicePage_56(__viewContext) {
  const s = __viewContext.ST(__viewContext.x.service);
  if (!s) return __nscVueView(__viewContext.templates[301], []);
  const st = __viewContext.serviceState(s),
    a = __viewContext.APP(s.app);
  let body = '';
  if (__viewContext.x.serviceTab === 'instances') body = __viewContext.instanceTable(s);
  if (__viewContext.x.serviceTab === 'events') body = __viewContext.eventsTable(__viewContext.db.runtimeEvents.filter(e => e.service === s.id));
  if (__viewContext.x.serviceTab === 'routes') body = __nscVueView(__viewContext.templates[304], [__nscVueJoin(__viewContext.refsFor(s).map(({
    d,
    r
  }) => __nscVueView(__viewContext.templates[302], [__viewContext.esc(d.name), __viewContext.esc(r.path), __viewContext.scopeName(d.project, d.env), __viewContext.TB('打开配置工作台', 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\" data-tab=\"config\""], [d.id]))])), '') || __nscVueView(__viewContext.templates[303], [])]);
  if (__viewContext.x.serviceTab === 'config') body = __nscVueView(__viewContext.templates[306], [__viewContext.esc(s.image), s.workloadKind === 'global' ? '每个符合条件的节点 1 个' : s.workloadKind === 'job' ? '一次性任务' : s.desired, s.memory ? __nscVueAdd(s.memory, ' MiB') : '未登记', s.port || '无 HTTP 入口', s.health === 'not-configured' ? '未配置；运行中不代表业务健康' : '已配置（演示）', a ? __nscVueAdd('所属应用 ', __viewContext.esc(a.name)) : '外部独立服务', __viewContext.B('修改镜像', 'x-op-form', 'edit', '', __nscVueInterpolate(["data-id=\"", "\" data-kind=\"image\" ", ""], [s.id, !s.managed || s.workloadKind === 'job' ? 'disabled' : ''])), __viewContext.B('调整实例数量', 'x-op-form', 'layers', '', __nscVueInterpolate(["data-id=\"", "\" data-kind=\"scale\" ", ""], [s.id, !s.managed || s.mode === 'container' || s.workloadKind !== 'replicated' ? 'disabled' : ''])), !s.managed ? __nscVueView(__viewContext.templates[305], []) : '']);
  return __nscVueView(__viewContext.templates[307], [s.project ? __viewContext.xCrumb(__nscVueAdd('服务 / ', s.name || s.id)) : __viewContext.breadcrumb([{
    label: '资源中心'
  }, {
    label: '未归属服务'
  }, {
    label: s.name || s.id
  }]), __viewContext.xHead(s.name || s.id, __nscVueAdd(__nscVueAdd(__viewContext.scopeName(s.project, s.env), ' · '), a ? a.name : '独立服务'), __nscVueAdd(__viewContext.B('运行位置', 'x-runtime', 'server', '', __nscVueInterpolate(["data-id=\"", "\""], [s.runtimeId])), __viewContext.B(__viewContext.RT(s.runtimeId)?.type === 'swarm' ? '重新创建实例' : '重启容器', 'x-op-form', 'refresh', '', __nscVueInterpolate(["data-id=\"", "\" data-kind=\"restart\" ", ""], [s.id, !s.managed || s.workloadKind === 'job' ? 'disabled title="只读服务或一次性任务不提供此操作"' : '']))), 'SERVICE'), __viewContext.I('layers', 'lg'), __viewContext.esc(s.id), __viewContext.esc(s.image), __viewContext.badge(st.name, st.tone), __viewContext.esc(st.detail), s.managed ? 'NSC 应用托管' : '外部管理 · 只读', s.specVersion ? __nscVueAdd('r', s.specVersion) : '未登记', __viewContext.RT(s.runtimeId)?.connection === 'online' && s.observedVersion ? __nscVueAdd('r', s.observedVersion) : '未知', __viewContext.tabs([['instances', '运行实例 / Task', 'layers'], ['events', '退出与恢复记录', 'history'], ['config', '服务配置', 'settings'], ['routes', '域名与路由', 'route']], __viewContext.x.serviceTab, 'x-service-tab'), body, __viewContext.xFooter()]);
}
export function instanceTable_57(__viewContext, s) {
  const ins = __viewContext.db.instances.filter(t => t.service === s.id && (__viewContext.x.includeHistory || t.current)).sort((a, b) => b.started.localeCompare(a.started));
  return __nscVueView(__viewContext.templates[310], [__viewContext.RT(s.runtimeId)?.type === 'swarm' ? 'Task 是一次执行；终止后不会原地恢复运行。' : 'Container ID 不变时的重启，与创建新容器分开记录。', __viewContext.x.includeHistory ? 'checked' : '', __nscVueJoin(ins.map(t => {
    const unknown = __viewContext.RT(t.runtime)?.connection !== 'online';
    return __nscVueView(__viewContext.templates[308], [t.id, __viewContext.esc(t.id), t.kind === 'task' ? 'Task' : 'Container', t.current ? '当前' : '历史', __viewContext.esc(t.slot), t.node, __viewContext.esc(__viewContext.NODE(t.node)?.name || t.node), __viewContext.esc(t.containerId), __viewContext.badge(unknown ? '未知' : __viewContext.statusCN[t.state], unknown ? 'amber' : ['FAILED', 'REJECTED', 'ORPHANED'].includes(t.state) ? 'red' : t.state === 'RUNNING' || t.state === 'COMPLETE' ? 'green' : ''), t.desiredState, unknown ? __nscVueAdd(__nscVueAdd('未知（最后 ', t.state), '）') : t.state, t.exitCode === null ? t.health === 'healthy' ? '健康检查通过' : t.health === 'not-configured' ? '未设置健康检查' : '—' : __nscVueAdd('退出码 ', t.exitCode), t.oomKilled === true ? 'OOM 证据已确认' : t.oomKilled === false ? '未观测到 OOM' : 'OOM：未知 / 未采集', t.started.slice(5), t.finished ? __nscVueAdd('结束 ', t.finished.slice(5)) : '未结束', __viewContext.esc(t.cause), t.release ? '关联部署记录' : '不是人工部署', __viewContext.TB('详情', 'x-instance', 'right', __nscVueInterpolate(["data-id=\"", "\""], [t.id]))]);
  }), '') || __nscVueView(__viewContext.templates[309], [])]);
}
export function runtimesPage_58(__viewContext) {
  return __nscVueView(__viewContext.templates[317], [__viewContext.xCrumb('集群与主机', true), __viewContext.xHead('集群与主机', '查看共享基础设施。项目与环境引用运行位置，不拥有或复制集群。', __viewContext.B('连接已有集群或主机', 'x-connect', 'plus', 'primary'), 'INFRASTRUCTURE'), __nscVueJoin(__viewContext.db.runtimes.map(r => {
    const c = __viewContext.clusterSummary(r);
    return __nscVueView(__viewContext.templates[316], [r.type === 'docker' ? 'blue' : '', __viewContext.I('server', 'lg'), __viewContext.esc(r.name), r.type === 'swarm' ? 'Docker Swarm' : 'Docker Engine · 单机', __viewContext.badge(!c.known ? '连接中断' : !c.quorum ? '无法管理' : c.ready < c.total ? '节点待处理' : '连接正常', !c.known || c.ready < c.total || !c.quorum ? 'amber' : 'green'), c.known ? c.ready : '?', c.total, c.known ? c.apps.length : '?', c.known ? c.services.length : '?', r.canWrite ? '已授权部署' : '仅观察', r.type === 'swarm' ? __nscVueAdd(__nscVueAdd(__nscVueAdd(' · Manager ', c.known ? c.reachable : '?'), '/'), c.managers.length) : '', c.known ? r.nodes.filter(n => n.agent).length : '?', r.nodes.length, r.gaps.length ? ' · 存在缺口' : '', r.observed, __viewContext.B('查看概览', 'x-runtime', 'right', 'sm primary', __nscVueInterpolate(["data-id=\"", "\""], [r.id]))]);
  }), ''), __viewContext.xFooter()]);
}
export function runtimePage_59(__viewContext) {
  const r = __viewContext.RT(__viewContext.x.runtime);
  if (!r) return __nscVueView(__viewContext.templates[318], []);
  const c = __viewContext.clusterSummary(r);
  let body = '';
  if (__viewContext.x.runtimeTab === 'overview') body = __nscVueView(__viewContext.templates[319], [__viewContext.xMetric(c.known ? __nscVueAdd(__nscVueAdd(c.ready, ' / '), c.total) : '未知', '在线节点', r.type === 'swarm' ? __nscVueInterpolate(["", " \u4E2A\u7BA1\u7406\u8282\u70B9 \xB7 ", " \u4E2A\u5DE5\u4F5C\u8282\u70B9"], [c.managers.length, c.total - c.managers.length]) : '单台 Docker 主机', c.ready < c.total ? 'warn' : ''), __viewContext.xMetric(c.known ? c.services.length : '未知', '服务', __nscVueInterpolate(["", " \u4E2A\u672A\u5F52\u5C5E\u9879\u76EE\uFF1B\u4E0D\u6309 Task \u8BA1\u6570"], [c.services.filter(s => !s.project).length])), __viewContext.xMetric(c.known ? c.apps.length : '未知', '应用', __nscVueInterpolate(["", " \u4E2A\u9879\u76EE\u4F7F\u7528\u6B64\u8FD0\u884C\u4F4D\u7F6E"], [new Set(c.apps.map(a => a.project)).size])), __viewContext.xMetric(c.known ? __viewContext.db.instances.filter(t => t.runtime === r.id && t.current && t.state === 'RUNNING').length : '未知', '运行实例', '不计入历史 Task / 已完成任务'), __viewContext.badge('演示快照', 'blue'), __viewContext.badge(c.known ? '在线' : '失联', c.known ? 'green' : 'amber'), r.canWrite ? '可提交已授权变更' : '仅观察', r.type === 'swarm' ? c.known ? __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__viewContext.badge(c.quorum ? '达到法定多数' : '未达到法定多数', c.quorum ? 'green' : 'red'), ' · '), c.reachable), '/'), c.managers.length) : '状态未知' : '不适用 · 不是 Swarm', c.known ? __nscVueAdd(c.services.filter(s => __viewContext.serviceState(s).key === 'issue').length, ' 个需要处理') : '未知；最后快照不代表当前状态', c.known ? r.nodes.filter(n => n.agent).length : '未知', r.nodes.length, r.gaps.length ? __nscVueAdd(' · ', __viewContext.TB('查看采集缺口', 'x-runtime-tab', 'right', 'data-id="events"')) : '', c.known ? r.nodes.filter(n => n.cpu !== null).length : '未知', r.nodes.length, r.observed, __viewContext.cardHead('运行在这里的应用', '应用可能属于不同项目与环境；切换筛选不改变部署位置。', '', 'box'), __viewContext.appTable(c.apps)]);
  if (__viewContext.x.runtimeTab === 'nodes') body = __nscVueView(__viewContext.templates[321], [__viewContext.cardHead('节点', '角色 / 在线状态 / 调度可用性 / 管理节点状态分别展示。', '', 'server'), __nscVueJoin(r.nodes.map(n => __nscVueView(__viewContext.templates[320], [n.id, __viewContext.esc(n.name), __viewContext.esc(n.id), n.role === 'manager' ? '管理节点' : n.role === 'host' ? '单机主机' : '工作节点', n.manager || '—', __viewContext.badge(c.known ? n.status : '未知', c.known && n.status === 'Ready' ? 'green' : 'amber'), n.availability, n.availability === 'Drain' ? '不接收新任务' : n.availability === 'Pause' ? '暂停新任务调度' : '允许调度', c.known ? __viewContext.db.instances.filter(t => t.node === n.id && t.current && t.state === 'RUNNING').length : '未知', c.known && n.cpu !== null ? __nscVueAdd(n.cpu, '%') : '— 未采集', c.known && n.used !== null ? __nscVueAdd(__nscVueAdd(__nscVueAdd(n.used, ' / '), n.total), ' GiB') : '— 未采集', __viewContext.badge(c.known && n.agent ? '已采集' : '采集中断', c.known && n.agent ? 'green' : 'amber'), n.sample])), '')]);
  if (__viewContext.x.runtimeTab === 'services') body = __nscVueView(__viewContext.templates[322], [__viewContext.cardHead('此运行位置的全部可见服务', '包含其他项目及未归属项目的只读服务。', '', 'layers'), __viewContext.serviceTable(c.services)]);
  if (__viewContext.x.runtimeTab === 'events') body = __nscVueView(__viewContext.templates[324], [__nscVueJoin(r.gaps.map(g => __nscVueView(__viewContext.templates[323], [__viewContext.callout(__nscVueInterpolate(["", " \xB7 ", "\u2014", "\uFF1A", " \u5386\u53F2\u7F3A\u53E3\u4F1A\u7EE7\u7EED\u4FDD\u7559\uFF0C\u4E0D\u5728\u91CD\u8FDE\u540E\u6807\u6210\u5B8C\u6574\u3002"], [__viewContext.esc(__viewContext.NODE(g.node)?.name || g.node), g.from, g.to, __viewContext.esc(g.reason)]), 'amber')])), ''), __viewContext.eventsTable(__viewContext.db.runtimeEvents.filter(e => e.runtime === r.id))]);
  return __nscVueView(__viewContext.templates[325], [__viewContext.breadcrumb([{
    label: '集群与主机',
    action: 'x-nav',
    attrs: 'data-page="runtimes"'
  }, {
    label: r.name
  }]), __viewContext.xHead(r.name, __nscVueInterpolate(["", " \xB7 ", " \xB7 \u5171\u4EAB\u57FA\u7840\u8BBE\u65BD"], [r.type === 'swarm' ? 'Docker Swarm' : 'Docker Engine · 单机', r.engineId]), __nscVueAdd(__viewContext.B('连接信息', 'x-runtime-info', 'info', '', __nscVueInterpolate(["data-id=\"", "\""], [r.id])), __viewContext.B(c.known ? '模拟连接中断' : '模拟连接恢复', 'x-runtime-toggle', 'refresh', '', __nscVueInterpolate(["data-id=\"", "\""], [r.id]))), 'RUNTIME OVERVIEW'), __viewContext.I('globe', 'sm'), r.observed, c.known ? '' : '（已过期）', !c.known ? __viewContext.callout('当前状态未知，以下仅保留最后已知记录。控制操作已阻断，不能把连接恢复等同于期间历史已经补齐。', 'amber') : '', __viewContext.tabs([['overview', '概览', 'grid'], ['nodes', '节点', 'server'], ['services', '服务与应用', 'box'], ['events', '事件与采集', 'activity']], __viewContext.x.runtimeTab, 'x-runtime-tab'), body, __viewContext.xFooter()]);
}
export function eventLabel_60(__viewContext, e) {
  return {
    fault: '意外退出 / 自动恢复',
    planned: '计划内变更',
    gap: '采集缺口',
    completed: '正常完成',
    operation: '人工操作',
    restart: '容器重启'
  }[e.category] || '运行事件';
}
export function eventsTable_61(__viewContext, es) {
  return __nscVueView(__viewContext.templates[328], [__nscVueJoin(es.slice().sort((a, b) => b.time.localeCompare(a.time)).map(e => __nscVueView(__viewContext.templates[326], [__viewContext.esc(e.time), __viewContext.esc(e.id), e.id, __viewContext.esc(e.title), __viewContext.eventLabel(e), __viewContext.esc(e.actor), __viewContext.esc(__viewContext.projectName(e.project)), __viewContext.esc(__viewContext.envName(e.env)), e.service ? __viewContext.esc(__viewContext.ST(e.service)?.name || e.service) : __viewContext.esc(__viewContext.RT(e.runtime)?.name), __viewContext.esc(__viewContext.NODE(e.node)?.name || '—'), __viewContext.badge(e.state === 'recovered' ? '已恢复 · 历史保留' : e.state === 'open' ? '待处理' : '已记录', e.state === 'recovered' ? 'green' : e.state === 'open' ? 'amber' : 'blue'), e.confidence === 'confirmed' ? '有明确演示证据' : e.confidence === 'inferred' ? '推测关联 · 待核实' : '已观察 · 不推断未知原因', __viewContext.TB('查看链路', 'x-event', 'right', __nscVueInterpolate(["data-id=\"", "\""], [e.id]))])), '') || __nscVueView(__viewContext.templates[327], [])]);
}
export function runtimeEventsPage_62(__viewContext) {
  const es = __viewContext.db.runtimeEvents.filter(e => __viewContext.x.eventFilter === 'all' || e.category === __viewContext.x.eventFilter);
  return __nscVueView(__viewContext.templates[330], [__viewContext.xCrumb('运行记录', true), __viewContext.xHead('运行记录', '查清谁做了什么、哪个实例退出、为什么被替换，以及恢复是否有证据。', __nscVueAdd(__viewContext.B('导出记录', 'x-export-events', 'download'), __viewContext.B('演示故障链路', 'x-event', 'activity', 'primary', 'data-id="INC-018"')), 'OPERATIONS TIMELINE'), __viewContext.I('globe', 'sm'), __nscVueJoin([['all', '全部'], ['fault', '意外退出 / 恢复'], ['planned', '计划内变更'], ['restart', '容器重启'], ['completed', '正常完成'], ['gap', '采集缺口']].map(([k, n]) => __nscVueView(__viewContext.templates[329], [__viewContext.x.eventFilter === k ? 'active' : '', k, n])), ''), __viewContext.eventsTable(es), __viewContext.xFooter()]);
}
export function deployState_63(__viewContext, o) {
  return {
    success: ['已核实', 'green'],
    partial: ['部分失败', 'amber'],
    unknown: ['结果未知', 'amber'],
    running: ['部署中', 'blue'],
    failed: ['失败', 'red']
  }[o.state] || ['等待', ''];
}
export function deployTable_64(__viewContext, os) {
  return __nscVueView(__viewContext.templates[338], [__nscVueJoin(os.slice().sort((a, b) => b.time.localeCompare(a.time)).map(o => __nscVueView(__viewContext.templates[336], [o.id, __viewContext.esc(o.action), __viewContext.esc(o.id), __viewContext.esc(__viewContext.APP(o.app)?.name || o.snapshot?.name), __viewContext.scopeName(o.project, o.env), o.version, o.targets.length, __viewContext.esc(__viewContext.RT(o.runtime)?.name), __viewContext.esc(o.actor), o.time, __viewContext.badge(...__viewContext.deployState(o)), o.attempts.length, __viewContext.TB('详情', 'x-deployment', 'right', __nscVueInterpolate(["data-id=\"", "\""], [o.id]))])), '') || __nscVueView(__viewContext.templates[337], [])]);
}
export function specText_66(__viewContext, spec) {
  return __nscVueAdd('# 应用定义快照（原型展示，不是可直接投产的 Compose 编译器）\n', JSON.stringify(spec, null, 2));
}
export function templateInfo_67(__viewContext, id) {
  const t = __viewContext.tpl(id);
  __viewContext.showModal({
    kind: 'x-template-info',
    size: 'small',
    title: t.name,
    sub: __nscVueAdd(t.version, ' · 演示目录'),
    body: __nscVueView(__viewContext.templates[377], [__viewContext.esc(t.desc), __nscVueJoin(t.services.map(s => __viewContext.esc(s.name)), ' + '), __nscVueJoin(t.modes.map(m => __viewContext.MODE[m]), __nscVueView(__viewContext.templates[376], [])), __viewContext.esc(t.notes)]),
    footer: __nscVueAdd(__viewContext.B('关闭', 'close'), __viewContext.B('使用这个模板', 'x-template', 'plus', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [id])))
  });
}
export function runtimeInfo_68(__viewContext, id) {
  const r = __viewContext.RT(id);
  if (!r) return;
  __viewContext.showModal({
    kind: 'x-runtime-info',
    size: 'small',
    title: '连接与授权',
    sub: r.name,
    body: __nscVueView(__viewContext.templates[379], [r.engineId, r.type === 'swarm' ? 'Docker Swarm · Manager' : 'Docker Engine · 单机', r.canWrite ? '允许已确认的应用部署' : '只读观察', __nscVueJoin(r.allowedEnvs.map(id => __viewContext.scopeName(__viewContext.E(id)?.project, id)), __nscVueView(__viewContext.templates[378], [])), r.historySince, __viewContext.callout('没有实际 SSH 或 Docker Socket 连接。请勿在原型中输入密码、私钥或生产 API 地址。', 'amber')]),
    footer: __viewContext.B('关闭', 'close')
  });
}
