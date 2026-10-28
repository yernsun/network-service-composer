import { view as __nscVueView, add as __nscVueAdd, join as __nscVueJoin, interpolateView as __nscVueInterpolate } from '../../components/view';
export function draftBadge_0(__viewContext) {
  return __viewContext.dirty() ? __viewContext.badge('未保存修改', 'amber') : __viewContext.D().draft && __viewContext.changed() ? __viewContext.badge('草稿 · 未发布', 'blue') : __viewContext.badge('无待发布变更');
}
export function domainStatus_1(__viewContext, d = __viewContext.D()) {
  const s = __viewContext.syncInfo(d);
  if (d.rev === 0) return __viewContext.badge('尚未发布');
  if (d.drift) return __viewContext.badge('配置漂移', 'amber', true);
  if (s.unknown) return __viewContext.badge('生效状态未知', 'amber', true);
  if (s.mixed) return __viewContext.badge('部分未生效', 'amber', true);
  return __viewContext.badge(__nscVueAdd('已确认 · v', d.rev), 'green', true);
}
export function heroActions_2(__viewContext) {
  return __nscVueAdd(__nscVueAdd(__nscVueAdd(__viewContext.B('保存草稿', 'save', 'save', '', __viewContext.dirty() ? '' : 'disabled title="没有未保存修改"'), __viewContext.B('校验', 'validate', 'checkcircle')), __viewContext.B('查看影响', 'impact', 'compare')), __viewContext.B('发布', 'publish', 'send', 'primary', !__viewContext.changed() || !!__viewContext.deploymentTimer ? 'disabled title="无变更时不生成发布"' : ''));
}
export function domainPage_4(__viewContext) {
  const d = __viewContext.D();
  return __nscVueView(__viewContext.templates[65], [__viewContext.ENV[d.env], __viewContext.esc(d.name), __viewContext.I('globe'), __viewContext.esc(d.name), __viewContext.domainStatus(), __viewContext.draftBadge(), __viewContext.db.view === 'cloud' ? '将域名、请求规则与逻辑服务组合为可发布的入口。' : '核实配置、Gateway 与真实生效状态；从问题定位到受控恢复。', __viewContext.heroActions(), __viewContext.I('layers', 'sm'), __viewContext.ENV[d.env], __viewContext.I('server', 'sm'), __viewContext.esc(__nscVueJoin(d.gateways.map(g => __viewContext.G(g).name), ' / ')), __viewContext.I('lock', 'sm'), __viewContext.work.mode === 'native' ? '文件编辑 · 图形编辑只读' : '表单配置 + Nginx 扩展', __viewContext.I('clock', 'sm'), d.draft?.base ?? d.rev, __viewContext.TB('域名设置', 'domain-settings', 'settings'), __nscVueJoin([['overview', __viewContext.db.view === 'cloud' ? '编排总览' : '运行总览', 'globe'], ['routes', '路由规则', 'route'], ['config', 'Nginx 配置', 'code'], ['tls', 'TLS / 证书', 'shield'], ['history', '变更记录', 'history']].map(([id, t, ic]) => __nscVueView(__viewContext.templates[64], [__viewContext.ui.tab === id ? 'active' : '', __viewContext.ui.tab === id, id, __viewContext.I(ic, 'sm'), t, id === 'routes' ? __nscVueView(__viewContext.templates[63], [__viewContext.work.routes.length]) : ''])), ''), __viewContext.stale() ? __nscVueView(__viewContext.templates[1], [__viewContext.callout(__nscVueAdd('草稿基线已经过期，禁止静默覆盖。请查看差异并显式以当前期望版本重新建立草稿。 ', __viewContext.TB('处理版本冲突', 'conflict')), 'red')]) : '', __viewContext.ui.tab === 'overview' ? __viewContext.db.view === 'cloud' ? __viewContext.cloudOverview() : __viewContext.opsOverview() : __viewContext.ui.tab === 'routes' ? __viewContext.routesPage() : __viewContext.ui.tab === 'config' ? __viewContext.configPage() : __viewContext.ui.tab === 'tls' ? __viewContext.tlsPage() : __viewContext.historyPage(), __viewContext.I('lock', 'sm'), __viewContext.dirty() ? '有未保存修改' : d.draft ? '草稿已保存至本机' : '与期望版本一致', __viewContext.storageAvailable ? '本机存储可用' : '仅内存模式']);
}
export function metric_5(__viewContext, n, label, icon, info = '') {
  return __nscVueView(__viewContext.templates[67], [__viewContext.I(icon), n, label, info ? __nscVueView(__viewContext.templates[66], [info]) : '']);
}
export function cloudOverview_6(__viewContext) {
  const d = __viewContext.D(),
    sync = __viewContext.syncInfo(),
    rs = __viewContext.work.routes.filter(r => r.enabled),
    svc = [...new Set(rs.filter(r => r.kind === 'proxy').map(r => r.service))],
    sel = rs.find(r => r.id === __viewContext.ui.selectedRoute && r.kind === 'proxy') || rs.find(r => r.kind === 'proxy');
  const changes = __viewContext.summarizeChanges();
  return __nscVueView(__viewContext.templates[70], [__viewContext.metric(rs.length, '启用的 Route', 'route', '按请求规则匹配'), __viewContext.metric(svc.length, '关联的 Service', 'box', '逻辑服务，不是容器'), __viewContext.metric(__nscVueView(__viewContext.templates[68], [sync.known, sync.total]), '已确认的 Gateway', 'server', '逐节点确认 · 演示'), __viewContext.metric(changes.length, '待发布的配置变更', 'edit', '保存与发布相互独立'), __viewContext.cardHead('请求如何到达服务', '同一 Domain 的入口、路由和后端依赖。点击任一资源继续编辑。', __viewContext.TB('模拟请求匹配', 'diagnose', 'play'), 'route'), __viewContext.work.mode === 'native' ? __viewContext.callout('该 Domain 已改为直接编辑配置文件。以下保留上次托管关系，不作为手动配置文件的完整解析结果。', 'amber') : '', __viewContext.I('globe'), __viewContext.esc(d.name), __viewContext.work.tls ? 'HTTPS :443 · HTTP :80' : 'HTTP :80', __viewContext.I('arrow'), d.gateways[0], __viewContext.I('server'), d.env === 'prod' ? 'production-edge' : __nscVueAdd(__viewContext.ENV[d.env], ' edge'), sync.known, sync.total, __viewContext.I('arrow'), __viewContext.I('route'), __viewContext.esc(sel?.path || '/'), __nscVueJoin(rs.slice(0, 3).map(r => __nscVueView(__viewContext.templates[69], [__viewContext.esc(r.path)])), ''), __viewContext.I('arrow'), sel?.service || '', __viewContext.I('box'), __viewContext.esc(sel?.service || '无代理服务'), __viewContext.S(sel?.service)?.port || '—', __viewContext.I('arrow'), sel?.service || '', __viewContext.I('layers'), __viewContext.ready(sel?.service).length, __viewContext.I('info', 'sm'), __viewContext.cardHead('路由规则', __viewContext.work.mode === 'native' ? '接管前的结构化关系，只读保留' : '从请求条件到处理动作；访问控制是策略，不是路由类型。', __viewContext.TB('管理路由', 'tab', 'right', 'data-tab="routes"'), 'route'), __viewContext.routeTable(rs.slice(0, 5), true), rs.some(r => r.path === '/' && r.match !== 'exact') ? '已配置 / 兜底路由，其他路径由它接管。' : '没有兜底路由时，不匹配请求返回 404。', __viewContext.TB('添加 Route', 'route-add', 'plus'), __viewContext.cardHead('关联资源', '入口资源归 NSC 管理；不接管外部应用的生命周期。', '', 'link'), __viewContext.I('shield'), __viewContext.esc(d.name), __viewContext.work.tls ? '绑定 Domain · 证书文件由 Gateway 加载' : 'HTTPS 未启用', __viewContext.badge(__viewContext.work.tls ? '有效 · 演示' : '未引用', __viewContext.work.tls ? 'green' : ''), __viewContext.TB('查看', 'tab', 'right', 'data-tab="tls"'), __viewContext.I('code'), __viewContext.TB('打开配置', 'tab', 'right', 'data-tab="config"'), __viewContext.cardHead('当前草稿', __nscVueAdd(__nscVueAdd('基于 v', d.draft?.base ?? d.rev), ' · 与两个视角共享'), __viewContext.draftBadge(), 'edit'), __viewContext.changesHTML(changes), __viewContext.callout('保存只保留草稿。通过校验并确认影响后，才进入发布。', 'blue'), __viewContext.TB('查看完整配置差异', 'impact', 'compare'), __viewContext.cardHead('变更闭环', '内容冻结后，目标 Gateway 与 Revision 不再改变。', '', 'send'), __viewContext.step('1', '编辑', 'done'), __viewContext.step('2', '校验', __viewContext.ui.validation ? 'done' : 'now'), __viewContext.step('3', '确认影响', ''), __viewContext.step('4', '发布核实', ''), __viewContext.B('校验当前草稿', 'validate', 'checkcircle', 'w100')]);
}
export function step_7(__viewContext, n, t, state) {
  return __nscVueView(__viewContext.templates[71], [state, state === 'done' ? __viewContext.I('check', 'sm') : n, t]);
}
export function routeTable_8(__viewContext, routes, compact = false, domain = null) {
  return __nscVueView(__viewContext.templates[81], [compact ? '' : __nscVueView(__viewContext.templates[72], []), compact ? '状态' : '请求路径', routes.length ? __nscVueJoin(routes.map(r => __nscVueView(__viewContext.templates[79], [r.id === __viewContext.ui.selectedRoute ? 'selected' : '', r.kind === 'return' ? 'green' : r.path === '/assets/' ? 'purple' : '', __viewContext.I(r.kind === 'proxy' ? 'route' : r.kind === 'return' ? 'checkcircle' : 'external'), __viewContext.esc(r.id), domain ? __nscVueInterpolate(["data-domain=\"", "\""], [domain]) : '', __viewContext.esc(r.path), __viewContext.esc(__viewContext.MATCH[r.match]), compact ? '' : __nscVueView(__viewContext.templates[73], [__viewContext.badge(__viewContext.KINDS[r.kind], r.kind === 'proxy' ? 'blue' : '')]), r.kind === 'proxy' ? __nscVueView(__viewContext.templates[74], [__viewContext.esc(r.service), __viewContext.I('box', 'sm'), __viewContext.esc(r.service), __viewContext.ready(r.service).length, r.timeout]) : __nscVueView(__viewContext.templates[75], [r.code, __viewContext.esc(r.value.slice(0, 22))]), compact ? __viewContext.badge(r.enabled ? '已启用' : '已停用', r.enabled ? 'green' : '', true) : r.kind === 'proxy' ? __nscVueView(__viewContext.templates[76], [r.strip ? '移除匹配前缀' : '保留完整 URI']) : __nscVueView(__viewContext.templates[77], []), __viewContext.esc(r.id), domain ? __nscVueInterpolate(["data-domain=\"", "\""], [domain]) : '', __viewContext.esc(r.path), __viewContext.I('edit', 'sm'), !compact ? __nscVueView(__viewContext.templates[78], [__viewContext.esc(r.id), __viewContext.esc(r.path), __viewContext.I('code', 'sm')]) : ''])), '') : __nscVueView(__viewContext.templates[80], [])]);
}
export function changesHTML_9(__viewContext, changes, limit = 4) {
  return changes.length ? __nscVueAdd(__nscVueJoin(changes.slice(0, limit).map(c => __nscVueView(__viewContext.templates[82], [__viewContext.I('edit'), __viewContext.esc(c.title), __viewContext.esc(c.detail), __viewContext.esc(c.old), __viewContext.I('arrow', 'sm'), __viewContext.esc(c.new)])), ''), changes.length > limit ? __nscVueView(__viewContext.templates[83], [changes.length - limit]) : '') : __nscVueView(__viewContext.templates[84], [__viewContext.I('checkcircle')]);
}
export function opsOverview_10(__viewContext) {
  const d = __viewContext.D(),
    s = __viewContext.syncInfo();
  return __nscVueView(__viewContext.templates[86], [__viewContext.cardHead('入口健康总览', '以下均为演示观测，生产环境需要记录检查时间、来源与证据。', __viewContext.B('切换故障场景', 'scenarios', 'settings', 'sm'), 'activity'), __viewContext.health('DNS 解析', '匹配入口地址', '203.0.113.10 · 示例地址', 'globe'), __viewContext.health('TLS / 证书', d.active.tls ? '资产有效 · 演示' : '未启用', d.active.tls ? __nscVueAdd('证书到期 ', d.certExpiry) : '当前期望版本未使用 HTTPS', 'shield', d.active.tls ? '' : 'gray'), __viewContext.health('Gateway 同步', __nscVueInterpolate(["", " / ", " \u5DF2\u786E\u8BA4"], [s.known, s.total]), s.unknown ? '有节点失联：生效版本未知' : s.mixed ? '期望与已确认版本不一致' : __nscVueAdd('期望版本 v', d.rev), 'server', s.known === s.total ? '' : 'amber'), __viewContext.health('配置一致性', d.drift ? '发现外部漂移' : '与期望一致', d.drift ? '不能直接覆盖外部修改' : '演示观测 · 非 nginx -t 结果', 'code', d.drift ? 'amber' : ''), __viewContext.health('上游就绪', __nscVueAdd(__viewContext.db.services.filter(x => x.env === d.env).reduce((n, x) => __nscVueAdd(n, __viewContext.ready(x.id).length), 0), ' Ready'), '仅展示发现数据 · 未发起探测', 'box'), __viewContext.cardHead('Gateway 与逐节点生效', '期望版本、已确认版本及启动文件状态分别展示。', __viewContext.TB('查看全部网关', 'nav', 'right', 'data-page="gateways"'), 'server'), __viewContext.gatewayTable(d), __viewContext.cardHead('配置差异与外部漂移', '生产目标、工作草稿和外部文件分别保存。', __viewContext.TB('进入配置工作台', 'tab', 'right', 'data-tab="config"'), 'compare'), d.drift ? __viewContext.callout(__nscVueView(__viewContext.templates[85], [__viewContext.esc(__viewContext.G(d.drift.gateway).name), __viewContext.TB('比较并处理', 'drift', 'right')]), 'amber', 'warning') : __viewContext.callout('没有发现外部漂移（演示）。当前草稿的修改尚未进入运行配置。', 'neutral'), __viewContext.diffHTML(__viewContext.renderConfig(d.active), __viewContext.renderConfig(__viewContext.work), true), __viewContext.cardHead('请求匹配诊断', '配置模拟，不向任何 URL 发起真实请求。', '', 'route'), __viewContext.diagnosticHTML(), __viewContext.cardHead('最近事件', '每次编辑、发布与恢复都有记录。', __viewContext.TB('全部记录', 'nav', 'right', 'data-page="events"'), 'history'), __viewContext.timeline(__viewContext.db.events.filter(e => e.domain === __viewContext.ui.domain).slice(0, 4))]);
}
export function health_11(__viewContext, title, value, sub, ic, state = '') {
  return __nscVueView(__viewContext.templates[87], [state, __viewContext.I(state === 'amber' ? 'warning' : state === 'gray' ? 'info' : 'check'), title, state === 'amber' ? 'amber' : '', value, sub]);
}
export function gatewayTable_12(__viewContext, d) {
  return __nscVueView(__viewContext.templates[89], [__nscVueJoin(d.gateways.map(id => {
    const g = __viewContext.G(id),
      off = __viewContext.offline(id),
      dr = d.drift?.gateway === id,
      mix = d.loaded[id] !== d.rev;
    return __nscVueView(__viewContext.templates[88], [id, off ? 'gray' : '', __viewContext.esc(g.name), g.ip, off ? '失联演示' : '在线演示', d.rev, off ? __viewContext.badge('未知', 'amber') : mix ? __viewContext.badge(d.loaded[id] ? __nscVueAdd(__nscVueAdd('v', d.loaded[id]), ' · 旧版本') : '未确认', 'amber') : __viewContext.badge(__nscVueAdd('v', d.rev), 'green'), off ? __viewContext.badge('未知') : dr ? __viewContext.badge('存在漂移', 'amber') : __viewContext.badge(mix ? '保留旧版' : '与目标一致', mix ? '' : 'green'), __viewContext.TB('详情', 'gateway-detail', 'right', __nscVueInterpolate(["data-id=\"", "\""], [id]))]);
  }), '')]);
}
export function timeline_13(__viewContext, events) {
  return events.length ? __nscVueView(__viewContext.templates[91], [__nscVueJoin(events.map(e => __nscVueView(__viewContext.templates[90], [__viewContext.I(e.type === 'warning' ? 'warning' : e.type === 'edit' ? 'edit' : e.type === 'send' ? 'send' : 'check'), __viewContext.esc(e.title), __viewContext.esc(e.detail), __viewContext.esc(e.time)])), '')]) : __nscVueView(__viewContext.templates[92], []);
}
export function diagnosticHTML_14(__viewContext) {
  const result = __viewContext.ui.diag;
  return __nscVueView(__viewContext.templates[96], [__viewContext.ui.diagSource === 'draft' ? 'selected' : '', __viewContext.ui.diagSource === 'target' ? 'selected' : '', __viewContext.D().rev, __nscVueJoin(__viewContext.D().gateways.map(id => __nscVueView(__viewContext.templates[93], [id, __viewContext.ui.diagSource === id ? 'selected' : '', __viewContext.G(id).name])), ''), __viewContext.esc(__viewContext.ui.diagUrl), __viewContext.B('模拟匹配', 'run-diag', 'play', 'primary sm'), result ? __nscVueView(__viewContext.templates[95], [result.error ? 'background:#fff8ed;border-color:#efdfbd' : '', result.error ? 'amber' : 'green', __viewContext.I(result.error ? 'warning' : 'checkcircle', 'sm'), __viewContext.esc(result.title), __viewContext.esc(result.note), result.route ? __nscVueView(__viewContext.templates[94], [__viewContext.esc(result.route.path), __viewContext.KINDS[result.route.kind], __viewContext.esc(result.route.service || result.route.code), __viewContext.esc(result.uri)]) : '']) : __viewContext.callout('支持本原型的精确匹配、前缀匹配与 ^~。原生、正则、复杂 rewrite 不伪装成可推导结果。', 'neutral')]);
}
export function routesPage_15(__viewContext) {
  return __nscVueView(__viewContext.templates[97], [__viewContext.I('info', 'sm'), __viewContext.work.mode === 'native' ? __nscVueView(__viewContext.templates[2], [__viewContext.callout('当前为文件编辑：下面展示接管前的结构化关系，编辑已锁定。请在配置工作台编辑配置文本。', 'amber')]) : '', __viewContext.I('search'), __viewContext.esc(__viewContext.ui.search), __viewContext.B('模拟请求', 'diagnose', 'play'), __viewContext.B('添加 Route', 'route-add', 'plus', 'primary', __viewContext.work.mode === 'native' ? 'disabled' : ''), __viewContext.routeTable(__viewContext.work.routes.filter(r => __nscVueAdd(__nscVueAdd(r.path, ' '), r.service).toLowerCase().includes(__viewContext.ui.search.toLowerCase()))), __viewContext.work.routes.length, __viewContext.cardHead('路径转发是显式选项', '避免只看 proxy_pass 而无法理解最终 URI。', '', 'route'), __viewContext.cardHead('配置归属与安全边界', '每段配置都有唯一写入入口。', '', 'lock'), __viewContext.I('check'), __viewContext.I('check'), __viewContext.I('check')]);
}
export function codeHTML_16(__viewContext, text, highlight = '') {
  return __nscVueView(__viewContext.templates[99], [__nscVueJoin(text.split('\n').map((l, i) => __nscVueView(__viewContext.templates[98], [highlight && l.includes(highlight) ? 'highlight' : '', __nscVueAdd(i, 1), l.trim().startsWith('#') ? 'code-comment' : '', __viewContext.highlightLine(l)])), '')]);
}
export function configPage_17(__viewContext) {
  let file = __viewContext.ui.file;
  if (__viewContext.work.mode === 'native' && !['native', 'generated', 'main'].includes(file)) file = __viewContext.ui.file = 'native';
  const r = __viewContext.work.routes.find(r => r.id === __viewContext.ui.selectedRoute) || __viewContext.work.routes[0];
  let text = '',
    editable = false,
    filename = __nscVueAdd(__nscVueAdd('domains/', __viewContext.D().name), '.conf'),
    title = '结构化生成配置',
    desc = '由 Domain、Route 和服务发现共同生成。此区域只读；修改应回到对应资源。';
  if (file === 'main') {
    text = '# Gateway bootstrap — read-only demo\nworker_processes auto;\nevents {\n    worker_connections 1024;\n}\nhttp {\n    include /etc/nginx/mime.types;\n    # Bundle all domains and dependencies in a Gateway snapshot.\n    include /etc/nginx/nsc/domains/*.conf;\n}\n';
    filename = 'nginx.conf';
    title = 'Gateway 主配置';
    desc = '共享上下文影响该 Gateway 的所有 Domain，本原型只读展示，不把它归属于某个 Domain。';
  } else if (file === 'server') {
    text = __viewContext.work.serverSnippet;
    editable = true;
    filename = __nscVueAdd(__nscVueAdd('extensions/', __viewContext.D().name), '/server.conf');
    title = 'Domain 自定义指令';
    desc = '插入 server 上下文。监听、域名、TLS、location 等托管字段不能在此重新定义。';
  } else if (file === 'route' && r) {
    text = r.snippet;
    editable = true;
    filename = __nscVueAdd(__nscVueAdd('extensions/', r.id), '.conf');
    title = 'Route 自定义指令';
    desc = '插入所选 location。扩展不创建第二份 Route；目标与代理超时仍回到 Route 编辑。';
  } else if (file === 'native' && __viewContext.work.mode === 'native') {
    text = __viewContext.work.native;
    editable = true;
    filename = __nscVueAdd(__nscVueAdd('manual/', __viewContext.D().name), '.conf');
    title = '直接编辑当前域名配置';
    desc = '这里的文件内容用于下一次发布。路由与 TLS 表单暂停修改；返回表单前会保留文件副本，并展示将被替换的内容。';
  } else {
    text = __viewContext.renderConfig(__viewContext.work);
  }
  return __nscVueView(__viewContext.templates[106], [__viewContext.work.mode === 'native' ? 'amber' : 'green', __viewContext.I(__viewContext.work.mode === 'native' ? 'terminal' : 'lock', 'sm'), __viewContext.work.mode === 'native' ? '文件编辑' : '表单配置', __viewContext.ui.configView === 'semantic' ? 'active' : '', __viewContext.ui.configView === 'code' ? 'active' : '', __viewContext.ui.configView === 'semantic' ? __viewContext.semanticConfig() : __nscVueView(__viewContext.templates[105], [file === 'main' ? 'active' : '', __viewContext.I('file'), __viewContext.I('lock', 'sm'), file === 'generated' ? 'active' : '', __viewContext.I('code'), __viewContext.I('lock', 'sm'), __viewContext.work.mode === 'managed' ? __nscVueView(__viewContext.templates[100], [file === 'server' ? 'active' : '', __viewContext.I('edit'), file === 'route' ? 'active' : '', __viewContext.I('route')]) : __nscVueView(__viewContext.templates[101], [file === 'native' ? 'active' : '', __viewContext.I('terminal')]), __viewContext.work.mode === 'native' ? 'managed-mode' : 'native-mode', __viewContext.I('settings'), __viewContext.work.mode === 'native' ? '改用表单配置…' : '改为直接编辑配置文件', __viewContext.I('info', 'sm'), __viewContext.I('file', 'sm'), __viewContext.esc(filename), __viewContext.badge(editable ? '可编辑' : '只读', editable ? 'blue' : ''), editable ? __viewContext.B('格式化', 'format-code', '', 'sm ghost') : '', __viewContext.I('copy', 'sm'), __viewContext.I('download', 'sm'), file === 'route' && r ? __nscVueView(__viewContext.templates[103], [__nscVueJoin(__viewContext.work.routes.map(x => __nscVueView(__viewContext.templates[102], [x.id, x.id === r.id ? 'selected' : '', __viewContext.esc(x.path), __viewContext.esc(x.id)])), '')]) : '', editable ? __nscVueView(__viewContext.templates[104], [__nscVueJoin(text.split('\n').map((_, i) => __nscVueAdd(i, 1)), '\n'), file, title, __viewContext.esc(text)]) : __viewContext.codeHTML(text, __viewContext.ui.selectedRoute), editable ? 'Tab 缩进 · Ctrl / ⌘ S 保存草稿' : '自动生成 · 请通过表单或自定义指令修改', text.split('\n').length, title, desc, __viewContext.esc(__viewContext.D().name), file === 'route' ? __nscVueAdd('location ', __viewContext.esc(r?.path || '/')) : file === 'main' ? 'main / http' : 'server / http fragment', __viewContext.work.mode === 'native' ? '手动编辑' : file === 'server' || file === 'route' ? '自定义指令' : '由表单生成', __viewContext.work.mode === 'native' ? '不会将不支持的原生指令改写成图形表单。' : '检测托管指令重复写入、路径冲突、无 Ready 上游，以及基础格式问题。', __viewContext.B('校验草稿', 'validate', 'checkcircle', 'sm w100'), __viewContext.callout('浏览器不能执行 nginx -t。这里的本地检查不等同于真实 NGINX 校验。', 'amber')]), __viewContext.B('导出 .conf', 'export', 'download', 'sm'), __viewContext.B('查看差异', 'impact', 'compare', 'sm')]);
}
export function semanticConfig_18(__viewContext) {
  if (__viewContext.work.mode === 'native') return __nscVueView(__viewContext.templates[107], [__viewContext.callout('文件编辑已启用。图形表单不会覆盖配置文本；返回托管前须显式确认重建。', 'amber'), __viewContext.B('编辑配置文件', 'file', '', 'primary', 'data-file="native"'), __viewContext.B('改用表单配置…', 'managed-mode')]);
  return __nscVueView(__viewContext.templates[108], [__viewContext.I('globe'), __viewContext.esc(__viewContext.D().name), __viewContext.work.tls ? 'on' : '', __viewContext.work.tls, __viewContext.work.redirect ? 'on' : '', !__viewContext.work.tls ? 'disabled' : '', __viewContext.work.redirect, __viewContext.I('settings'), __viewContext.esc(__viewContext.work.body), __viewContext.B('编辑 /api/ 路由', 'route-edit', 'route', 'sm', __nscVueAdd(__nscVueAdd('data-id="', __viewContext.work.routes.find(r => r.path === '/api/')?.id || __viewContext.work.routes[0]?.id || ''), '')), __viewContext.I('edit'), __viewContext.B('补充域名指令', 'file', 'code', 'sm', 'data-file="server"'), __viewContext.B('补充路由指令', 'file', 'route', 'sm', 'data-file="route"'), __viewContext.I('lock'), __viewContext.B('直接编辑配置文件…', 'native-mode', 'terminal', 'sm')]);
}
export function tlsPage_19(__viewContext) {
  const d = __viewContext.D();
  return __nscVueView(__viewContext.templates[110], [__viewContext.cardHead('Domain 的 TLS 身份', '证书绑定域名，而不是绑定某个后端 Service。', __viewContext.badge(__viewContext.work.tls ? '草稿已启用' : '草稿未启用', __viewContext.work.tls ? 'green' : ''), 'shield'), __viewContext.I('shield'), __viewContext.esc(d.name), __viewContext.esc(d.name), __viewContext.badge('示例资产', 'blue'), __viewContext.esc(d.name), d.certExpiry, __viewContext.esc(d.name), __viewContext.badge('不在浏览器存储或展示'), d.gateways.length, __viewContext.esc(d.name), __viewContext.work.tls ? 'on' : '', __viewContext.work.tls, __viewContext.work.mode === 'native' ? 'disabled' : '', __viewContext.work.redirect ? 'on' : '', __viewContext.work.redirect, !__viewContext.work.tls || __viewContext.work.mode === 'native' ? 'disabled' : '', __viewContext.callout('此原型没有 ACME、DNS Provider 或真实证书签发功能；“有效”仅指示例状态。', 'neutral'), __viewContext.cardHead('证书部署状态', '证书资产存在不代表节点已经加载。', '', 'server'), __nscVueJoin(d.gateways.map(id => __nscVueView(__viewContext.templates[109], [__viewContext.I('server'), __viewContext.G(id).name, __viewContext.offline(id) ? '未知' : __nscVueAdd('v', d.loaded[id]), __viewContext.badge(__viewContext.offline(id) ? '未知' : d.active.tls ? '已引用 · 演示' : '未引用', __viewContext.offline(id) ? 'amber' : d.active.tls ? 'green' : '')])), ''), __viewContext.cardHead('交互边界', '', '', 'info')]);
}
export function domainsPage_22(__viewContext, ds) {
  const filtered = ds.filter(d => d.name.toLowerCase().includes(__viewContext.ui.search.toLowerCase()) && (__viewContext.ui.filter === 'all' || __viewContext.ui.filter === 'draft' && d.draft || __viewContext.ui.filter === 'issue' && (d.drift || __viewContext.syncInfo(d).known !== d.gateways.length)));
  return __nscVueView(__viewContext.templates[117], [__viewContext.I('search'), __viewContext.esc(__viewContext.ui.search), __nscVueJoin([['all', __nscVueAdd('全部 ', ds.length)], ['draft', __nscVueAdd('有草稿 ', ds.filter(d => d.draft).length)], ['issue', __nscVueAdd('待处理 ', ds.filter(d => d.drift || __viewContext.syncInfo(d).known !== d.gateways.length).length)]].map(([id, t]) => __nscVueView(__viewContext.templates[114], [__viewContext.ui.filter === id ? 'active' : '', id, t])), ''), filtered.length ? __nscVueJoin(filtered.map(d => {
    const c = d.draft?.config || d.active,
      si = __viewContext.syncInfo(d);
    return __nscVueView(__viewContext.templates[115], [__viewContext.I('globe'), d.id, __viewContext.esc(d.name), __viewContext.esc(__viewContext.resourceScopeName(d)), c.mode === 'native' ? '文件编辑' : '表单配置', c.tls ? 'HTTPS :443' : 'HTTP :80', c.tls ? 'Domain certificate' : '未启用 TLS', c.routes.length, new Set(c.routes.filter(r => r.kind === 'proxy').map(r => r.service)).size, __viewContext.domainStatus(d), si.known, si.total, d.draft ? __viewContext.badge('待发布', 'blue') : __viewContext.badge('无变更'), __viewContext.TB('打开', 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\""], [d.id]))]);
  }), '') : __nscVueView(__viewContext.templates[116], [])]);
}
export function workspaceOverview_23(__viewContext, ds) {
  const svc = __viewContext.db.services.filter(s => s.env === __viewContext.db.env && s.project === __viewContext.db.project),
    gs = __viewContext.db.gateways.filter(g => __viewContext.gatewayAllowed(g, __viewContext.db.env));
  return __nscVueView(__viewContext.templates[119], [__viewContext.metric(ds.length, 'Domain', 'globe'), __viewContext.metric(ds.reduce((n, d) => __nscVueAdd(n, (d.draft?.config || d.active).routes.length), 0), 'Route', 'route'), __viewContext.metric(svc.length, 'Service', 'box'), __viewContext.metric(gs.length, 'Gateway', 'server'), __viewContext.cardHead('管理入口，而不是站点', 'Domain → Route → Service；Gateway 负责承载这些入口。', __viewContext.TB('全部 Domain', 'nav', 'right', 'data-page="domains"'), 'globe'), __nscVueJoin(ds.map(d => __nscVueView(__viewContext.templates[118], [__viewContext.I('globe'), d.id, __viewContext.esc(d.name), d.active.routes.length, d.gateways.length, __viewContext.domainStatus(d), __viewContext.TB('进入', 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\""], [d.id]))])), ''), __viewContext.cardHead('从这里体验', '两个视角切换不丢失当前草稿。', '', 'play'), __viewContext.B('打开交互指南', 'help', 'help', 'w100')]);
}
export function allRoutes_24(__viewContext, ds) {
  return __nscVueView(__viewContext.templates[121], [__viewContext.I('info', 'sm'), __nscVueJoin(ds.map(d => __nscVueView(__viewContext.templates[120], [__viewContext.cardHead(__nscVueAdd(__viewContext.esc(d.name), __viewContext.allEnvironmentView() ? __nscVueAdd(' · ', __viewContext.esc(__viewContext.resourceScopeName(d))) : ''), (d.draft?.config || d.active).mode === 'native' ? '文件编辑 · 下方只读保留历史关系' : 'Domain 的结构化匹配关系', __viewContext.TB('打开 Domain', 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\""], [d.id])), 'globe'), __viewContext.routeTable((d.draft?.config || d.active).routes, true, d.id)])), '')]);
}
export function servicesPage_25(__viewContext) {
  return __nscVueView(__viewContext.templates[123], [__viewContext.I('lock', 'sm'), __nscVueJoin(__viewContext.db.services.filter(s => s.env === __viewContext.db.env && s.project === __viewContext.db.project).map(s => {
    const refs = __viewContext.db.domains.filter(d => d.env === __viewContext.db.env && d.project === __viewContext.db.project && (d.draft?.config || d.active).routes.some(r => r.service === s.id));
    return __nscVueView(__viewContext.templates[122], [__viewContext.I('box', 'lg'), s.id, s.runtime, __viewContext.badge('已发现', 'green'), s.port, __viewContext.ready(s.id).length, s.endpoints.length, refs.length, __viewContext.badge('入口托管 / 应用只读'), __viewContext.B('Endpoint 与引用', 'service-detail', 'layers', 'sm', __nscVueAdd(__nscVueAdd('data-id="', s.id), '"'))]);
  }), '')]);
}
export function gatewaysPage_26(__viewContext) {
  return __nscVueView(__viewContext.templates[125], [__nscVueJoin(__viewContext.db.gateways.filter(g => __viewContext.gatewayAllowed(g, __viewContext.db.env)).map(g => {
    const ds = __viewContext.db.domains.filter(d => d.gateways.includes(g.id));
    return __nscVueView(__viewContext.templates[124], [__viewContext.I('server', 'lg'), g.name, g.ip, g.runtime, __viewContext.badge(__viewContext.offline(g.id) ? '未知' : '在线', __viewContext.offline(g.id) ? 'amber' : 'green', true), ds.length, ds.some(d => d.drift?.gateway === g.id) ? __viewContext.badge('有漂移', 'amber') : __viewContext.offline(g.id) ? __viewContext.badge('未知') : __viewContext.badge('演示一致', 'green'), __viewContext.B('逐 Domain 版本', 'gateway-detail', 'layers', 'sm', __nscVueAdd(__nscVueAdd('data-id="', g.id), '"'))]);
  }), ''), __viewContext.callout('示例以 Domain 内容切片展示版本。真实部署应以包含全部依赖的 Gateway Snapshot 作为原子发布单元。', 'neutral')]);
}
export function certificatesPage_27(__viewContext, ds) {
  return __nscVueView(__viewContext.templates[127], [__nscVueJoin(ds.map(d => __nscVueView(__viewContext.templates[126], [__viewContext.I('shield'), __viewContext.esc(d.name), __viewContext.esc(d.name), __viewContext.environmentBadge(d), d.certExpiry, d.gateways.length, __viewContext.TB('查看绑定', 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\" data-tab=\"tls\""], [d.id]))])), '')]);
}
export function configsPage_28(__viewContext, ds) {
  return __nscVueView(__viewContext.templates[129], [__viewContext.I('info', 'sm'), __nscVueJoin(ds.map(d => __nscVueView(__viewContext.templates[128], [__viewContext.I('code'), __viewContext.esc(d.name), __viewContext.allEnvironmentView() ? __nscVueAdd(__viewContext.esc(__viewContext.resourceScopeName(d)), ' · ') : '', __viewContext.badge((d.draft?.config || d.active).mode === 'native' ? '文件编辑' : '结构化 + 自定义指令', (d.draft?.config || d.active).mode === 'native' ? 'amber' : 'green'), d.draft?.base ?? d.rev, d.draft ? __viewContext.badge('有工作草稿', 'blue') : __viewContext.badge('无待发布变更'), __viewContext.TB('打开编辑器', 'open-domain', 'right', __nscVueInterpolate(["data-id=\"", "\" data-tab=\"config\""], [d.id]))])), '')]);
}
export function deploymentsPage_29(__viewContext, ds) {
  const items = __viewContext.db.deployments.filter(x => ds.some(d => d.id === x.domain));
  return __nscVueView(__viewContext.templates[132], [__viewContext.cardHead('发布记录', '失败不会抹掉成功节点；重试复用固定 Revision 和原始目标。', '', 'send'), items.length ? __nscVueJoin(items.map(x => __nscVueView(__viewContext.templates[130], [x.id, __viewContext.esc(__viewContext.db.domains.find(d => d.id === x.domain)?.name), __viewContext.environmentBadge(__viewContext.db.domains.find(d => d.id === x.domain)), x.version, __viewContext.esc(x.note || '配置变更'), x.targets.length, __viewContext.badge(x.status === 'success' ? '已确认（演示）' : x.status === 'partial' ? '部分失败' : '进行中', x.status === 'success' ? 'green' : 'amber'), x.time, __viewContext.TB('查看结果', 'deployment-detail', 'right', __nscVueInterpolate(["data-id=\"", "\""], [x.id]))])), '') : __nscVueView(__viewContext.templates[131], [])]);
}
export function eventsPage_30(__viewContext, ds) {
  return __nscVueView(__viewContext.templates[134], [__viewContext.cardHead('可追踪的操作', '所有记录仅存在于本地浏览器。', __viewContext.B('导出演示状态', 'export-state', 'download', 'sm'), 'activity'), __viewContext.allEnvironmentView() ? __nscVueJoin(__viewContext.listEnvironments().map(env => __nscVueView(__viewContext.templates[133], [__viewContext.esc(__viewContext.resourceScopeName({
    project: env.project,
    env: env.id
  })), __viewContext.timeline(__viewContext.db.events.filter(event => ds.some(d => d.id === event.domain && d.env === env.id)).slice(0, 40))])), '') : __viewContext.timeline(__viewContext.db.events.filter(e => ds.some(d => d.id === e.domain)).slice(0, 40))]);
}
export function validationItem_31(__viewContext, title, desc, type, ic) {
  return __nscVueView(__viewContext.templates[145], [__viewContext.I(ic, __nscVueAdd(' ', type)), __viewContext.esc(title), __viewContext.esc(desc)]);
}
export function diffHTML_32(__viewContext, a, b, short = false) {
  const lines = __viewContext.diffLines(a, b),
    add = lines.filter(x => x.type === 'add').length,
    remove = lines.filter(x => x.type === 'remove').length;
  let shown = lines;
  if (short) {
    const indices = new Set();
    lines.forEach((l, i) => {
      if (l.type !== 'same') for (let k = Math.max(0, i - 2); k <= Math.min(lines.length - 1, __nscVueAdd(i, 2)); k++) indices.add(k);
    });
    shown = indices.size ? lines.filter((_, i) => indices.has(i)) : lines.slice(0, 3);
  }
  return __nscVueView(__viewContext.templates[147], [__viewContext.esc(__viewContext.D().name), __viewContext.D().rev, add, remove, short ? 'style="max-height:190px"' : '', __nscVueJoin(shown.map(l => __nscVueView(__viewContext.templates[146], [l.type, l.type === 'add' ? '+' : l.type === 'remove' ? '−' : ' ', __viewContext.esc(l.text)])), '')]);
}
