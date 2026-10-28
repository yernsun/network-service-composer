import * as __infrastructureViews from "../views/infrastructure/renderers.js";
import * as __variablesViews from "../views/variables/renderers.js";
import * as __applicationsViews from "../views/applications/renderers.js";
import * as __projectsViews from "../views/projects/renderers.js";
import * as __shellViews from "../views/shell/renderers.js";
import * as __domainsViews from "../views/domains/renderers.js";
import NSCEngine from './compiler.js';
import { view as __nscVueView, add as __nscVueAdd, join as __nscVueJoin, interpolateView as __nscVueInterpolate, insertView, highlightLine, clearRegions, setRegion } from '../components/view';
import templates from '../views/blueprints.json';
export function createMockService(hooks) {
  const __renderContext0 = {
    get B() {
      return B;
    },
    get D() {
      return D;
    },
    get E() {
      return E;
    },
    get ENV() {
      return ENV;
    },
    get G() {
      return G;
    },
    get I() {
      return I;
    },
    get KINDS() {
      return KINDS;
    },
    get KIND_NAMES() {
      return KIND_NAMES;
    },
    get MATCH() {
      return MATCH;
    },
    get P() {
      return P;
    },
    get PAGE_NAMES() {
      return PAGE_NAMES;
    },
    get S() {
      return S;
    },
    get TB() {
      return TB;
    },
    get allEnvironmentView() {
      return allEnvironmentView;
    },
    get allProjectView() {
      return allProjectView;
    },
    get allRoutes() {
      return allRoutes;
    },
    get badge() {
      return badge;
    },
    get breadcrumb() {
      return breadcrumb;
    },
    get callout() {
      return callout;
    },
    get cardHead() {
      return cardHead;
    },
    get certificatesPage() {
      return certificatesPage;
    },
    get changed() {
      return changed;
    },
    get changesHTML() {
      return changesHTML;
    },
    get cloudOverview() {
      return cloudOverview;
    },
    get codeHTML() {
      return codeHTML;
    },
    get configPage() {
      return configPage;
    },
    get configsPage() {
      return configsPage;
    },
    get contextCrumb() {
      return contextCrumb;
    },
    get db() {
      return db;
    },
    get deploymentTimer() {
      return deploymentTimer;
    },
    get deploymentsPage() {
      return deploymentsPage;
    },
    get diagnosticHTML() {
      return diagnosticHTML;
    },
    get diffHTML() {
      return diffHTML;
    },
    get diffLines() {
      return diffLines;
    },
    get dirty() {
      return dirty;
    },
    get domainStatus() {
      return domainStatus;
    },
    get domainsPage() {
      return domainsPage;
    },
    get draftBadge() {
      return draftBadge;
    },
    get envCard() {
      return envCard;
    },
    get environmentBadge() {
      return environmentBadge;
    },
    get esc() {
      return esc;
    },
    get eventsPage() {
      return eventsPage;
    },
    get gatewayAllowed() {
      return gatewayAllowed;
    },
    get gatewayTable() {
      return gatewayTable;
    },
    get gatewaysPage() {
      return gatewaysPage;
    },
    get globalScopeContext() {
      return globalScopeContext;
    },
    get health() {
      return health;
    },
    get heroActions() {
      return heroActions;
    },
    get highlightLine() {
      return highlightLine;
    },
    get historyPage() {
      return historyPage;
    },
    get listDomains() {
      return listDomains;
    },
    get listEnvironments() {
      return listEnvironments;
    },
    get metric() {
      return metric;
    },
    get navEntry() {
      return navEntry;
    },
    get offline() {
      return offline;
    },
    get opsOverview() {
      return opsOverview;
    },
    get projectCrumb() {
      return projectCrumb;
    },
    get projectEnvs() {
      return projectEnvs;
    },
    get ready() {
      return ready;
    },
    get renderConfig() {
      return renderConfig;
    },
    get resourceScopeName() {
      return resourceScopeName;
    },
    get routeTable() {
      return routeTable;
    },
    get routesPage() {
      return routesPage;
    },
    get scopeDomains() {
      return scopeDomains;
    },
    get scopeServices() {
      return scopeServices;
    },
    get semanticConfig() {
      return semanticConfig;
    },
    get servicesPage() {
      return servicesPage;
    },
    get stale() {
      return stale;
    },
    get step() {
      return step;
    },
    get storageAvailable() {
      return storageAvailable;
    },
    get summarizeChanges() {
      return summarizeChanges;
    },
    get syncInfo() {
      return syncInfo;
    },
    get templates() {
      return templates;
    },
    get timeline() {
      return timeline;
    },
    get tlsPage() {
      return tlsPage;
    },
    get ui() {
      return ui;
    },
    get work() {
      return work;
    },
    get workspaceOverview() {
      return workspaceOverview;
    }
  };
  let booting = true,
    NSCBridge,
    NSCWorkspace;
  const disposers = [];
  function listen(target, type, handler, options) {
    target.addEventListener(type, handler, options);
    disposers.push(() => target.removeEventListener(type, handler, options));
  }
  listen(window, 'click', environmentAction, true);
  function region(el, value) {
    if (el) {
      setRegion(el.id, value);
      hooks.render();
    }
  }
  /* NSC interactive product prototype. Browser-only. No requests to infrastructure. */
  'use strict';
  const ICONS = {
    globe: __nscVueView(templates[4], []),
    grid: __nscVueView(templates[5], []),
    route: __nscVueView(templates[6], []),
    box: __nscVueView(templates[7], []),
    server: __nscVueView(templates[8], []),
    shield: __nscVueView(templates[9], []),
    file: __nscVueView(templates[10], []),
    code: __nscVueView(templates[11], []),
    send: __nscVueView(templates[12], []),
    activity: __nscVueView(templates[13], []),
    clock: __nscVueView(templates[14], []),
    settings: __nscVueView(templates[15], []),
    cloud: __nscVueView(templates[16], []),
    terminal: __nscVueView(templates[17], []),
    search: __nscVueView(templates[18], []),
    help: __nscVueView(templates[19], []),
    bell: __nscVueView(templates[20], []),
    chevron: __nscVueView(templates[21], []),
    right: __nscVueView(templates[22], []),
    arrow: __nscVueView(templates[23], []),
    back: __nscVueView(templates[24], []),
    check: __nscVueView(templates[25], []),
    checkcircle: __nscVueView(templates[26], []),
    x: __nscVueView(templates[27], []),
    plus: __nscVueView(templates[28], []),
    save: __nscVueView(templates[29], []),
    compare: __nscVueView(templates[30], []),
    edit: __nscVueView(templates[31], []),
    trash: __nscVueView(templates[32], []),
    copy: __nscVueView(templates[33], []),
    download: __nscVueView(templates[34], []),
    refresh: __nscVueView(templates[35], []),
    info: __nscVueView(templates[36], []),
    warning: __nscVueView(templates[37], []),
    lock: __nscVueView(templates[38], []),
    link: __nscVueView(templates[39], []),
    layers: __nscVueView(templates[40], []),
    external: __nscVueView(templates[41], []),
    dots: __nscVueView(templates[42], []),
    menu: __nscVueView(templates[43], []),
    play: __nscVueView(templates[44], []),
    filter: __nscVueView(templates[45], []),
    history: __nscVueView(templates[46], []),
    folder: __nscVueView(templates[47], []),
    bolt: __nscVueView(templates[48], []),
    users: __nscVueView(templates[49], [])
  };
  const I = (n, cls = '') => __nscVueView(templates[50], [cls, ICONS[n] || ICONS.file]);
  const esc = x => String(x ?? '').replace(/[&<>"']/g, c => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;'
  })[c]);
  const clone = x => JSON.parse(JSON.stringify(x));
  const equal = (a, b) => JSON.stringify(a) === JSON.stringify(b);
  const KEY = 'nginx-service-composer-vue-v1.3';
  const ENV = {
    prod: 'Production',
    test: 'Testing',
    sandbox: 'Sandbox',
    dev: 'Development',
    'docs-prod': 'Production'
  };
  const MATCH = {
    prefix: '前缀匹配',
    exact: '精确匹配',
    prefer: '优先前缀 ^~'
  };
  const KINDS = {
    proxy: '反向代理',
    return: '固定响应',
    redirect: '重定向'
  };
  const stamp = () => new Date().toLocaleString('sv-SE', {
    timeZone: 'Asia/Shanghai'
  }).slice(0, 19);
  const uid = p => __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(p, '-'), Date.now().toString(36)), '-'), Math.random().toString(36).slice(2, 6));
  const badge = (text, type = '', dot = false) => __nscVueView(templates[51], [type, dot ? __nscVueView(templates[0], [type === 'amber' ? 'amber' : type === 'red' ? 'red' : type === '' ? 'gray' : '']) : '', esc(text)]);
  const B = (text, action, icon = '', cls = '', attrs = '') => __nscVueView(templates[52], [cls, action, attrs, icon ? I(icon) : '', text]);
  const TB = (text, action, icon = '', attrs = '') => __nscVueView(templates[53], [action, attrs, text, icon ? I(icon, 'sm') : '']);
  const callout = (text, type = 'blue', icon = 'info') => __nscVueView(templates[54], [type, I(icon), text]);
  const cardHead = (title, sub = '', action = '', icon = '') => __nscVueView(templates[56], [icon ? I(icon) : '', title, sub ? __nscVueView(templates[55], [sub]) : '', action]);
  const route = (id, path, service, extra = {}) => ({
    id,
    name: service ? __nscVueAdd(service, ' route') : '健康检查',
    path,
    match: 'prefix',
    kind: 'proxy',
    service,
    timeout: 30,
    connect: 10,
    strip: false,
    websocket: false,
    enabled: true,
    snippet: '',
    code: 200,
    value: 'ok',
    ...extra
  });
  function seedV10() {
    const services = [{
      id: 'web-frontend',
      env: 'prod',
      runtime: 'swarm-production',
      image: 'geeksmeta/frontend:1.8.2',
      port: 3000,
      endpoints: [['10.0.0.11', true], ['10.0.0.12', true], ['10.0.0.13', true]]
    }, {
      id: 'api-service',
      env: 'prod',
      runtime: 'swarm-production',
      image: 'geeksmeta/api:2.4.1',
      port: 8080,
      endpoints: [['10.0.1.21', true], ['10.0.1.22', true], ['10.0.1.23', false]]
    }, {
      id: 'static-service',
      env: 'prod',
      runtime: 'swarm-production',
      image: 'geeksmeta/assets:1.3.0',
      port: 8080,
      endpoints: [['10.0.2.31', true], ['10.0.2.32', true]]
    }, {
      id: 'console-service',
      env: 'prod',
      runtime: 'swarm-production',
      image: 'geeksmeta/console:1.1.0',
      port: 8080,
      endpoints: [['10.0.3.41', true]]
    }, {
      id: 'staging-api',
      env: 'staging',
      runtime: 'swarm-staging',
      image: 'geeksmeta/api:2.5.0-rc',
      port: 8080,
      endpoints: [['10.1.0.21', true]]
    }, {
      id: 'dev-web',
      env: 'dev',
      runtime: 'compose-local',
      image: 'geeksmeta/frontend:dev',
      port: 3000,
      endpoints: [['172.18.0.5', true]]
    }];
    const make = (id, name, env, gateways, routes, rev) => {
      const config = {
        mode: 'managed',
        tls: true,
        redirect: true,
        body: '32m',
        serverSnippet: '# Domain 级扩展：由 NSC 放入 server 上下文\nadd_header X-Content-Type-Options nosniff always;',
        native: '',
        routes
      };
      return {
        id,
        name,
        env,
        gateways,
        rev,
        active: config,
        loaded: Object.fromEntries(gateways.map(g => [g, rev])),
        draft: null,
        drift: null,
        created: '2026-08-20 10:24:00',
        certExpiry: '2026-12-01',
        revisions: [{
          v: rev,
          config: clone(config),
          time: '2026-09-13 17:42:08',
          note: '入口配置已确认（演示种子数据）'
        }]
      };
    };
    const domains = [make('d1', 'example.com', 'prod', ['gw1', 'gw2'], [route('r-api', '/api/', 'api-service'), route('r-assets', '/assets/', 'static-service', {
      match: 'prefer',
      strip: true
    }), route('r-health', '/health', '', {
      match: 'exact',
      kind: 'return',
      name: '健康检查'
    }), route('r-web', '/', 'web-frontend')], 12), make('d2', 'api.example.com', 'prod', ['gw1', 'gw2'], [route('r-api-root', '/', 'api-service')], 7), make('d3', 'admin.example.com', 'prod', ['gw1', 'gw2'], [route('r-console', '/', 'console-service')], 4), make('d4', 'staging.example.com', 'staging', ['gw3'], [route('r-stage', '/', 'staging-api')], 5), make('d5', 'dev.example.com', 'dev', ['gw4'], [route('r-dev', '/', 'dev-web')], 2)];
    const draft = clone(domains[0].active);
    draft.routes[0].timeout = 60;
    domains[0].draft = {
      base: 12,
      config: draft,
      savedAt: '2026-09-14 09:20:00'
    };
    const previous = clone(domains[0].active);
    previous.body = '16m';
    domains[0].revisions.push({
      v: 11,
      config: previous,
      time: '2026-09-12 14:18:00',
      note: '调整请求体上限之前的版本'
    });
    return {
      schema: 1,
      epoch: 0,
      view: 'cloud',
      env: 'prod',
      scenario: 'normal',
      scenarioGateway: null,
      services,
      gateways: [{
        id: 'gw1',
        name: 'nginx-gw-01',
        env: 'prod',
        ip: '10.0.0.2',
        runtime: 'swarm-production'
      }, {
        id: 'gw2',
        name: 'nginx-gw-02',
        env: 'prod',
        ip: '10.0.0.3',
        runtime: 'swarm-production'
      }, {
        id: 'gw3',
        name: 'nginx-gw-staging',
        env: 'staging',
        ip: '10.1.0.2',
        runtime: 'swarm-staging'
      }, {
        id: 'gw4',
        name: 'nginx-gw-dev',
        env: 'dev',
        ip: '172.18.0.2',
        runtime: 'compose-local'
      }],
      domains,
      deployments: [{
        id: 'DEP-012',
        domain: 'd1',
        version: 12,
        previous: 11,
        time: '2026-09-13 17:42:08',
        status: 'success',
        targets: ['gw1', 'gw2'],
        results: {
          gw1: 'success',
          gw2: 'success'
        },
        logs: ['[demo] revision v12 · seeded deployment record'],
        note: '更新入口配置'
      }],
      events: [{
        time: '2026-09-14 09:20:00',
        domain: 'd1',
        title: '保存 Route 超时变更草稿',
        detail: '/api/ · proxy_read_timeout 30s → 60s',
        type: 'edit'
      }, {
        time: '2026-09-13 17:42:08',
        domain: 'd1',
        title: 'v12 在 2 个 Gateway 已确认',
        detail: '演示种子记录 · 非真实观测',
        type: 'check'
      }, {
        time: '2026-09-13 17:42:01',
        domain: 'd1',
        title: '开始模拟发布 v12',
        detail: '内容和目标均已冻结',
        type: 'send'
      }]
    };
  }
  let storageAvailable = true,
    storageWarning = '',
    db;
  try {
    const raw = localStorage.getItem(KEY);
    db = raw ? JSON.parse(raw) : seed();
    if (db.schema !== 2 || !Array.isArray(db.domains) || !Array.isArray(db.services)) db = seed();
  } catch (e) {
    db = seed();
    storageAvailable = false;
    storageWarning = '浏览器存储不可用：刷新后将丢失修改，请导出备份。';
  }
  let ui = {
    page: 'project',
    domain: 'd1',
    tab: 'overview',
    file: 'generated',
    configView: 'code',
    selectedRoute: 'r-api',
    filter: 'all',
    search: '',
    menu: false,
    diag: null,
    diagUrl: 'https://example.com/api/users?limit=10',
    diagSource: 'draft',
    validation: null,
    externalConflict: false
  };
  let work = clone(db.domains[0].draft?.config || db.domains[0].active),
    modal = null,
    routeForm = null,
    routeDraftSnapshot = null,
    deploymentTimer = null,
    focusBeforeModal = null;
  function D() {
    return db.domains.find(d => d.id === ui.domain) || db.domains[0];
  }
  function S(id) {
    return db.services.find(s => s.id === id);
  }
  function G(id) {
    return db.gateways.find(g => g.id === id);
  }
  function ready(id) {
    const s = S(id);
    return s ? s.endpoints.filter(e => e[1]) : [];
  }
  function offline(gid) {
    return db.scenario === 'offline' && gid === db.scenarioGateway;
  }
  function workingSaved() {
    return D().draft?.config || D().active;
  }
  function dirty() {
    return !equal(work, workingSaved());
  }
  function changed() {
    return !equal(work, D().active) || D().rev === 0;
  }
  function stale() {
    return !!(D().draft && D().draft.base !== D().rev) || ui.externalConflict;
  }
  function persist() {
    db.epoch++;
    try {
      localStorage.setItem(KEY, JSON.stringify(db));
    } catch (e) {
      storageAvailable = false;
      storageWarning = '浏览器存储不可用；请导出工作区备份。';
    }
  }
  function event(title, detail = '', type = 'info', domain = ui.domain) {
    db.events.unshift({
      time: stamp(),
      domain,
      title,
      detail,
      type
    });
    db.events = db.events.slice(0, 120);
  }
  function toast(text, error = false) {
    hooks.toast(String(text), error);
  }
  function signature() {
    return JSON.stringify(work);
  }
  function invalidate() {
    ui.validation = null;
    ui.diag = null;
    refreshHeader();
  }
  function refreshHeader() {
    const el = document.querySelector('#hero-actions');
    if (el) region(el, heroActions());
    const flag = document.querySelector('#draft-flag');
    if (flag) region(flag, draftBadge());
    const saved = document.querySelector('#workspace-status');
    if (saved) saved.textContent = dirty() ? '有未保存修改' : D().draft ? '草稿已保存至本机' : '与期望版本一致';
  }
  function saveDraft(silent = false) {
    try {
      const raw = localStorage.getItem(KEY);
      if (raw && JSON.parse(raw).epoch > db.epoch) ui.externalConflict = true;
    } catch (_) {}
    if (ui.externalConflict) {
      if (!silent) toast('另一标签页已写入更新，请先导出当前草稿或重新载入。', true);
      return false;
    }
    const d = D();
    d.draft = {
      base: d.draft?.base ?? d.rev,
      config: clone(work),
      savedAt: stamp()
    };
    event('保存草稿', __nscVueInterpolate(["", " \xB7 \u57FA\u4E8E v", ""], [d.name, d.draft.base]), 'save');
    persist();
    refreshHeader();
    if (!silent) toast('草稿已保存；运行中的配置没有变化。');
    return true;
  }
  function ensureNavigate(fn) {
    if (deploymentTimer) {
      toast('当前模拟发布进行中，请先查看本次结果。', true);
      return;
    }
    if (!dirty()) {
      fn();
      return;
    }
    showModal({
      title: '保留未保存的修改？',
      sub: __nscVueAdd(D().name, ' · 离开当前 Domain'),
      body: callout('视角和页签切换会保留编辑；切换 Domain 或模块前，需要处理尚未保存的修改。', 'neutral'),
      footer: __nscVueAdd(__nscVueAdd(B('继续编辑', 'close', '', 'ghost'), B('放弃并离开', 'discard-leave')), B('保存草稿并离开', 'save-leave', 'save', 'primary')),
      size: 'small',
      next: fn
    });
  }
  function navigate(page, tab = 'overview', id = ui.domain, guard = true) {
    const run = () => {
      ui.page = page;
      ui.tab = tab;
      ui.search = '';
      ui.filter = 'all';
      ui.menu = false;
      if (id !== ui.domain) {
        ui.domain = id;
        db.env = D().env;
        work = clone(workingSaved());
        ui.validation = null;
        ui.diag = null;
        ui.selectedRoute = work.routes[0]?.id;
        ui.diagUrl = __nscVueAdd(__nscVueAdd('https://', D().name), '/api/users');
        ui.diagSource = 'draft';
        ui.file = 'generated';
      }
      updateHash();
      render();
      window.scrollTo(0, 0);
    };
    if (guard && (page !== ui.page || id !== ui.domain)) ensureNavigate(run);else run();
  }
  function updateHash() {
    if (booting) return;
    const hash = ui.page === 'domain' ? __nscVueInterpolate(["#/domain/", "/", ""], [encodeURIComponent(D().name), ui.tab]) : __nscVueInterpolate(["#/", ""], [ui.page]);
    if (location.hash !== hash) history.replaceState(null, '', hash);
  }
  function parseHash() {
    const parts = location.hash.replace(/^#\/?/, '').split('/');
    if (parts[0] === 'domain') {
      const d = db.domains.find(d => d.name === decodeURIComponent(parts[1] || ''));
      if (d) {
        ui.domain = d.id;
        db.env = d.env;
        ui.tab = ['overview', 'routes', 'config', 'tls', 'history'].includes(parts[2]) ? parts[2] : 'overview';
        work = clone(d.draft?.config || d.active);
        ui.selectedRoute = work.routes[0]?.id;
        ui.diagUrl = __nscVueAdd(__nscVueAdd('https://', d.name), '/api/users');
      }
    } else if (['overview', 'domains', 'routes', 'services', 'gateways', 'certificates', 'configs', 'deployments', 'events'].includes(parts[0])) ui.page = parts[0];
  }
  function draftBadge(...args) {
    return __domainsViews.draftBadge_0(__renderContext0, ...args);
  }
  function syncInfo(d = D()) {
    const known = d.gateways.filter(id => d.rev > 0 && !offline(id) && d.loaded[id] === d.rev && !d.drift?.gateway?.includes(id));
    return {
      known: known.length,
      total: d.gateways.length,
      mixed: d.gateways.some(id => d.loaded[id] !== d.rev),
      unknown: d.gateways.some(offline)
    };
  }
  function domainStatus(...args) {
    return __domainsViews.domainStatus_1(__renderContext0, ...args);
  }
  function heroActions(...args) {
    return __domainsViews.heroActions_2(__renderContext0, ...args);
  }
  function navigation(...args) {
    return __shellViews.navigation_3(__renderContext0, ...args);
  }
  function shell() {
    return __nscVueView(templates[62], [I('menu'), I('search', 'sm'), I('help'), I('bell'), ui.menu ? 'open' : '', db.env === 'prod' ? '' : db.env === 'staging' ? 'amber' : 'gray', __nscVueJoin(Object.entries(ENV).map(([v, n]) => __nscVueView(templates[61], [v, v === db.env ? 'selected' : '', n])), ''), I('chevron', 'sm'), navigation(), I('play', 'sm'), badge('OFFLINE'), TB('切换演示场景', 'scenarios', 'right'), I('help'), I('external', 'sm'), storageWarning ? callout(esc(storageWarning), 'amber') : '', ui.externalConflict ? callout(__nscVueAdd(__nscVueAdd(__nscVueAdd('检测到另一标签页写入了新状态。当前草稿已保留，但不能继续覆盖保存。请导出当前草稿后重新载入。 ', TB('导出当前配置', 'export')), ' · '), TB('重新载入', 'reload-state')), 'red') : '', ui.page === 'domain' ? domainPage() : globalPage()]);
  }
  function domainPage(...args) {
    return __domainsViews.domainPage_4(__renderContext0, ...args);
  }
  function metric(...args) {
    return __domainsViews.metric_5(__renderContext0, ...args);
  }
  function cloudOverview(...args) {
    return __domainsViews.cloudOverview_6(__renderContext0, ...args);
  }
  function step(...args) {
    return __domainsViews.step_7(__renderContext0, ...args);
  }
  function routeTable(...args) {
    return __domainsViews.routeTable_8(__renderContext0, ...args);
  }
  function summarizeChanges() {
    const a = D().active,
      b = work,
      out = [];
    for (const [key, label] of [['mode', '配置管理方式'], ['tls', 'HTTPS'], ['redirect', 'HTTP 跳转'], ['body', '请求体上限'], ['serverSnippet', '补充域名指令'], ['native', '手动配置文件']]) if ((b.mode !== 'native' || ['mode', 'native'].includes(key)) && !equal(a[key], b[key])) out.push({
      title: label,
      detail: key === 'serverSnippet' ? 'server 上下文扩展已修改' : key === 'native' ? '手动配置文件文本发生变化' : 'Domain 配置变更',
      old: typeof a[key] === 'boolean' ? a[key] ? '开启' : '关闭' : ['native', 'serverSnippet'].includes(key) ? '原内容' : a[key],
      new: typeof b[key] === 'boolean' ? b[key] ? '开启' : '关闭' : ['native', 'serverSnippet'].includes(key) ? '新内容' : b[key]
    });
    if (b.mode === 'native') return out;
    const map = new Map(a.routes.map(r => [r.id, r]));
    for (const r of b.routes) {
      const old = map.get(r.id);
      if (!old) {
        out.push({
          title: __nscVueAdd('新增 ', r.path),
          detail: __nscVueAdd(__nscVueAdd(KINDS[r.kind], ' → '), r.service || r.value),
          old: '未配置',
          new: '新增'
        });
        continue;
      }
      map.delete(r.id);
      const fields = {
        path: '请求路径',
        match: '匹配方式',
        kind: '处理动作',
        service: '目标服务',
        timeout: '读取超时',
        connect: '连接超时',
        strip: 'URI 转发',
        websocket: 'WebSocket',
        enabled: '启用状态',
        snippet: '补充路由指令',
        code: '响应码',
        value: '响应内容'
      };
      for (const [k, label] of Object.entries(fields)) if (!equal(old[k], r[k])) out.push({
        title: __nscVueAdd(__nscVueAdd(r.path, ' · '), label),
        detail: k === 'timeout' ? 'proxy_read_timeout' : k === 'connect' ? 'proxy_connect_timeout' : 'Route 配置变更',
        old: k === 'snippet' ? '原扩展' : k === 'timeout' || k === 'connect' ? __nscVueAdd(old[k], 's') : typeof old[k] === 'boolean' ? old[k] ? '开启' : '关闭' : old[k],
        new: k === 'snippet' ? '新扩展' : k === 'timeout' || k === 'connect' ? __nscVueAdd(r[k], 's') : typeof r[k] === 'boolean' ? r[k] ? '开启' : '关闭' : r[k]
      });
    }
    for (const old of map.values()) out.push({
      title: __nscVueAdd('删除 ', old.path),
      detail: '匹配结果可能由其他路由接管',
      old: '已配置',
      new: '删除'
    });
    return out;
  }
  function changesHTML(...args) {
    return __domainsViews.changesHTML_9(__renderContext0, ...args);
  }
  function opsOverview(...args) {
    return __domainsViews.opsOverview_10(__renderContext0, ...args);
  }
  function health(...args) {
    return __domainsViews.health_11(__renderContext0, ...args);
  }
  function gatewayTable(...args) {
    return __domainsViews.gatewayTable_12(__renderContext0, ...args);
  }
  function timeline(...args) {
    return __domainsViews.timeline_13(__renderContext0, ...args);
  }
  function diagnosticHTML(...args) {
    return __domainsViews.diagnosticHTML_14(__renderContext0, ...args);
  }
  function runDiagnostic() {
    ui.diagUrl = document.querySelector('#diag-url')?.value || ui.diagUrl;
    ui.diagSource = document.querySelector('#diag-source')?.value || ui.diagSource;
    let c = work;
    if (ui.diagSource === 'target') c = D().active;else if (ui.diagSource !== 'draft') {
      const id = ui.diagSource;
      if (offline(id) || D().drift?.gateway === id) {
        ui.diag = {
          error: true,
          title: '运行配置证据不足',
          note: '该节点失联或存在漂移，不能使用期望配置冒充运行状态。'
        };
        return redrawDiag();
      }
      c = D().revisions.find(r => r.v === D().loaded[id])?.config;
      if (!c) {
        ui.diag = {
          error: true,
          title: '没有该节点已确认配置的快照',
          note: '先完成节点核实，再进行配置模拟。'
        };
        return redrawDiag();
      }
    }
    try {
      const u = new URL(ui.diagUrl);
      if (!['http:', 'https:'].includes(u.protocol)) throw Error('仅支持 HTTP / HTTPS URL。');
      if (u.hostname !== D().name) throw Error('URL 的 Host 不属于当前 Domain。');
      if (c.mode === 'native') throw Error('手动配置文件无法安全映射回 Route；匹配结果未知。');
      if (c.routes.some(r => r.enabled && r.snippet.trim())) throw Error('Route 扩展可能改变处理结果，本地模拟不覆盖扩展指令。');
      if (/rewrite|return|if\s*\(|include/.test(c.serverSnippet.replace(/#.*$/gm, ''))) throw Error('Domain 扩展可能改变请求处理；结果需要真实 NGINX 验证。');
      if (u.protocol === 'https:' && !c.tls) throw Error('所选配置没有 HTTPS Listener。');
      if (u.port && u.port !== (u.protocol === 'https:' ? '443' : '80')) throw Error('所选配置没有对应的监听端口。');
      if (u.protocol === 'http:' && c.tls && c.redirect) {
        ui.diag = {
          title: '匹配 HTTP → HTTPS 跳转',
          note: '所选配置声明返回 308；未发送网络请求。',
          error: false
        };
        return redrawDiag();
      }
      if (/%|\/\/|\/\.|[^\x20-\x7E]/.test(u.pathname)) throw Error('复杂编码或路径规范化不在本地模拟范围内。');
      const rs = c.routes.filter(r => r.enabled);
      const r = rs.find(r => r.match === 'exact' && r.path === u.pathname) || rs.filter(r => r.match !== 'exact' && u.pathname.startsWith(r.path)).sort((a, b) => b.path.length - a.path.length)[0];
      if (!r) {
        ui.diag = {
          title: '未匹配 · 404',
          note: '该配置没有覆盖此 URI 的 Route，生成配置使用 404 兜底。'
        };
      } else {
        const path = r.kind === 'proxy' ? __nscVueAdd(r.strip ? __nscVueAdd('/', u.pathname.slice(r.path.length)) : u.pathname, u.search) : r.value;
        ui.diag = {
          title: '配置匹配成功',
          note: r.kind === 'proxy' ? '这是路由推导结果；未验证真实上游响应。' : '返回值来自配置声明，不是真实 HTTP 检测。',
          route: clone(r),
          uri: path
        };
        ui.selectedRoute = r.id;
      }
    } catch (e) {
      ui.diag = {
        error: true,
        title: '无法完成匹配',
        note: e.message
      };
    }
    redrawDiag();
  }
  function redrawDiag() {
    if (modal?.kind === 'diagnose') {
      modal.body = diagnosticHTML();
      renderModal();
    } else render();
  }
  function routesPage(...args) {
    return __domainsViews.routesPage_15(__renderContext0, ...args);
  }
  function upstreamName(id, d = D()) {
    return __nscVueAdd(__nscVueAdd(__nscVueAdd('nsc_', d.id.replace(/[^a-zA-Z0-9_]/g, '_')), '_'), id.replace(/[^a-zA-Z0-9_]/g, '_'));
  }
  function nginxQuote(v) {
    return __nscVueAdd(__nscVueAdd('"', String(v).replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\r/g, '\\r').replace(/\n/g, '\\n')), '"');
  }
  function renderConfig(c, d = D()) {
    if (c.mode === 'native') return c.native;
    const rs = c.routes.filter(r => r.enabled),
      svcs = [...new Set(rs.filter(r => r.kind === 'proxy').map(r => r.service))];
    let lines = [__nscVueAdd('# NSC · ', d.name), '# Generated HTTP-context fragment. Attach through the Gateway snapshot.', '# Demonstration only: paths, addresses and readiness are sample data.', ''];
    for (const id of svcs) {
      lines.push(__nscVueInterpolate(["upstream ", " {"], [upstreamName(id, d)]));
      const endpoints = ready(id),
        s = S(id);
      for (const [ip] of endpoints) lines.push(__nscVueInterpolate(["    server ", ":", ";"], [ip, s.port]));
      if (!endpoints.length) lines.push('    # ERROR: no Ready endpoints. Publishing is blocked.');
      lines.push('    keepalive 32;', '}', '');
    }
    if (c.tls && c.redirect) lines.push('server {', '    listen 80;', __nscVueAdd(__nscVueAdd('    server_name ', d.name), ';'), '    return 308 https://$host$request_uri;', '}', '');
    lines.push('server {');
    if (!c.tls || !c.redirect) lines.push('    listen 80;');
    if (c.tls) lines.push('    listen 443 ssl;');
    lines.push(__nscVueAdd(__nscVueAdd('    server_name ', d.name), ';'));
    if (c.tls) lines.push(__nscVueInterpolate(["    ssl_certificate /etc/nginx/certs/", "/fullchain.pem;"], [d.name]), __nscVueInterpolate(["    ssl_certificate_key /etc/nginx/certs/", "/privkey.pem;"], [d.name]));
    lines.push(__nscVueAdd(__nscVueAdd('    client_max_body_size ', c.body), ';'), '');
    if (c.serverSnippet.trim()) lines.push(...c.serverSnippet.split('\n').map(l => __nscVueAdd('    ', l)), '');
    for (const r of rs) {
      lines.push(__nscVueInterpolate(["    # Route: ", " \u2192 ", ""], [r.id, r.kind === 'proxy' ? r.service : r.kind]));
      const mod = r.match === 'exact' ? '= ' : r.match === 'prefer' ? '^~ ' : '';
      lines.push(__nscVueInterpolate(["    location ", "", " {"], [mod, r.path]));
      if (r.kind === 'proxy') {
        lines.push(__nscVueInterpolate(["        proxy_pass http://", "", ";"], [upstreamName(r.service, d), r.strip ? '/' : '']), '        proxy_http_version 1.1;', '        proxy_set_header Host $host;', '        proxy_set_header X-Real-IP $remote_addr;', '        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;', '        proxy_set_header X-Forwarded-Proto $scheme;', __nscVueInterpolate(["        proxy_connect_timeout ", "s;"], [r.connect]), __nscVueInterpolate(["        proxy_read_timeout ", "s;"], [r.timeout]));
        if (r.websocket) lines.push('        proxy_set_header Upgrade $http_upgrade;', '        proxy_set_header Connection "upgrade";');else lines.push('        proxy_set_header Connection "";');
      } else lines.push(__nscVueInterpolate(["        return ", " ", ";"], [r.code, nginxQuote(r.value)]));
      if (r.snippet.trim()) lines.push('        # User-managed Route extension', ...r.snippet.split('\n').map(l => __nscVueAdd('        ', l)));
      lines.push('    }', '');
    }
    if (!rs.some(r => r.path === '/' && r.match !== 'exact')) lines.push('    location / { return 404; }');
    lines.push('}', '');
    return __nscVueJoin(lines, '\n');
  }
  function codeHTML(...args) {
    return __domainsViews.codeHTML_16(__renderContext0, ...args);
  }
  function configPage(...args) {
    return __domainsViews.configPage_17(__renderContext0, ...args);
  }
  function semanticConfig(...args) {
    return __domainsViews.semanticConfig_18(__renderContext0, ...args);
  }
  function tlsPage(...args) {
    return __domainsViews.tlsPage_19(__renderContext0, ...args);
  }
  function historyPage(...args) {
    return __shellViews.historyPage_20(__renderContext0, ...args);
  }
  const PAGE_NAMES = {
    overview: '工作区概览',
    domains: 'Domains',
    routes: 'Routes',
    services: 'Services',
    gateways: 'Gateways',
    certificates: '证书资产',
    configs: '配置工作台',
    deployments: '变更与发布',
    events: '活动记录'
  };
  function globalPage(...args) {
    return __shellViews.globalPage_21(__renderContext0, ...args);
  }
  function domainsPage(...args) {
    return __domainsViews.domainsPage_22(__renderContext0, ...args);
  }
  function workspaceOverview(...args) {
    return __domainsViews.workspaceOverview_23(__renderContext0, ...args);
  }
  function allRoutes(...args) {
    return __domainsViews.allRoutes_24(__renderContext0, ...args);
  }
  function servicesPage(...args) {
    return __domainsViews.servicesPage_25(__renderContext0, ...args);
  }
  function gatewaysPage(...args) {
    return __domainsViews.gatewaysPage_26(__renderContext0, ...args);
  }
  function certificatesPage(...args) {
    return __domainsViews.certificatesPage_27(__renderContext0, ...args);
  }
  function configsPage(...args) {
    return __domainsViews.configsPage_28(__renderContext0, ...args);
  }
  function deploymentsPage(...args) {
    return __domainsViews.deploymentsPage_29(__renderContext0, ...args);
  }
  function eventsPage(...args) {
    return __domainsViews.eventsPage_30(__renderContext0, ...args);
  }
  function render() {
    if (booting) return;
    clearRegions();
    hooks.render();
    if (modal) renderModal(false);
  }
  function showModal(spec) {
    if (!modal) focusBeforeModal = document.activeElement;
    modal = spec;
    renderModal();
  }
  function renderModal(focus = true) {
    if (booting) return;
    document.body.style.overflow = modal ? 'hidden' : '';
    hooks.render();
    if (focus && modal) setTimeout(() => {
      const host = document.getElementById('overlay');
      const el = host?.querySelector('[autofocus],input:not([type="checkbox"]):not(:disabled),textarea:not(:disabled),select:not(:disabled)') || host?.querySelector('button:not(:disabled)');
      el?.focus();
    }, 30);
  }
  function closeModal(force = false) {
    if (modal?.locked && !force) return;
    if (modal?.kind === 'route' && !force && routeDraftSnapshot && !equal(routeForm, routeDraftSnapshot)) {
      showModal({
        title: '放弃路由表单的修改？',
        sub: '该表单还没有写入 Domain 草稿。',
        body: callout('关闭会丢弃这次表单修改；已经保存的 Domain 草稿不受影响。', 'amber'),
        footer: __nscVueAdd(B('继续编辑', 'route-resume'), B('放弃表单', 'route-discard', '', 'danger')),
        size: 'small',
        kind: 'route-exit'
      });
      return;
    }
    modal = null;
    routeForm = null;
    routeDraftSnapshot = null;
    renderModal();
    focusBeforeModal?.isConnected && focusBeforeModal.focus();
  }
  function openRoute(id) {
    if (work.mode === 'native') {
      toast('文件编辑范围的结构化编辑已锁定，请编辑配置文件。', true);
      navigate('domain', 'config', ui.domain, false);
      ui.file = 'native';
      render();
      return;
    }
    const r = work.routes.find(r => r.id === id);
    routeForm = r ? clone(r) : route(uid('route'), '/new/', db.services.find(s => s.env === D().env && s.project === D().project)?.id || '', {
      name: '新 Route'
    });
    routeDraftSnapshot = clone(routeForm);
    showRouteForm();
  }
  function showRouteForm(error = '') {
    const r = routeForm;
    ui.selectedRoute = r.id;
    const upstreamPath = r.strip ? '/users' : __nscVueAdd(r.path.replace(/\/$/, ''), '/users');
    showModal({
      kind: 'route',
      size: 'drawer',
      title: work.routes.some(x => x.id === r.id) ? '编辑 Route' : '添加 Route',
      sub: __nscVueAdd(esc(D().name), ' / 请求规则 → 处理动作'),
      body: __nscVueView(templates[142], [esc(r.path), __nscVueJoin(Object.entries(MATCH).map(([v, t]) => __nscVueView(templates[135], [v, v === r.match ? 'selected' : '', t, v === 'prefix' ? ' · 最长前缀' : ''])), ''), __nscVueJoin([['proxy', 'box'], ['return', 'checkcircle'], ['redirect', 'external']].map(([k, ic]) => __nscVueView(templates[136], [r.kind === k ? 'selected' : '', k, I(ic), KINDS[k]])), ''), r.kind === 'proxy' ? __nscVueView(templates[138], [__nscVueJoin(db.services.filter(s => s.env === D().env && s.project === D().project).map(s => __nscVueView(templates[137], [s.id, s.id === r.service ? 'selected' : '', s.id, s.port, ready(s.id).length])), ''), r.timeout, r.connect, r.strip ? 'on' : '', r.strip, r.websocket ? 'on' : '', r.websocket, esc(r.path), esc(upstreamPath)]) : __nscVueView(templates[140], [__nscVueJoin((r.kind === 'return' ? [200, 204, 403, 404, 503] : [301, 302, 307, 308]).map(n => __nscVueView(templates[139], [n, Number(r.code) === n ? 'selected' : '', n])), ''), r.kind === 'redirect' ? '跳转 URL' : '响应正文', esc(r.value), r.kind === 'redirect' ? 'https://example.com/' : 'ok']), r.enabled ? 'on' : '', r.enabled, callout('应用到草稿不会发布。高级指令在配置工作台的 Route 扩展中编辑。', 'neutral'), error ? __nscVueView(templates[141], [esc(error)]) : '']),
      footer: __nscVueView(templates[3], [work.routes.some(x => x.id === r.id) ? B('删除此 Route', 'route-delete', 'trash', 'danger sm') : '', B('取消', 'close', '', 'ghost'), B('应用到草稿', 'route-apply', 'check', 'primary')])
    });
  }
  function checkRoute(r, exceptId = r.id) {
    if (!/^\/[a-zA-Z0-9_./~!@+\-]*$/.test(r.path) || r.path.includes('//') || r.path.includes('/../')) return '路径需要以 / 开头；本原型仅支持无空白的简单字面量路径。';
    if (!Object.hasOwn(MATCH, r.match)) return '匹配方式不受支持。';
    if (work.routes.some(x => x.id !== exceptId && x.enabled && r.enabled && x.path === r.path && x.match === 'exact' === (r.match === 'exact'))) return '存在相同路径与匹配类别的已启用规则，不能重复定义 location。';
    if (r.kind === 'proxy') {
      const s = S(r.service);
      if (!s || s.env !== D().env) return '请选择当前环境的有效 Service。';
      if (!Number.isInteger(r.timeout) || r.timeout < 1 || r.timeout > 3600) return '读取超时必须是 1–3600 的整数秒。';
      if (!Number.isInteger(r.connect) || r.connect < 1 || r.connect > 300) return '连接超时必须是 1–300 的整数秒。';
      if (r.strip && !r.path.endsWith('/')) return '移除前缀要求路径以 / 结尾，避免生成双斜线 URI。';
      if (r.strip && r.match === 'exact') return '本原型不支持精确匹配与移除前缀组合，请保留原 URI。';
    }
    if (r.kind === 'redirect' && !/^https?:\/\/[^\s"{};]+$/.test(r.value)) return '跳转目标必须是完整 HTTP / HTTPS URL。';
    if (r.kind === 'return' && r.value.includes('$')) return '固定响应不支持变量，请使用字面量文本或转到文件编辑。';
    return '';
  }
  function applyRoute() {
    const err = checkRoute(routeForm);
    if (err) return showRouteForm(err);
    const i = work.routes.findIndex(x => x.id === routeForm.id);
    if (i >= 0) work.routes[i] = clone(routeForm);else work.routes.push(clone(routeForm));
    closeModal(true);
    invalidate();
    render();
    toast('Route 已写入当前工作草稿，尚未保存或发布。');
  }
  function tokenCheck(text) {
    let depth = 0,
      quote = null,
      escape = false,
      comment = false;
    const issues = [];
    for (const c of text) {
      if (comment) {
        if (c === '\n') comment = false;
        continue;
      }
      if (escape) {
        escape = false;
        continue;
      }
      if (c === '\\') {
        escape = true;
        continue;
      }
      if (quote) {
        if (c === quote) quote = null;
        continue;
      }
      if (c === '#') {
        comment = true;
        continue;
      }
      if (c === '"' || c === "'") {
        quote = c;
        continue;
      }
      if (c === '{') depth++;
      if (c === '}') {
        depth--;
        if (depth < 0) {
          issues.push('存在多余的右花括号 }。');
          depth = 0;
        }
      }
    }
    if (quote) issues.push('存在未闭合的引号。');
    if (depth) issues.push('花括号未闭合。');
    return issues;
  }
  function validateConfig() {
    const errors = [],
      warnings = [],
      d = D();
    if (stale()) errors.push('草稿基线过期或另一标签页已更新，必须先处理版本冲突。');
    if (d.drift && !d.drift.reviewed) errors.push('存在外部配置漂移，必须先明确处理该差异。');
    if (d.gateways.some(offline)) errors.push('目标 Gateway 存在未知状态，当前演示的全量发布被阻断。');
    if (d.drift?.reviewed) warnings.push('外部漂移已采用为原生草稿；发布前必须确认覆盖范围，实际运行状态仍待核实。');
    if (work.mode === 'managed') {
      if (!/^\d+[kKmM]?$/.test(work.body)) errors.push('请求体上限格式错误：请使用数字及可选 k/m 单位。');
      if (work.tls && d.rev === 0 && !d.certReadyDemo) errors.push('当前域名没有已选择的演示证书。请先关闭 HTTPS，或在复制向导中明确选择目标域名证书；真实签发未接入。');
      for (const r of work.routes) {
        const err = checkRoute(r);
        if (err) errors.push(__nscVueAdd(__nscVueAdd(r.path, '：'), err));
        if (r.enabled && r.kind === 'proxy' && !ready(r.service).length) errors.push(__nscVueAdd(r.path, '：目标 Service 没有 Ready Endpoint。'));
        if (r.enabled && r.match === 'prefix' && r.path !== '/' && !r.path.endsWith('/')) warnings.push(__nscVueAdd(r.path, ' 也会匹配以它开头的其他路径；确认是否需要结尾 /。'));
        if (r.enabled && r.kind === 'return' && r.code === 204 && r.value) warnings.push(__nscVueAdd(r.path, '：204 响应不返回正文。'));
      }
      const snippets = [['补充域名指令', work.serverSnippet], ...work.routes.map(r => [__nscVueAdd(r.path, ' 扩展'), r.snippet])];
      for (const [label, text] of snippets) {
        const clean = text.replace(/#.*$/gm, '').trim();
        if (!clean) continue;
        const protectedDirective = /(^|;)\s*(listen|server_name|location|server|upstream|ssl_certificate(?:_key)?|proxy_pass|proxy_read_timeout|proxy_connect_timeout|client_max_body_size|proxy_set_header)\b/m;
        if (protectedDirective.test(clean)) errors.push(__nscVueAdd(label, ' 包含托管指令，请在所属 Domain / Route 表单修改。'));
        if (/[{}]/.test(clean)) errors.push(__nscVueAdd(label, '：扩展仅支持平面指令，块级配置请使用文件编辑。'));
        if (clean.split('\n').some(l => l.trim() && !l.trim().endsWith(';'))) errors.push(__nscVueAdd(label, '：每条指令须以分号结尾。'));
        const known = ['add_header', 'gzip', 'gzip_types', 'expires', 'proxy_buffering', 'proxy_buffers', 'proxy_buffer_size', 'client_body_timeout', 'keepalive_timeout', 'client_header_timeout'];
        for (const l of clean.split('\n')) {
          const directive = l.trim().split(/\s+/)[0];
          if (directive && !known.includes(directive)) warnings.push(__nscVueAdd(__nscVueAdd(__nscVueAdd(label, '：指令 '), directive), ' 未做语义验证，必须在目标 NGINX 中检查。'));
        }
        for (const e of tokenCheck(text)) errors.push(__nscVueAdd(__nscVueAdd(label, '：'), e));
      }
    } else {
      if (!work.native.trim()) errors.push('手动配置文件不能为空。');
      for (const e of tokenCheck(work.native)) errors.push(__nscVueAdd('配置文本：', e));
      warnings.push('手动配置文件仅检查基础结构，不推断其完整路由、作用域或安全性。');
    }
    const hash = signature();
    ui.validation = {
      errors: [...new Set(errors)],
      warnings: [...new Set(warnings)],
      hash,
      time: stamp()
    };
    event('执行本地规则检查', errors.length ? __nscVueAdd(errors.length, ' 项阻断错误') : '本地规则无阻断；未运行 nginx -t', errors.length ? 'warning' : 'check');
    return ui.validation;
  }
  function validationModal() {
    const v = validateConfig();
    refreshHeader();
    const good = !v.errors.length;
    showModal({
      kind: 'validation',
      title: good ? '本地规则检查完成' : '检测到需要处理的问题',
      sub: __nscVueAdd(__nscVueAdd('仅检查演示模型 · ', v.time), ' UTC+8'),
      body: __nscVueAdd(callout(__nscVueView(templates[143], []), 'amber'), __nscVueView(templates[144], [__nscVueJoin(v.errors.map(e => validationItem(e, '阻断发布', 'red', 'warning')), ''), good ? validationItem('当前支持范围内未发现阻断性问题', '基础格式、Route 冲突、Ready 目标、版本及所有权检查', 'green', 'checkcircle') : '', __nscVueJoin(v.warnings.map(e => validationItem(e, '需要在真实 Gateway 中进一步验证', 'amber', 'warning')), ''), validationItem('真实 NGINX 校验', '未执行 · 接入后端后应由目标环境完成', 'muted', 'info')])),
      footer: __nscVueAdd(B('返回编辑', 'close'), B('查看影响', 'impact', 'compare', 'primary', good ? '' : 'disabled'))
    });
  }
  function validationItem(...args) {
    return __domainsViews.validationItem_31(__renderContext0, ...args);
  }
  function diffLines(a, b) {
    const aa = a.split('\n'),
      bb = b.split('\n');
    if (aa.length > 600 || bb.length > 600) return [{
      type: 'remove',
      text: a
    }, {
      type: 'add',
      text: b
    }];
    const m = aa.length,
      n = bb.length,
      dp = Array.from({
        length: __nscVueAdd(m, 1)
      }, () => new Uint16Array(__nscVueAdd(n, 1)));
    for (let i = m - 1; i >= 0; i--) for (let j = n - 1; j >= 0; j--) dp[i][j] = aa[i] === bb[j] ? __nscVueAdd(1, dp[__nscVueAdd(i, 1)][__nscVueAdd(j, 1)]) : Math.max(dp[__nscVueAdd(i, 1)][j], dp[i][__nscVueAdd(j, 1)]);
    const out = [];
    let i = 0,
      j = 0;
    while (i < m || j < n) {
      if (i < m && j < n && aa[i] === bb[j]) {
        out.push({
          type: 'same',
          text: aa[i]
        });
        i++;
        j++;
      } else if (j < n && (i === m || dp[i][__nscVueAdd(j, 1)] > dp[__nscVueAdd(i, 1)][j])) {
        out.push({
          type: 'add',
          text: bb[j++]
        });
      } else out.push({
        type: 'remove',
        text: aa[i++]
      });
    }
    return out;
  }
  function diffHTML(...args) {
    return __domainsViews.diffHTML_32(__renderContext0, ...args);
  }
  function impactModal(publish = false) {
    const changes = summarizeChanges(),
      s = syncInfo(),
      v = ui.validation;
    const valid = v && v.hash === signature() && !v.errors.length && !stale() && (!D().drift || D().drift.reviewed) && !D().gateways.some(offline);
    if (publish && !valid) {
      validationModal();
      return;
    }
    if (publish && !changed()) {
      toast('与期望版本无差异，不生成空发布。');
      return;
    }
    showModal({
      kind: publish ? 'publish-confirm' : 'impact',
      size: 'wide',
      title: publish ? '确认本次模拟发布' : '草稿变更与影响范围',
      sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(esc(D().name), ' · 基线 v'), D().draft?.base ?? D().rev), ' → 新 Revision'),
      body: __nscVueView(templates[151], [changes.length, D().gateways.length, work.mode === 'native' ? '未知' : new Set(work.routes.filter(r => r.kind === 'proxy').map(r => r.service)).size, work.mode === 'native' ? '手动配置文件完整依赖范围' : '候选关联 Service', callout(publish ? __nscVueView(templates[148], []) : '所有变化来自当前工作草稿；未测量流量，不推断停机时间或受影响请求量。', publish ? 'amber' : 'blue'), changesHTML(changes, 12), __nscVueJoin(D().gateways.map(id => __nscVueView(templates[149], [I('server'), G(id).name, G(id).ip, ENV[G(id).env], badge(offline(id) ? '未知' : '已选目标', offline(id) ? 'amber' : 'blue')])), ''), callout(work.mode === 'native' ? '手动配置文件可能影响未知资源；本原型无法精确推断完整影响范围。' : 'Service Endpoint 是演示快照；真实控制面必须在冻结 Snapshot 时重新解析依赖。', 'neutral'), diffHTML(renderConfig(D().active), renderConfig(work)), publish ? __nscVueView(templates[150], [esc(changes[0]?.title || '配置变更'), D().gateways.length]) : '']),
      footer: __nscVueAdd(B('返回编辑', 'close', '', 'ghost'), publish ? B('冻结并模拟发布', 'release-start', 'send', 'primary', 'disabled id="confirm-publish"') : B(valid ? '进入发布确认' : '先完成校验', valid ? 'publish' : 'validate', valid ? 'send' : 'checkcircle', 'primary', changed() ? '' : 'disabled'))
    });
  }
  function startRelease() {
    if (!document.querySelector('#publish-ack')?.checked) return;
    const v = ui.validation;
    if (!v || v.hash !== signature() || v.errors.length || stale() || D().drift && !D().drift.reviewed || D().gateways.some(offline)) {
      toast('草稿或发布前提已经变化，请重新校验。', true);
      return validationModal();
    }
    if (!saveDraft(true)) return;
    const d = D(),
      previous = d.rev,
      newVersion = __nscVueAdd(Math.max(d.rev, ...d.revisions.map(r => r.v)), 1);
    const note = document.querySelector('#release-note')?.value.trim() || '配置变更';
    const config = clone(work);
    d.rev = newVersion;
    d.active = clone(config);
    d.revisions.unshift({
      v: newVersion,
      config: clone(config),
      time: stamp(),
      note
    });
    d.draft = null;
    work = clone(config);
    const dep = {
      id: __nscVueAdd(__nscVueAdd(__nscVueAdd('DEP-', newVersion), '-'), Date.now().toString(36).slice(-4).toUpperCase()),
      domain: d.id,
      version: newVersion,
      previous,
      time: stamp(),
      status: 'running',
      targets: [...d.gateways],
      results: Object.fromEntries(d.gateways.map(id => [id, 'pending'])),
      logs: [__nscVueInterpolate(["[demo] freeze revision v", " \xB7 ", ""], [newVersion, note])],
      note
    };
    db.deployments.unshift(dep);
    event(__nscVueAdd('冻结并开始模拟发布 v', newVersion), __nscVueJoin(dep.targets.map(id => G(id).name), ' / '), 'send');
    persist();
    ui.validation = null;
    simulateDeployment(dep, false);
  }
  function simulateDeployment(dep, retry) {
    if (deploymentTimer) return;
    dep.status = 'running';
    let stepIndex = 0;
    const target = dep.targets.filter(id => !retry || dep.results[id] !== 'success');
    const labels = ['冻结内容与目标', '模拟节点校验', '模拟应用候选配置', '模拟核实生效版本'];
    showDeployment(dep, 0, true);
    deploymentTimer = setInterval(() => {
      stepIndex++;
      dep.logs.push(__nscVueInterpolate(["[demo] ", " \xB7 ", ""], [labels[Math.min(stepIndex, 3)], __nscVueJoin(target.map(id => G(id).name), ', ')]));
      if (stepIndex >= 3) {
        const d = db.domains.find(x => x.id === dep.domain);
        for (const id of target) {
          const fail = !retry && db.scenario === 'partial' && id === dep.targets.at(-1);
          dep.results[id] = fail ? 'failed' : 'success';
          if (!fail) {
            d.loaded[id] = dep.version;
            if (d.drift?.gateway === id) d.drift = null;
          }
        }
        dep.status = Object.values(dep.results).every(x => x === 'success') ? 'success' : 'partial';
        dep.logs.push(dep.status === 'success' ? '[demo] all selected gateways confirmed; no real request was sent.' : '[demo] last gateway failed; its previous known configuration was retained.');
        event(dep.status === 'success' ? __nscVueAdd(__nscVueAdd('v', dep.version), ' 模拟发布已确认') : __nscVueAdd(__nscVueAdd('v', dep.version), ' 模拟发布部分失败'), dep.status === 'success' ? '全部目标显示演示确认结果' : '失败节点保留旧版本；可重试固定 Revision', dep.status === 'success' ? 'check' : 'warning', dep.domain);
        clearInterval(deploymentTimer);
        deploymentTimer = null;
        persist();
        render();
        showDeployment(dep, 4, false);
      } else showDeployment(dep, stepIndex, true);
    }, 620);
  }
  function showDeployment(dep, stage = 4, running = false) {
    showModal({
      kind: 'deployment',
      deployment: dep.id,
      locked: running,
      size: 'wide',
      title: running ? __nscVueAdd('正在模拟发布 v', dep.version) : dep.status === 'success' ? '模拟发布已完成' : '部分节点未完成发布',
      sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(dep.id, ' · 固定 Revision v'), dep.version), ' · '), esc(db.domains.find(d => d.id === dep.domain).name)),
      body: __nscVueAdd(callout('演示状态机：仅修改浏览器里的记录，没有执行真实校验、reload 或网络请求。', 'neutral'), __nscVueView(templates[154], [__nscVueJoin(['冻结 Revision', '节点校验', '应用配置', '生效核实'].map((t, i) => step(String(__nscVueAdd(i, 1)), t, i < stage ? 'done' : i === stage ? 'now' : '')), ''), running ? Math.max(8, stage * 27) : 100, __nscVueJoin(dep.targets.map(id => __nscVueView(templates[152], [I('server'), G(id).name, dep.version, G(id).ip, badge(dep.results[id] === 'success' ? __nscVueAdd('已确认 v', dep.version) : dep.results[id] === 'failed' ? '失败 · 保留旧版本' : '等待模拟结果', dep.results[id] === 'success' ? 'green' : dep.results[id] === 'failed' ? 'red' : 'blue')])), ''), esc(__nscVueJoin(dep.logs, '\n')), !running && dep.status === 'partial' ? __nscVueView(templates[153], [callout('保留已经成功的节点。重试仅面向失败目标，复用本次不可变 Revision；不会重新读取后续草稿。', 'amber')]) : ''])),
      footer: running ? __nscVueView(templates[155], []) : __nscVueAdd(B('关闭', 'close'), dep.status === 'partial' ? __nscVueAdd(B('恢复旧版为草稿', 'restore-deployment', 'history', '', __nscVueInterpolate(["data-id=\"", "\""], [dep.id])), B('仅重试失败节点', 'retry-deployment', 'refresh', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [dep.id]))) : B('查看运维状态', 'go-ops', 'server', 'primary', __nscVueInterpolate(["data-domain=\"", "\""], [dep.domain])))
    });
  }
  function retryDeployment(id) {
    const dep = db.deployments.find(x => x.id === id);
    const d = db.domains.find(x => x.id === dep?.domain);
    if (!dep || dep.status !== 'partial') return;
    if (d.rev !== dep.version) {
      toast('当前期望版本已前进，不能重试过期任务。', true);
      return;
    }
    if (d.drift || dep.targets.some(offline)) {
      toast('存在漂移或未知状态，请先处理再重试。', true);
      return;
    }
    event(__nscVueAdd('重试固定 Revision v', dep.version), '仅面向失败目标', 'refresh', dep.domain);
    simulateDeployment(dep, true);
  }
  function serviceDetail(id) {
    const s = S(id);
    if (!s) {
      toast('此规则没有关联代理 Service。');
      return;
    }
    const refs = db.domains.flatMap(d => (d.draft?.config || d.active).routes.filter(r => r.service === id).map(r => ({
      d,
      r
    })));
    showModal({
      size: 'drawer',
      title: esc(s.id),
      sub: '逻辑 Service / 只读服务发现',
      body: __nscVueAdd(callout('Endpoint 是发现结果；NSC 的入口托管不会更新镜像、重启或删除这个 Service。', 'neutral'), __nscVueView(templates[158], [ENV[s.env], s.runtime, esc(s.image), s.port, I('layers'), __nscVueJoin(s.endpoints.map(([ip, ok]) => __nscVueView(templates[156], [ip, s.port, badge(ok ? 'Ready' : 'Not Ready', ok ? 'green' : 'amber'), ok ? '加入' : '排除'])), ''), I('route'), __nscVueJoin(refs.map(({
        d,
        r
      }) => __nscVueView(templates[157], [esc(d.name), esc(r.path), MATCH[r.match], TB('打开 Route', 'resource-route', 'right', __nscVueInterpolate(["data-domain=\"", "\" data-id=\"", "\""], [d.id, r.id]))])), '')])),
      footer: B('关闭', 'close')
    });
  }
  function gatewayDetail(id) {
    const g = G(id);
    if (!g) return;
    const ds = db.domains.filter(d => d.gateways.includes(id));
    showModal({
      size: 'drawer',
      title: esc(g.name),
      sub: __nscVueAdd(__nscVueAdd(g.ip, ' / '), g.runtime),
      body: __nscVueAdd(callout('这里展示每个 Domain 在此节点上的演示版本切片。实际部署必须以完整 Gateway Snapshot 为原子单位。', 'neutral'), __nscVueView(templates[160], [I('globe'), __nscVueJoin(ds.map(d => __nscVueView(templates[159], [d.id, esc(d.name), d.rev, offline(id) ? badge('未知', 'amber') : badge(__nscVueAdd('v', d.loaded[id]), d.loaded[id] === d.rev ? 'green' : 'amber')])), ''), I('lock')])),
      footer: B('关闭', 'close')
    });
  }
  function domainSettings() {
    const d = D();
    showModal({
      size: 'drawer',
      title: 'Domain 身份与入口范围',
      sub: __nscVueAdd(esc(d.name), ' / 不等于 Site 或 Service'),
      body: __nscVueView(templates[162], [esc(d.name), ENV[d.env], d.id, d.created, work.mode === 'native' ? '文件编辑' : '表单配置 + 自定义指令', I('server'), __nscVueJoin(d.gateways.map(id => __nscVueView(templates[161], [I('server'), G(id).name, G(id).ip, ENV[G(id).env], badge('当前目标', 'blue')])), ''), callout('此原型不提供已发布域名重命名和迁移 Gateway 的写操作。它们属于入口迁移流程，不能伪装成无风险字段编辑。', 'amber')]),
      footer: __nscVueAdd(B('关闭', 'close'), B('编辑入口配置', 'settings-to-config', 'code', 'primary'))
    });
  }
  function addDomainModal(error = '') {
    showModal({
      kind: 'add-domain',
      size: 'small',
      title: '添加 Domain',
      sub: '创建入口草稿，不立即影响任何 Gateway。',
      body: __nscVueView(templates[165], [esc(modal?.newName || ''), ENV[db.env], __nscVueJoin(db.services.filter(s => s.env === db.env && s.project === db.project).map(s => __nscVueView(templates[163], [s.id, s.id])), ''), callout('使用当前环境的演示 Gateway，先创建 HTTP :80 入口。证书签发没有接入，新 Domain 默认不启用 HTTPS。', 'neutral'), error ? __nscVueView(templates[164], [esc(error)]) : '']),
      footer: __nscVueAdd(B('取消', 'close'), B('创建并编辑草稿', 'domain-create', 'plus', 'primary'))
    });
  }
  function createDomain() {
    const name = document.querySelector('#new-domain-name').value.trim().toLowerCase(),
      svc = document.querySelector('#new-domain-service').value;
    const invalid = name.length > 253 || !name.includes('.') || !name.split('.').every(l => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(l));
    if (invalid) {
      modal.newName = name;
      return addDomainModal('请输入合法的 ASCII 完整域名，例如 preview.example.com。');
    }
    const gs = db.gateways.filter(g => gatewayAllowed(g, db.env)).map(g => g.id);
    if (db.domains.some(d => d.name === name && d.gateways.some(g => gs.includes(g)))) {
      modal.newName = name;
      return addDomainModal('相同 Domain 已占用当前 Gateway 的入口范围。');
    }
    const c = {
      mode: 'managed',
      tls: false,
      redirect: false,
      body: '32m',
      serverSnippet: '',
      native: '',
      routes: []
    };
    const d = {
      id: uid('domain'),
      name,
      env: db.env,
      gateways: gs,
      rev: 0,
      active: clone(c),
      loaded: Object.fromEntries(gs.map(g => [g, 0])),
      draft: null,
      drift: null,
      created: stamp(),
      certExpiry: '—',
      revisions: [{
        v: 0,
        config: clone(c),
        time: stamp(),
        note: '尚未发布的空入口基线'
      }]
    };
    db.domains.push(d);
    closeModal(true);
    ui.domain = d.id;
    work = clone(c);
    work.routes.push(route(uid('r'), '/', __nscVueAdd('', svc)));
    d.draft = {
      base: 0,
      config: clone(work),
      savedAt: stamp()
    };
    event('创建 Domain 草稿', __nscVueAdd(name, ' · 尚未发布'), 'plus', d.id);
    persist();
    ui.page = 'domain';
    ui.tab = 'overview';
    ui.selectedRoute = work.routes[0].id;
    ui.validation = null;
    ui.diag = null;
    ui.diagUrl = __nscVueAdd(__nscVueAdd('http://', name), '/');
    updateHash();
    render();
    toast('Domain 草稿已创建；没有向 Gateway 下发任何内容。');
  }
  function ownershipModal(native = true) {
    showModal({
      size: 'small',
      title: native ? '改为直接编辑配置文件？' : '改用表单配置…？',
      sub: __nscVueAdd(esc(D().name), ' / 只改变当前草稿的管理方式'),
      body: __nscVueAdd(callout(native ? '切换后，手动配置文件文本成为此 Domain 范围的唯一写入源。' : '这不是把任意手动配置文件解析回表单。系统将用保留的结构化资源重新生成配置。', 'amber'), __nscVueView(templates[166], [native ? '当前渲染结果将作为原生草稿的起点。' : '当前配置文本改动将从候选配置中移除。', native ? 'Route / Domain 配置表单将只读保留。' : '原来的 Route 及扩展保留；不会猜测原生指令的含义。'])),
      footer: __nscVueAdd(B('取消', 'close'), B(native ? '确认接管此 Domain' : '确认重建结构化草稿', native ? 'native-confirm' : 'managed-confirm', 'check', 'primary'))
    });
  }
  function driftModal() {
    const dr = D().drift;
    if (!dr) {
      toast('当前 Domain 没有外部漂移。');
      return;
    }
    showModal({
      size: 'wide',
      kind: 'drift',
      title: '外部修改与期望配置不一致',
      sub: __nscVueAdd(__nscVueAdd(G(dr.gateway).name, ' · '), esc(D().name)),
      body: __nscVueAdd(callout('不把外部文件直接写成“已发布版本”。先审阅差异，再选择恢复期望或采用为新草稿。', 'amber'), __nscVueView(templates[167], [diffHTML(renderConfig(D().active), dr.content)])),
      footer: __nscVueAdd(__nscVueAdd(B('暂不处理', 'close', '', 'ghost'), B('恢复期望配置', 'drift-restore', 'history')), B('采用为原生草稿', 'drift-adopt', 'edit', 'primary'))
    });
  }
  function conflictModal() {
    showModal({
      kind: 'conflict',
      size: 'wide',
      title: '草稿基线与当前期望版本冲突',
      sub: __nscVueInterpolate(["\u8349\u7A3F\u57FA\u4E8E v", " \xB7 \u5F53\u524D\u671F\u671B v", ""], [D().draft?.base ?? D().rev, D().rev]),
      body: __nscVueAdd(callout('不会自动覆盖他人的更新。请比较当前期望版本与旧草稿，并明确选择保留哪一份内容。', 'red'), __nscVueView(templates[168], [diffHTML(renderConfig(D().active), renderConfig(work))])),
      footer: __nscVueAdd(__nscVueAdd(B('暂不处理', 'close', '', 'ghost'), B('放弃旧稿，采用期望版本', 'conflict-current')), B('确认保留整个工作草稿', 'conflict-rebase', 'check', 'primary', 'disabled id="rebase-button"'))
    });
  }
  function scenarioModal() {
    showModal({
      size: 'wide',
      title: '切换演示场景',
      sub: '只改变本机示例状态；不会连接真实集群。',
      body: __nscVueView(templates[170], [__nscVueJoin([['normal', 'checkcircle', '正常运行', '清除演示漂移与失联，保留现有配置和发布记录。'], ['partial', 'server', '发布部分失败', '下一次发布中，最后一个 Gateway 失败并保留旧版本。'], ['offline', 'activity', 'Gateway 失联', '最后一个目标生效状态未知，禁止按全正常状态发布。'], ['drift', 'compare', '外部配置漂移', '模拟有人改动最后一个节点的文件；需要显式处理。'], ['conflict', 'users', '并发版本冲突', '模拟另一编辑者发布新版；旧草稿基线过期。']].map(([id, ic, t, p]) => __nscVueView(templates[169], [db.scenario === id ? 'active' : '', id, I(ic), t, p])), ''), I('refresh'), callout(__nscVueAdd('场景用于检验边界和恢复路径，不表示真实的集群健康状态。当前作用域：', esc(D().name)), 'neutral')]),
      footer: B('关闭', 'close')
    });
  }
  function setScenario(value) {
    if (deploymentTimer) return;
    const d = D();
    db.scenario = value;
    db.scenarioGateway = value === 'offline' ? d.gateways.at(-1) : null;
    ui.validation = null;
    if (['normal', 'partial', 'offline'].includes(value)) d.drift = null;
    if (value === 'drift') {
      const content = __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(renderConfig(d.active), '\n# external change on '), G(d.gateways.at(-1)).name), '\nserver {\n    listen 8088;\n    server_name '), d.name), ';\n    return 403;\n}\n');
      d.drift = {
        gateway: d.gateways.at(-1),
        content,
        reviewed: false,
        time: stamp()
      };
    }
    if (value === 'conflict') {
      if (!d.draft) d.draft = {
        base: d.rev,
        config: clone(work),
        savedAt: stamp()
      };
      const conf = clone(d.active);
      if (conf.mode === 'native') conf.native = __nscVueAdd(conf.native, '\n# changed by another editor\n');else conf.body = conf.body === '64m' ? '128m' : '64m';
      d.rev = __nscVueAdd(Math.max(d.rev, ...d.revisions.map(r => r.v)), 1);
      d.active = conf;
      d.revisions.unshift({
        v: d.rev,
        config: clone(conf),
        time: stamp(),
        note: '演示：另一编辑者的修改'
      });
      d.gateways.forEach(id => d.loaded[id] = d.rev);
    }
    event('切换演示场景', value, 'warning');
    persist();
    closeModal(true);
    render();
    toast(__nscVueAdd('已切换场景：', {
      normal: '正常运行',
      partial: '下一次发布部分失败',
      offline: '目标节点失联',
      drift: '外部配置漂移',
      conflict: '草稿版本冲突'
    }[value]));
  }
  function helpModal() {
    showModal({
      size: 'wide',
      title: 'NSC 交互原型 / 使用指南',
      sub: '同一资源模型、同一工作草稿，两个工作视角。',
      body: __nscVueView(templates[171], [callout('该交付是可离线运行的产品交互原型，不是生产管理后台。所有基础设施状态和发布步骤都是模拟。', 'neutral'), callout('本原型不执行 nginx -t、DNS、ACME、容器管理或真实 reload。不包含认证授权后端，不应作为公网生产管理工具直接上线。', 'amber')]),
      footer: __nscVueAdd(B('关闭', 'close'), B('导出工作区 JSON', 'export-state', 'download'))
    });
  }
  function palette() {
    if (modal?.kind === 'route' && !equal(routeForm, routeDraftSnapshot)) {
      toast('请先应用或取消路由表单的修改。', true);
      return;
    }
    showModal({
      kind: 'palette',
      size: 'small',
      body: __nscVueView(templates[172], [paletteResults('')])
    });
  }
  function paletteResults(...args) {
    return __shellViews.paletteResults_33(__renderContext0, ...args);
  }
  function download(name, text, type = 'text/plain;charset=utf-8') {
    const blob = new Blob([text], {
        type
      }),
      url = URL.createObjectURL(blob),
      a = document.createElement('a');
    a.href = url;
    a.download = name;
    document.body.append(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 5000);
  }
  function restoreRevision(v) {
    const r = D().revisions.find(r => r.v === Number(v));
    if (!r) return;
    showModal({
      size: 'small',
      title: __nscVueAdd(__nscVueAdd('恢复 v', r.v), ' 为新草稿？'),
      sub: esc(D().name),
      body: callout('恢复不会直接回滚生产，也不会改写历史记录。当前工作草稿将被此历史快照替换，之后仍需校验和发布。', 'amber'),
      footer: __nscVueAdd(B('取消', 'close'), B('确认恢复为草稿', 'restore-confirm', 'history', 'primary', __nscVueInterpolate(["data-version=\"", "\""], [r.v])))
    });
  }
  // Central action router. UI never makes fetch/XHR calls.
  listen(document, 'click', e => {
    const target = e.target.closest('[data-action]');
    if (!target || target.disabled) return;
    const a = target.dataset.action,
      id = target.dataset.id,
      val = target.dataset.value;
    switch (a) {
      case 'nav':
        closeModal(true);
        navigate(target.dataset.page);
        break;
      case 'open-domain':
        navigate('domain', target.dataset.tab || 'overview', id);
        break;
      case 'resource-domain':
        closeModal(true);
        navigate('domain', 'overview', id);
        break;
      case 'resource-route':
        {
          const rid = id,
            did = target.dataset.domain;
          closeModal(true);
          ensureNavigate(() => {
            navigate('domain', 'routes', did, false);
            openRoute(rid);
          });
          break;
        }
      case 'open-example':
        {
          const d = db.domains.find(x => x.name === 'example.com');
          navigate('domain', 'overview', d.id);
          break;
        }
      case 'menu':
        ui.menu = !ui.menu;
        render();
        break;
      case 'view':
        db.view = target.dataset.view;
        if (!ui.externalConflict) persist();
        ui.menu = false;
        render();
        toast(db.view === 'cloud' ? '已切换 Cloud Native；当前草稿保持不变。' : '已切换运维视角；当前草稿保持不变。');
        break;
      case 'tab':
        ui.tab = target.dataset.tab;
        updateHash();
        render();
        break;
      case 'save':
        saveDraft();
        render();
        break;
      case 'validate':
        validationModal();
        break;
      case 'impact':
        impactModal();
        break;
      case 'publish':
        impactModal(true);
        break;
      case 'close':
        closeModal();
        break;
      case 'backdrop':
        if (e.target === target) closeModal();
        break;
      case 'discard-leave':
        {
          const fn = modal.next;
          work = clone(workingSaved());
          closeModal(true);
          fn();
          break;
        }
      case 'save-leave':
        {
          const fn = modal.next;
          if (saveDraft(true)) {
            closeModal(true);
            fn();
          }
          break;
        }
      case 'route-add':
        openRoute('');
        break;
      case 'route-edit':
        if (target.dataset.domain && (allEnvironmentView() || target.dataset.domain !== ui.domain)) {
          ensureNavigate(() => {
            const d = db.domains.find(d => d.id === target.dataset.domain);
            if (d) hooks.navigate(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('/projects/', enc(d.project)), '/environments/'), enc(d.env)), '/domains/'), enc(d.id)), '?tab=routes')).then(failure => {
              if (!failure) openRoute(id);
            });
          });
        } else openRoute(id);
        break;
      case 'route-kind':
        routeForm.kind = val;
        if (val === 'return') {
          routeForm.code = 200;
          routeForm.value = 'ok';
        }
        if (val === 'redirect') {
          routeForm.code = 308;
          routeForm.value = __nscVueAdd(__nscVueAdd('https://', D().name), '/');
        }
        showRouteForm();
        break;
      case 'route-toggle':
        routeForm[target.dataset.key] = !routeForm[target.dataset.key];
        showRouteForm();
        break;
      case 'route-apply':
        applyRoute();
        break;
      case 'route-resume':
        showRouteForm();
        break;
      case 'route-discard':
        closeModal(true);
        break;
      case 'route-delete':
        showModal({
          kind: 'route-delete',
          size: 'small',
          title: '从草稿删除此 Route？',
          sub: esc(routeForm.path),
          body: callout('删除后该请求可能命中其他前缀或兜底规则。确认后仍需保存、校验和发布。', 'amber'),
          footer: __nscVueAdd(B('返回编辑', 'route-resume'), B('确认删除', 'route-delete-confirm', 'trash', 'danger'))
        });
        break;
      case 'route-delete-confirm':
        work.routes = work.routes.filter(r => r.id !== routeForm.id);
        ui.selectedRoute = work.routes[0]?.id;
        closeModal(true);
        invalidate();
        render();
        toast('Route 已从工作草稿移除，尚未影响运行配置。');
        break;
      case 'route-code':
        ui.selectedRoute = id;
        ui.tab = 'config';
        ui.file = 'generated';
        ui.configView = 'code';
        updateHash();
        render();
        break;
      case 'config-view':
        ui.configView = val;
        render();
        break;
      case 'file':
        ui.file = target.dataset.file;
        ui.tab = 'config';
        ui.configView = 'code';
        updateHash();
        render();
        break;
      case 'config-toggle':
        if (work.mode === 'native') {
          toast('文件编辑范围请编辑文本。', true);
          break;
        }
        work[target.dataset.key] = !work[target.dataset.key];
        if (!work.tls) work.redirect = false;
        invalidate();
        render();
        break;
      case 'native-mode':
        ownershipModal(true);
        break;
      case 'managed-mode':
        ownershipModal(false);
        break;
      case 'native-confirm':
        work.native = renderConfig(work);
        work.mode = 'native';
        ui.file = 'native';
        ui.configView = 'code';
        ui.tab = 'config';
        closeModal(true);
        invalidate();
        render();
        toast('文件编辑已写入工作草稿，图形表单不会覆盖它。');
        break;
      case 'managed-confirm':
        work.mode = 'managed';
        work.native = '';
        ui.file = 'generated';
        ui.configView = 'semantic';
        closeModal(true);
        invalidate();
        render();
        toast('已用保留的结构化资源重建草稿，请检查差异。');
        break;
      case 'format-code':
        {
          const input = document.querySelector('#code-input');
          if (input) {
            input.value = __nscVueJoin(input.value.split('\n').map(l => l.trimEnd()), '\n').replace(/\n{4,}/g, '\n\n\n');
            input.dispatchEvent(new Event('input', {
              bubbles: true
            }));
            toast('已清理行尾空白；不重排指令或改写语义。');
          }
          break;
        }
      case 'copy-config':
        {
          const text = renderConfig(work);
          if (navigator.clipboard?.writeText) navigator.clipboard.writeText(text).then(() => toast('已复制完整渲染配置。')).catch(() => copyFallback(text));else copyFallback(text);
          break;
        }
      case 'export':
        download(__nscVueAdd(D().name, '.draft.conf'), renderConfig(work));
        toast('已导出当前草稿配置，不代表已经发布。');
        break;
      case 'export-state':
        download('nsc-workspace-demo.json', JSON.stringify({
          schema: 2,
          exportedAt: stamp(),
          demo: true,
          db,
          unsavedWorkingCopy: {
            domain: ui.domain,
            config: work
          }
        }, null, 2), 'application/json');
        toast('已导出工作区与当前未保存草稿。');
        break;
      case 'service-detail':
        serviceDetail(id);
        break;
      case 'gateway-detail':
        gatewayDetail(id);
        break;
      case 'domain-settings':
        domainSettings();
        break;
      case 'settings-to-config':
        closeModal(true);
        ui.tab = 'config';
        ui.configView = 'semantic';
        updateHash();
        render();
        break;
      case 'domain-add':
        ensureNavigate(() => addDomainModal());
        break;
      case 'domain-create':
        createDomain();
        break;
      case 'diagnose':
        showModal({
          kind: 'diagnose',
          title: '模拟请求匹配',
          sub: __nscVueAdd(esc(D().name), ' · 不执行真实 HTTP 请求'),
          size: 'small',
          body: diagnosticHTML(),
          footer: B('关闭', 'close')
        });
        break;
      case 'run-diag':
        runDiagnostic();
        break;
      case 'release-start':
        startRelease();
        break;
      case 'deployment-detail':
        {
          const dep = db.deployments.find(x => x.id === id);
          if (dep) showDeployment(dep);
          break;
        }
      case 'retry-deployment':
        retryDeployment(id);
        break;
      case 'go-ops':
        {
          const did = target.dataset.domain || ui.domain;
          closeModal(true);
          db.view = 'ops';
          navigate('domain', 'overview', did);
          break;
        }
      case 'restore-deployment':
        {
          const dep = db.deployments.find(x => x.id === id);
          if (dep) {
            closeModal(true);
            ensureNavigate(() => {
              navigate('domain', 'history', dep.domain, false);
              restoreRevision(dep.previous);
            });
          }
          break;
        }
      case 'revision-view':
        {
          const r = D().revisions.find(x => x.v === Number(target.dataset.version));
          if (r) showModal({
            size: 'wide',
            title: __nscVueAdd(__nscVueAdd('Revision v', r.v), ' / 不可变快照'),
            sub: __nscVueAdd(__nscVueAdd(r.time, ' UTC+8 · '), esc(r.note)),
            body: codeHTML(renderConfig(r.config)),
            footer: B('关闭', 'close')
          });
          break;
        }
      case 'restore-revision':
        restoreRevision(target.dataset.version);
        break;
      case 'restore-confirm':
        {
          const r = D().revisions.find(x => x.v === Number(target.dataset.version));
          if (r) {
            work = clone(r.config);
            D().draft = {
              base: D().rev,
              config: clone(work),
              savedAt: stamp()
            };
            event(__nscVueAdd(__nscVueAdd('从 v', r.v), ' 恢复草稿'), '尚未改变运行目标', 'history');
            persist();
            closeModal(true);
            ui.validation = null;
            ui.tab = 'config';
            ui.configView = 'code';
            ui.file = work.mode === 'native' ? 'native' : 'generated';
            render();
            toast('历史内容已复制为新草稿，请重新校验和发布。');
          }
          break;
        }
      case 'drift':
        driftModal();
        break;
      case 'drift-adopt':
        work.native = D().drift.content;
        work.mode = 'native';
        D().drift.reviewed = true;
        saveDraft(true);
        event('采用外部内容为原生草稿', '已审阅漂移；等待显式发布，未推广到其他节点', 'edit');
        persist();
        closeModal(true);
        ui.validation = null;
        ui.tab = 'config';
        ui.configView = 'code';
        ui.file = 'native';
        render();
        toast('外部内容已成为原生草稿；不会立即影响其他节点。');
        break;
      case 'drift-restore':
        showModal({
          size: 'small',
          title: '模拟恢复该节点的期望配置？',
          body: callout('这会明确覆盖演示节点的外部修改，恢复到当前期望版本。当前工作草稿不受影响。', 'amber'),
          footer: __nscVueAdd(B('取消', 'close'), B('确认恢复（演示）', 'drift-restore-confirm', 'history', 'primary'))
        });
        break;
      case 'drift-restore-confirm':
        {
          const dr = D().drift;
          if (dr) {
            D().loaded[dr.gateway] = D().rev;
            D().drift = null;
            event('模拟恢复期望配置', '外部漂移已处理；没有执行真实文件写入', 'history');
            persist();
          }
          closeModal(true);
          ui.validation = null;
          render();
          toast('演示漂移已清除，工作草稿仍保持原样。');
          break;
        }
      case 'conflict':
        conflictModal();
        break;
      case 'conflict-current':
        work = clone(D().active);
        D().draft = null;
        ui.validation = null;
        persist();
        closeModal(true);
        render();
        toast('已采用当前期望版本，旧工作草稿已放弃。');
        break;
      case 'conflict-rebase':
        if (!document.querySelector('#rebase-ack')?.checked) break;
        D().draft = {
          base: D().rev,
          config: clone(work),
          savedAt: stamp()
        };
        event('显式重新建立草稿基线', '用户确认保留整个旧工作草稿，未自动合并', 'edit');
        persist();
        closeModal(true);
        ui.validation = null;
        render();
        toast('草稿已建立新基线，需要重新校验和审阅。');
        break;
      case 'scenarios':
        scenarioModal();
        break;
      case 'scenario-set':
        setScenario(val);
        break;
      case 'filter':
        ui.filter = val;
        render();
        break;
      case 'palette':
        palette();
        break;
      case 'palette-config':
        closeModal(true);
        navigate('domain', 'config');
        break;
      case 'help':
        helpModal();
        break;
      case 'about':
        showModal({
          size: 'small',
          title: 'Nginx Service Composer',
          sub: 'Community / 交互原型 1.1',
          body: __nscVueView(templates[177], [callout('离线 HTML + CSS + JavaScript，无构建依赖，无 CDN，无真实基础设施连接。', 'neutral')]),
          footer: B('关闭', 'close')
        });
        break;
      case 'reset-confirm':
        showModal({
          size: 'small',
          title: '重置全部本机演示数据？',
          body: callout('此操作删除当前浏览器内的草稿、发布和事件记录。可以先导出 JSON 备份；不会影响真实基础设施。', 'amber'),
          footer: __nscVueAdd(__nscVueAdd(B('取消', 'close'), B('导出备份', 'export-state', 'download')), B('确认重置', 'reset', 'refresh', 'danger'))
        });
        break;
      case 'reset':
        db = seed();
        work = clone(db.domains[0].draft.config);
        ui = {
          ...ui,
          page: 'domain',
          domain: 'd1',
          tab: 'overview',
          selectedRoute: 'r-api',
          file: 'generated',
          configView: 'code',
          validation: null,
          diag: null,
          diagSource: 'draft',
          diagUrl: 'https://example.com/api/users?limit=10',
          externalConflict: false,
          search: '',
          filter: 'all'
        };
        persist();
        closeModal(true);
        updateHash();
        render();
        toast('已恢复初始演示数据。');
        break;
      case 'reload-state':
        location.reload();
        break;
    }
  });
  function copyFallback(text) {
    const el = document.createElement('textarea');
    el.value = text;
    el.style.position = 'fixed';
    el.style.opacity = '0';
    document.body.append(el);
    el.select();
    let ok = false;
    try {
      ok = document.execCommand('copy');
    } catch (e) {}
    el.remove();
    toast(ok ? '已复制配置。' : '浏览器禁止复制，请使用导出 .conf。', !ok);
  }
  listen(document, 'input', e => {
    const t = e.target;
    if (t.dataset.rf && routeForm) {
      const key = t.dataset.rf;
      routeForm[key] = ['timeout', 'connect', 'code'].includes(key) ? Number(t.value) : t.value;
      if (key === 'path') {
        const el = document.querySelector('#route-path-preview');
        if (el) el.textContent = __nscVueAdd(__nscVueAdd(__nscVueAdd('请求 ', routeForm.path), 'users → 上游 '), routeForm.strip ? '/users' : __nscVueAdd(routeForm.path.replace(/\/$/, ''), '/users'));
      }
      return;
    }
    if (t.id === 'code-input') {
      const f = t.dataset.file;
      if (f === 'native') work.native = t.value;else if (f === 'server') work.serverSnippet = t.value;else if (f === 'route') {
        const r = work.routes.find(r => r.id === ui.selectedRoute) || work.routes[0];
        if (r) r.snippet = t.value;
      }
      const gutter = document.querySelector('#code-gutter');
      if (gutter) gutter.textContent = __nscVueJoin(t.value.split('\n').map((_, i) => __nscVueAdd(i, 1)), '\n');
      invalidate();
    }
    if (t.id === 'body-size') {
      work.body = t.value;
      invalidate();
    }
    if (t.id === 'route-search' || t.id === 'domain-search') {
      ui.search = t.value;
      render();
    }
    if (t.id === 'palette-input') region(document.querySelector('#palette-results'), paletteResults(t.value));
    if (t.id === 'diag-url') ui.diagUrl = t.value;
  });
  listen(document, 'change', e => {
    const t = e.target;
    if (t.id === 'env-select') {
      const desired = t.value;
      ensureNavigate(() => {
        db.env = desired;
        const d = db.domains.find(d => d.env === desired);
        if (d) {
          ui.domain = d.id;
          work = clone(d.draft?.config || d.active);
          ui.selectedRoute = work.routes[0]?.id;
          ui.diagUrl = __nscVueAdd(__nscVueAdd('https://', d.name), '/api/users');
        }
        ui.page = 'domains';
        ui.search = '';
        ui.filter = 'all';
        ui.validation = null;
        ui.diag = null;
        persist();
        updateHash();
        render();
      });
      if (dirty()) t.value = db.env;
    }
    if (t.id === 'snippet-route') {
      ui.selectedRoute = t.value;
      render();
    }
    if (t.id === 'diag-source') {
      ui.diagSource = t.value;
      ui.diag = null;
    }
    if (t.id === 'publish-ack') document.querySelector('#confirm-publish').disabled = !t.checked;
    if (t.id === 'rebase-ack') document.querySelector('#rebase-button').disabled = !t.checked;
    if (t.dataset.rf && routeForm) {
      const k = t.dataset.rf;
      routeForm[k] = ['code', 'timeout', 'connect'].includes(k) ? Number(t.value) : t.value;
    }
  });
  listen(document, 'scroll', e => {
    if (e.target.id === 'code-input') {
      const g = document.querySelector('#code-gutter');
      if (g) g.scrollTop = e.target.scrollTop;
    }
  }, true);
  listen(document, 'keydown', e => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      palette();
      return;
    }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
      e.preventDefault();
      if (modal?.kind === 'route') {
        applyRoute();
        if (!modal) saveDraft();
      } else saveDraft();
      return;
    }
    if (e.key === 'Escape' && modal) {
      e.preventDefault();
      closeModal();
      return;
    }
    if (e.key === 'Enter' && e.target.id === 'diag-url') {
      e.preventDefault();
      runDiagnostic();
    }
    if (e.key === 'Tab' && e.target.id === 'code-input') {
      e.preventDefault();
      const t = e.target,
        start = t.selectionStart,
        end = t.selectionEnd;
      t.setRangeText('    ', start, end, 'end');
      t.dispatchEvent(new Event('input', {
        bubbles: true
      }));
      return;
    }
    if (e.key === 'Tab' && modal) {
      const items = [...document.querySelectorAll('#overlay button:not(:disabled),#overlay input:not(:disabled),#overlay select:not(:disabled),#overlay textarea:not(:disabled),#overlay [tabindex="0"]')].filter(el => el.offsetParent !== null);
      const first = items[0],
        last = items.at(-1);
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last?.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first?.focus();
      }
    }
  });
  listen(window, 'beforeunload', e => {
    if (dirty() || deploymentTimer || modal?.kind === 'route' && !equal(routeForm, routeDraftSnapshot)) {
      e.preventDefault();
      e.returnValue = '';
    }
  });
  listen(window, 'storage', e => {
    if (e.key === KEY && e.newValue) {
      try {
        const other = JSON.parse(e.newValue);
        if (other.epoch !== db.epoch) {
          ui.externalConflict = true;
          ui.validation = null;
          render();
          toast('另一标签页更新了工作区，请避免覆盖。', true);
        }
      } catch (_) {}
    }
  });

  /* Project-first interaction layer, v1.1. Browser-only demonstrator.
     Rendering perspective (cloud/ops) and configuration authoring (managed/native)
     are independent. Environment IDs are globally unique; project IDs are checked
     at every resource lookup and clone boundary. */
  function emptyConfig() {
    return {
      mode: 'managed',
      tls: false,
      redirect: false,
      body: '32m',
      serverSnippet: '',
      native: '',
      routes: []
    };
  }
  function seed() {
    const b = seedV10();
    b.schema = 2;
    b.project = 'portal';
    b.env = 'prod';
    b.projects = [{
      id: 'portal',
      key: 'customer-portal',
      name: '客户门户',
      description: '应用入口、API 与管理控制台。按开发、测试、沙盒、生产分别配置。',
      created: '2026-09-14 10:00:00'
    }, {
      id: 'docs',
      key: 'team-docs',
      name: '团队文档',
      description: '简单部署：只有一个生产环境，不强制创建开发和测试环境。',
      created: '2026-09-14 10:00:00'
    }];
    b.environments = [{
      id: 'dev',
      project: 'portal',
      slug: 'development',
      name: '开发环境',
      kind: 'development',
      gateways: ['gw3']
    }, {
      id: 'test',
      project: 'portal',
      slug: 'testing',
      name: '测试环境',
      kind: 'testing',
      gateways: ['gw3']
    }, {
      id: 'sandbox',
      project: 'portal',
      slug: 'sandbox',
      name: '沙盒环境',
      kind: 'sandbox',
      gateways: ['gw3']
    }, {
      id: 'prod',
      project: 'portal',
      slug: 'production',
      name: '生产环境',
      kind: 'production',
      gateways: ['gw1', 'gw2']
    }, {
      id: 'docs-prod',
      project: 'docs',
      slug: 'production',
      name: '生产环境',
      kind: 'production',
      gateways: ['gw4']
    }];
    b.services = b.services.filter(s => s.env === 'prod').map(s => ({
      ...s,
      project: 'portal',
      name: s.id,
      role: s.id.replace('-service', '').replace('web-', '')
    }));
    for (const [i, en] of ['dev', 'test', 'sandbox'].entries()) {
      for (const [j, [name, port]] of [['web-frontend', 3000], ['api-service', 8080], ['static-service', 8080]].entries()) {
        b.services.push({
          id: __nscVueAdd(__nscVueAdd(en, '-'), name),
          name,
          role: name.replace('-service', '').replace('web-', ''),
          project: 'portal',
          env: en,
          runtime: 'swarm-nonproduction',
          image: __nscVueAdd(__nscVueAdd(__nscVueAdd('demo/', name), ':'), en),
          port,
          endpoints: [[__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('10.', __nscVueAdd(i, 10)), '.'), j), '.11'), true]]
        });
      }
    }
    b.services.push({
      id: 'docs-web',
      name: 'docs-web',
      role: 'docs',
      project: 'docs',
      env: 'docs-prod',
      runtime: 'compose-docs',
      image: 'demo/docs:1.0',
      port: 3000,
      endpoints: [['172.20.0.5', true]]
    });
    b.gateways = [{
      id: 'gw1',
      name: 'nginx-gw-01',
      env: 'prod',
      ip: '10.0.0.2',
      runtime: 'swarm-production'
    }, {
      id: 'gw2',
      name: 'nginx-gw-02',
      env: 'prod',
      ip: '10.0.0.3',
      runtime: 'swarm-production'
    }, {
      id: 'gw3',
      name: 'nginx-gw-nonprod',
      env: 'test',
      ip: '10.9.0.2',
      runtime: 'swarm-nonproduction'
    }, {
      id: 'gw4',
      name: 'nginx-gw-docs',
      env: 'docs-prod',
      ip: '172.20.0.2',
      runtime: 'compose-docs'
    }];
    b.domains = b.domains.filter(d => d.env === 'prod').map(d => ({
      ...d,
      project: 'portal',
      fileBackups: [],
      certReadyDemo: true
    }));
    for (const [id, name, env, service, project] of [['d-dev', 'dev.int.example.com', 'dev', 'dev-api-service', 'portal'], ['d-test', 'test.int.example.com', 'test', 'test-api-service', 'portal'], ['d-test-external', 'qa-preview.example.net', 'test', 'test-web-frontend', 'portal'], ['d-sandbox', 'sandbox.int.example.com', 'sandbox', 'sandbox-api-service', 'portal'], ['d-docs', 'docs.example.org', 'docs-prod', 'docs-web', 'docs']]) {
      const en = b.environments.find(e => e.id === env),
        c = emptyConfig();
      c.tls = true;
      c.redirect = true;
      c.routes = [route(__nscVueAdd(id, '-root'), '/', service)];
      b.domains.push({
        id,
        name,
        project,
        env,
        gateways: [...en.gateways],
        rev: 2,
        active: clone(c),
        loaded: Object.fromEntries(en.gateways.map(g => [g, 2])),
        draft: null,
        drift: null,
        created: '2026-09-14 09:00:00',
        certExpiry: '2026-12-01',
        certReadyDemo: true,
        fileBackups: [],
        revisions: [{
          v: 2,
          config: clone(c),
          time: '2026-09-14 09:00:00',
          note: '环境入口 · 演示数据'
        }]
      });
    }
    for (const e of b.environments) ENV[e.id] = e.name;
    return b;
  }
  const V10 = {
    shell,
    globalPage,
    domainPage,
    configPage,
    semanticConfig,
    validateConfig,
    checkRoute,
    saveDraft,
    serviceDetail,
    impactModal,
    showRouteForm,
    helpModal,
    gatewaysPage,
    closeModal
  };
  const KIND_NAMES = {
    production: '生产',
    development: '开发',
    testing: '测试',
    sandbox: '沙盒',
    custom: '自定义'
  };
  function P(id = db.project) {
    return db.projects.find(p => p.id === id);
  }
  function E(id = db.env) {
    return db.environments.find(e => e.id === id);
  }
  function projectEnvs(id = db.project) {
    return db.environments.filter(e => e.project === id);
  }
  function scopeDomains(env = db.env, project = db.project) {
    return db.domains.filter(d => d.project === project && d.env === env);
  }
  function scopeServices(env = db.env, project = db.project) {
    return db.services.filter(s => s.project === project && s.env === env);
  }
  function gatewayAllowed(g, env = db.env) {
    return !!E(env)?.gateways.includes(g.id);
  }
  function scopeText(d = D()) {
    return __nscVueJoin([P(d.project)?.name, E(d.env)?.name, d.name].filter(Boolean), ' / ');
  }
  function hasDomain() {
    return !!db.domains.find(d => d.id === ui.domain && d.project === db.project && d.env === db.env);
  }
  D = function () {
    return db.domains.find(d => d.id === ui.domain && d.project === db.project && d.env === db.env) || {
      id: null,
      name: '尚未添加域名',
      project: db.project,
      env: db.env,
      active: emptyConfig(),
      draft: null,
      gateways: [],
      loaded: {},
      rev: 0,
      revisions: [],
      fileBackups: []
    };
  };
  function selectScope(project, env, domain = null) {
    ui.allEnvironments = false;
    ui.allProjects = false;
    const e = E(env);
    if (!P(project) || !e || e.project !== project) return false;
    db.project = project;
    db.env = env;
    const d = scopeDomains().find(d => d.id === domain) || scopeDomains()[0];
    ui.domain = d?.id || null;
    work = clone(d?.draft?.config || d?.active || emptyConfig());
    ui.selectedRoute = work.routes[0]?.id;
    ui.validation = null;
    ui.diag = null;
    ui.search = '';
    ui.filter = 'all';
    ui.file = work.mode === 'native' ? 'native' : 'generated';
    ui.diagUrl = __nscVueAdd(__nscVueAdd(work.tls ? 'https://' : 'http://', d?.name || 'example.com'), '/');
    ui.diagSource = 'draft';
    ui.menu = false;
    return true;
  }
  navigate = function (page, tab = 'overview', id = ui.domain, guard = true) {
    const run = () => {
      if (page === 'domain') {
        const d = db.domains.find(d => d.id === id);
        if (!d) {
          toast('这个域名不在当前资源记录中。', true);
          return;
        }
        if (d.id !== ui.domain || d.project !== db.project || d.env !== db.env) selectScope(d.project, d.env, d.id);
      }
      ui.page = page;
      ui.tab = tab;
      ui.menu = false;
      ui.search = '';
      ui.filter = 'all';
      closeModal(true);
      updateHash();
      render();
      window.scrollTo(0, 0);
    };
    if (guard && (page !== ui.page || id !== ui.domain)) ensureNavigate(run);else run();
  };
  function switchScope(project, env = null, page = null) {
    const target = env || projectEnvs(project).find(e => e.kind === 'production')?.id || projectEnvs(project)[0]?.id;
    if (!P(project) || !E(target) || E(target).project !== project) {
      toast('项目与环境不匹配。', true);
      return;
    }
    const sameProject = project === db.project;
    const oldPage = ui.page;
    ensureNavigate(() => {
      selectScope(project, target);
      ui.page = page || (sameProject && ['domains', 'routes', 'services', 'configs', 'certificates', 'deployments', 'events', 'gateways'].includes(oldPage) ? oldPage : sameProject ? 'overview' : 'project');
      ui.tab = 'overview';
      if (!ui.externalConflict) persist();
      closeModal(true);
      updateHash();
      render();
      window.scrollTo(0, 0);
      toast(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('已进入 ', P().name), ' / '), E().name), '。不会切换线上流量。'));
    });
  }
  updateHash = function () {
    if (booting) return;
    const root = __nscVueAdd('#/projects/', encodeURIComponent(db.project));
    const h = ui.page === 'projects' ? '#/projects' : ui.page === 'project' ? root : ui.page === 'environments' ? __nscVueAdd(root, '/environments') : ui.page === 'domain' ? __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(root, '/environments/'), encodeURIComponent(db.env)), '/domains/'), encodeURIComponent(ui.domain)), '/'), ui.tab) : __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(root, '/environments/'), encodeURIComponent(db.env)), '/'), ui.page);
    if (location.hash !== h) history.replaceState(null, '', h);
  };
  parseHash = function () {
    const a = location.hash.replace(/^#\/?/, '').split('/').map(x => {
      try {
        return decodeURIComponent(x);
      } catch (_) {
        return '';
      }
    });
    if (a[0] === 'projects') {
      if (!a[1]) {
        ui.page = 'projects';
        return;
      }
      const p = P(a[1]);
      if (!p) {
        ui.page = 'projects';
        toast('项目不存在，已返回项目列表。', true);
        return;
      }
      const env = a[2] === 'environments' && a[3] ? E(a[3]) : projectEnvs(p.id).find(e => e.kind === 'production') || projectEnvs(p.id)[0];
      if (!env || env.project !== p.id) {
        ui.page = 'projects';
        toast('链接中的项目与环境不匹配。', true);
        return;
      }
      if (a[4] === 'domains' && a[5]) {
        const d = db.domains.find(d => d.id === a[5] && d.project === p.id && d.env === env.id);
        if (!d) {
          selectScope(p.id, env.id);
          ui.page = 'domains';
          toast('域名不属于链接指定的项目 / 环境。', true);
          return;
        }
        selectScope(p.id, env.id, d.id);
        ui.page = 'domain';
        ui.tab = ['overview', 'routes', 'config', 'tls', 'history'].includes(a[6]) ? a[6] : 'overview';
      } else {
        selectScope(p.id, env.id);
        ui.page = a[4] && Object.hasOwn(PAGE_NAMES, a[4]) ? a[4] : a[2] === 'environments' && !a[3] ? 'environments' : a[3] ? 'overview' : 'project';
      }
    } else if (a[0] === 'domain') {
      // Compatibility only when the old domain-only URL resolves unambiguously.
      const ds = db.domains.filter(d => d.name === a[1]);
      if (ds.length === 1) {
        selectScope(ds[0].project, ds[0].env, ds[0].id);
        ui.page = 'domain';
        ui.tab = ['overview', 'routes', 'config', 'tls', 'history'].includes(a[2]) ? a[2] : 'overview';
      } else {
        ui.page = 'projects';
        toast('旧链接无法唯一定位资源，请从项目进入。', true);
      }
    }
  };
  function navEntry(...args) {
    return __projectsViews.navEntry_34(__renderContext0, ...args);
  }
  function projectSidebar(...args) {
    return __projectsViews.projectSidebar_35(__renderContext0, ...args);
  }
  function contextBar(...args) {
    return __projectsViews.contextBar_36(__renderContext0, ...args);
  }
  function breadcrumb(...args) {
    return __projectsViews.breadcrumb_37(__renderContext0, ...args);
  }
  function projectCrumb(...args) {
    return __projectsViews.projectCrumb_38(__renderContext0, ...args);
  }
  function contextCrumb(...args) {
    return __projectsViews.contextCrumb_39(__renderContext0, ...args);
  }
  domainPage = function () {
    return insertView(V10.domainPage(), '.breadcrumbs', breadcrumb([{
      label: P().name,
      action: 'nav',
      attrs: 'data-page="project"'
    }, {
      label: E().name,
      action: 'nav',
      attrs: 'data-page="overview"'
    }, {
      label: '域名',
      action: 'nav',
      attrs: 'data-page="domains"'
    }, {
      label: D().name
    }]), 'replace');
  };
  function envCard(...args) {
    return __projectsViews.envCard_40(__renderContext0, ...args);
  }
  function projectPage(...args) {
    return __projectsViews.projectPage_41(__renderContext0, ...args);
  }
  function projectsPage(...args) {
    return __projectsViews.projectsPage_42(__renderContext0, ...args);
  }
  function environmentsPage(...args) {
    return __projectsViews.environmentsPage_43(__renderContext0, ...args);
  }
  function environmentOverview(...args) {
    return __projectsViews.environmentOverview_44(__renderContext0, ...args);
  }
  globalPage = function () {
    if (ui.page === 'projects') return projectsPage();
    if (ui.page === 'project') return projectPage();
    if (ui.page === 'environments') return environmentsPage();
    if (ui.page === 'overview') return environmentOverview();
    let h = V10.globalPage();
    h = insertView(h, '.breadcrumbs', contextCrumb({
      domains: '域名',
      routes: '路由规则',
      services: '服务',
      configs: 'Nginx 配置',
      gateways: '可用网关',
      certificates: 'TLS / 证书',
      deployments: '发布记录',
      events: '活动记录'
    }[ui.page] || ''), 'replace');
    if (ui.page === 'services') h = h;
    return h;
  };
  configPage = function () {
    let h = V10.configPage();
    h = insertView(h, 'h4', __nscVueView(templates[204], [esc(P().name), esc(E().name)]), 'after');
    return insertView(h, '.tree-heading', __nscVueView(templates[205], [I('history'), D().fileBackups?.length || 0]), 'after');
  };
  semanticConfig = function () {
    if (work.mode === 'native') return __nscVueView(templates[206], [I('terminal'), B('继续编辑配置文件', 'file', 'code', 'primary', 'data-file="native"'), B('改用表单配置…', 'managed-mode', 'settings'), B('查看文件副本', 'p-file-backups', 'history')]);
    return __nscVueView(templates[207], [I('globe'), esc(D().name), esc(P().name), esc(E().name), work.tls ? 'on' : '', work.tls, work.redirect ? 'on' : '', !work.tls ? 'disabled' : '', work.redirect, I('settings'), esc(work.body), B('编辑路由设置', 'route-edit', 'route', 'sm', __nscVueAdd(__nscVueAdd('data-id="', work.routes.find(r => r.path === '/api/')?.id || work.routes[0]?.id || ''), '"')), I('edit'), B('补充域名指令', 'file', 'code', 'sm', 'data-file="server"'), B('补充路由指令', 'file', 'route', 'sm', 'data-file="route"'), I('terminal'), B('直接编辑配置文件…', 'native-mode', 'terminal', 'sm'), (D().fileBackups?.length || 0) > 0 ? __nscVueAdd(' ', B('查看已保留的文件副本', 'p-file-backups', 'history', 'sm')) : '']);
  };
  saveDraft = function (silent = false) {
    if (!hasDomain()) {
      if (!silent) toast('当前没有选中的域名草稿。');
      return false;
    }
    return V10.saveDraft(silent);
  };
  checkRoute = function (r, exceptId = r.id) {
    const err = V10.checkRoute(r, exceptId);
    if (err) return err;
    const s = S(r.service);
    if (r.kind === 'proxy' && (!s || s.project !== D().project || s.env !== D().env)) return '目标服务必须属于当前项目和环境；不能隐式转发到另一个环境。';
    return '';
  };
  function domainConflict(name, targets, exclude = null) {
    return db.domains.find(d => d.id !== exclude && d.name === name && d.gateways.some(g => targets.includes(g)));
  }
  validateConfig = function () {
    const v = V10.validateConfig(),
      d = D();
    if (!hasDomain()) v.errors.push('没有选中的域名。');
    if (!E(d.env) || E(d.env).project !== d.project) v.errors.push('项目 / 环境归属不匹配。');
    if (!d.gateways.length) v.errors.push('未选择发布网关。');
    if (d.gateways.some(id => !E(d.env)?.gateways.includes(id))) v.errors.push('绑定网关不在该环境的可用范围。');
    const conflict = domainConflict(d.name, d.gateways, d.id);
    if (conflict) v.errors.push(__nscVueAdd(__nscVueAdd('入口已被 ', scopeText(conflict)), ' 占用；更换环境标签不能绕过冲突。'));
    if (work.mode === 'native') v.warnings.push('文件内容可能引用其他环境；本地检查不能确认其完整依赖。需要逐项人工审阅及真实网关校验。');
    v.errors = [...new Set(v.errors)];
    return v;
  };
  impactModal = function (publish = false) {
    V10.impactModal(publish);
    if (modal?.kind === 'publish-confirm' || modal?.kind === 'impact') {
      modal.sub = __nscVueAdd(__nscVueAdd(scopeText(), ' · 基线 v'), D().draft?.base ?? D().rev);
      modal.body = __nscVueAdd(__nscVueView(templates[208], [publish ? '发布' : '变更', esc(P().name), esc(E().name), esc(D().name), badge(E().kind === 'production' ? '生产环境 · 请确认' : '仅此环境', E().kind === 'production' ? 'amber' : 'blue')]), modal.body);
      renderModal();
    }
  };
  showRouteForm = function (error = '') {
    V10.showRouteForm(error);
    if (modal?.kind === 'route') {
      modal.sub = __nscVueAdd(scopeText(), ' / 路由草稿');
      renderModal();
    }
  };
  domainSettings = function () {
    const d = D();
    showModal({
      size: 'drawer',
      title: '域名设置',
      sub: scopeText(),
      body: __nscVueView(templates[211], [esc(P(d.project).name), badge(E(d.env).name, E(d.env).kind === 'production' ? 'green' : 'blue'), esc(d.name), work.mode === 'native' ? '直接编辑配置文件' : '表单配置 + 自定义指令', d.id, __nscVueJoin(d.gateways.map(id => __nscVueView(templates[209], [I('server'), esc(G(id).name), badge('已绑定', 'blue')])), ''), d.rev === 0 ? __nscVueView(templates[210], [B('选择发布网关', 'p-domain-targets', 'server', 'sm')]) : '', callout('域名所属环境由绑定决定，不按 dev / test 前缀推断。DNS 解析和证书仍需分别配置。', 'blue'), B('复制配置到其他环境', 'p-copy', 'copy', 'sm')]),
      footer: __nscVueAdd(B('关闭', 'close'), B('编辑 Nginx 配置', 'settings-to-config', 'code', 'primary'))
    });
  };
  function modeSnapshotText() {
    const c = clone(work);
    c.mode = 'managed';
    c.native = '';
    return renderConfig(c);
  }
  ownershipModal = function (native = true) {
    const d = D();
    showModal({
      kind: 'mode-switch',
      size: native ? 'small' : 'wide',
      modeDirection: native ? 'file' : 'form',
      title: native ? '改为直接编辑配置文件？' : '改用表单配置？',
      sub: __nscVueAdd(scopeText(), ' · 只影响当前草稿'),
      body: __nscVueView(templates[215], [callout(native ? __nscVueView(templates[212], []) : __nscVueView(templates[213], []), 'amber'), I('file'), native ? '当前生成配置会成为可编辑文件' : '先保留当前文件副本', native ? '无需重新输入，当前草稿内容会完整带入。' : '文件副本保存在当前浏览器中，可查看和导出；不会直接丢弃。', I('settings'), native ? '路由与 TLS 表单暂停修改' : '恢复切换前保留的路由和设置', native ? '避免两处编辑相互覆盖。云原生 / 运维视角仍可随时切换。' : '不是解析文件后同步回表单。需要的手写内容可从副本手动迁回。', I('send'), !native ? __nscVueView(templates[214], [diffHTML(work.native, modeSnapshotText(), true).replace(__nscVueAdd(__nscVueAdd('· v', D().rev), ' → 工作草稿'), '· 当前文件 → 表单生成结果')]) : '', native ? '我理解切换后需在配置文件中修改路由与 TLS。' : '我理解手写修改不会自动回填，将保留文件副本并使用表单重新生成。']),
      footer: __nscVueAdd(B(native ? '继续使用表单' : '继续编辑文件', 'close'), B(native ? '确认改为文件编辑' : '保留副本并改用表单', native ? 'p-mode-file' : 'p-mode-form', native ? 'code' : 'settings', 'primary', 'id="p-mode-confirm" disabled'))
    });
  };
  function changeMode(toFile) {
    if (!document.querySelector('#p-mode-ack')?.checked) return;
    const d = D();
    if (toFile) {
      d.formSnapshot = {
        config: clone(work),
        time: stamp()
      };
      work.native = renderConfig(work);
      work.mode = 'native';
      ui.file = 'native';
      ui.configView = 'code';
    } else {
      d.fileBackups = d.fileBackups || [];
      d.fileBackups.unshift({
        id: uid('file-copy'),
        text: work.native,
        time: stamp(),
        reason: '改用表单前保留',
        project: d.project,
        env: d.env,
        domain: d.id
      });
      work.mode = 'managed';
      work.native = '';
      ui.file = 'generated';
      ui.configView = 'semantic';
    }
    persist();
    ui.tab = 'config';
    event(toFile ? '改为直接编辑文件' : '保留文件副本并改用表单', '仅修改草稿；不改变线上版本', 'edit');
    closeModal(true);
    invalidate();
    updateHash();
    render();
    toast(toFile ? '现在可以编辑配置文件。路由与 TLS 表单已暂停修改。' : '文件副本已保留，表单已恢复。请检查差异后再发布。');
  }
  function fileBackups() {
    const list = D().fileBackups || [];
    showModal({
      size: 'wide',
      title: '已保留的文件副本',
      sub: scopeText(),
      body: __nscVueAdd(callout('文件副本仅在当前浏览器中保存，不是已发布版本。导出工作区 JSON 可以一并备份。', 'neutral'), __nscVueView(templates[218], [list.length ? __nscVueJoin(list.map(x => __nscVueView(templates[216], [cardHead(x.time, esc(x.reason), B('导出 .conf', 'p-backup-export', 'download', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [x.id]))), codeHTML(x.text)])), '') : __nscVueView(templates[217], [])])),
      footer: __nscVueAdd(B('关闭', 'close'), B('导出工作区 JSON', 'export-state', 'download'))
    });
  }
  function formError(message) {
    const el = document.querySelector('#p-form-error');
    if (el) {
      region(el, callout(esc(message), 'red'));
      el.scrollIntoView({
        block: 'nearest'
      });
    } else toast(message, true);
  }
  function validHost(name) {
    return name.length <= 253 && name.includes('.') && name.split('.').every(l => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(l));
  }
  function projectModal() {
    showModal({
      kind: 'p-project-form',
      size: 'small',
      title: '创建项目',
      sub: '先创建一个生产环境，其他环境按需添加。',
      body: __nscVueView(templates[219], [I('layers')]),
      footer: __nscVueAdd(B('取消', 'close'), B('创建项目', 'p-project-create', 'plus', 'primary'))
    });
  }
  function createProject() {
    const name = document.querySelector('#p-name').value.trim(),
      key = document.querySelector('#p-key').value.trim(),
      description = document.querySelector('#p-description').value.trim();
    if (!name) return formError('填写项目名称。');
    if (!/^[a-z0-9](?:[a-z0-9-]{0,46}[a-z0-9])?$/.test(key)) return formError('项目标识使用小写字母、数字和连字符，不以连字符开头或结尾。');
    if (db.projects.some(p => p.key === key)) return formError('该项目标识已存在，请使用另一个。');
    const p = {
        id: uid('project'),
        key,
        name,
        description,
        created: stamp()
      },
      e = {
        id: uid('env'),
        project: p.id,
        slug: 'production',
        name: '生产环境',
        kind: 'production',
        gateways: []
      };
    db.projects.push(p);
    db.environments.push(e);
    ENV[e.id] = esc(e.name);
    selectScope(p.id, e.id);
    ui.page = 'project';
    closeModal(true);
    persist();
    updateHash();
    render();
    toast('项目已创建，包含一个生产环境。');
  }
  function gatewayChoices(selected = [], name = 'p-gateway') {
    return __nscVueView(templates[221], [__nscVueJoin(db.gateways.map(g => {
      const n = db.environments.filter(e => e.gateways.includes(g.id)).length;
      return __nscVueView(templates[220], [name, g.id, selected.includes(g.id) ? 'checked' : '', esc(g.name), esc(g.runtime), n]);
    }), '')]);
  }
  function envModal(editId = null) {
    const e = E(editId),
      editing = !!e;
    showModal({
      kind: 'p-env-form',
      envId: editId,
      size: 'drawer',
      title: editing ? '环境设置' : '添加环境',
      sub: __nscVueAdd(P().name, ' · 环境名称不决定域名格式'),
      body: __nscVueView(templates[223], [esc(e?.name || ''), esc(e?.slug || ''), editing ? 'disabled' : '', __nscVueJoin(Object.entries(KIND_NAMES).map(([k, v]) => __nscVueView(templates[222], [k, k === (e?.kind || 'custom') ? 'selected' : '', v])), ''), gatewayChoices(e?.gateways || []), callout('创建环境不会复制生产配置。需要复用时，使用“复制配置到其他环境”并重新确认目标资源。', 'blue')]),
      footer: __nscVueAdd(editing ? B('删除空环境', 'p-env-delete', 'trash', 'danger', __nscVueInterpolate(["data-id=\"", "\""], [e.id])) : B('取消', 'close'), B(editing ? '保存环境设置' : '创建环境', 'p-env-save', 'save', 'primary'))
    });
  }
  function saveEnvironment() {
    const editing = modal.envId,
      name = document.querySelector('#p-env-name').value.trim(),
      slug = document.querySelector('#p-env-slug').value.trim(),
      kind = document.querySelector('#p-env-kind').value,
      gateways = [...document.querySelectorAll('[name="p-gateway"]:checked')].map(x => x.value);
    if (!name) return formError('填写环境名称。');
    if (!/^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$/.test(slug)) return formError('环境标识使用小写字母、数字和连字符。');
    if (projectEnvs().some(e => e.slug === slug && e.id !== editing)) return formError('本项目中已有相同环境标识。');
    if (editing) {
      const e = E(editing);
      const refs = scopeDomains(e.id, e.project).filter(d => d.gateways.some(g => !gateways.includes(g)));
      if (refs.length) return formError(__nscVueAdd(__nscVueAdd('不能移除已被 ', __nscVueJoin(refs.map(d => d.name), '、')), ' 引用的网关。请先完成入口迁移。'));
      Object.assign(e, {
        name,
        kind,
        gateways
      });
      ENV[e.id] = esc(name);
      closeModal(true);
      persist();
      render();
      toast('环境设置已保存。已有域名的发布目标未改变。');
    } else {
      const e = {
        id: uid('env'),
        project: db.project,
        name,
        slug,
        kind,
        gateways
      };
      db.environments.push(e);
      ENV[e.id] = esc(name);
      selectScope(db.project, e.id);
      ui.page = 'overview';
      closeModal(true);
      persist();
      updateHash();
      render();
      toast('环境已创建，没有复制生产资源。');
    }
  }
  function deleteEnvironment(id) {
    const e = E(id);
    if (projectEnvs(e.project).length === 1) return formError('项目至少保留一个环境。');
    if (scopeDomains(id, e.project).length || scopeServices(id, e.project).length) return formError('环境仍有域名或服务，不能直接删除。请先处理资源与入口。');
    showModal({
      size: 'small',
      title: '删除空环境？',
      sub: esc(e.name),
      body: callout('仅删除该空环境记录，不操作网关或 DNS。', 'amber'),
      footer: __nscVueAdd(B('取消', 'close'), B('确认删除空环境', 'p-env-delete-confirm', 'trash', 'danger', __nscVueInterpolate(["data-id=\"", "\""], [id])))
    });
  }
  function chooseEnvForDomain() {
    showModal({
      size: 'small',
      title: '为哪个环境绑定域名？',
      sub: P().name,
      body: __nscVueView(templates[225], [__nscVueJoin(projectEnvs().map(e => __nscVueView(templates[224], [e.id, I('layers'), esc(e.name), scopeDomains(e.id).length, esc(e.slug), I('right', 'sm')])), '')]),
      footer: B('取消', 'close')
    });
  }
  addDomainModal = function () {
    const e = E();
    showModal({
      kind: 'add-domain',
      size: 'drawer',
      title: '绑定域名',
      sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(P().name, ' / '), e.name), ' · 创建草稿，不修改线上入口'),
      body: __nscVueView(templates[229], [esc(P().name), esc(e.name), __nscVueJoin(scopeServices().map(s => __nscVueView(templates[226], [s.id, esc(s.name || s.id), s.port, esc(s.id)])), ''), __nscVueJoin(e.gateways.map(id => __nscVueView(templates[227], [id, esc(G(id).name), esc(G(id).ip)])), '') || __nscVueView(templates[228], []), callout('先创建 HTTP 草稿。绑定域名不会创建 DNS 记录，也不会自动签发证书。没有服务或网关时仍可保存，但不能完成发布。', 'neutral')]),
      footer: __nscVueAdd(B('取消', 'close'), B('创建域名草稿', 'domain-create', 'plus', 'primary'))
    });
  };
  function makeDraftDomain(name, env, project, gateways, config, extra = {}) {
    const c = emptyConfig(),
      d = {
        id: uid('domain'),
        name,
        project,
        env,
        gateways,
        rev: 0,
        active: clone(c),
        loaded: Object.fromEntries(gateways.map(g => [g, 0])),
        draft: {
          base: 0,
          config: clone(config),
          savedAt: stamp()
        },
        drift: null,
        created: stamp(),
        certExpiry: extra.certReadyDemo ? '2026-12-01' : '—',
        revisions: [{
          v: 0,
          config: clone(c),
          time: stamp(),
          note: '尚未发布 · 空基线'
        }],
        fileBackups: [],
        ...extra
      };
    db.domains.push(d);
    return d;
  }
  createDomain = function () {
    const name = document.querySelector('#new-domain-name').value.trim().toLowerCase(),
      service = document.querySelector('#new-domain-service').value,
      gateways = [...document.querySelectorAll('[name="domain-gateway"]:checked')].map(x => x.value);
    if (!validHost(name)) return formError('请输入合法的 ASCII 完整域名，例如 preview.example.net。');
    if (scopeDomains().some(d => d.name === name)) return formError('当前环境已绑定此域名，请打开已有域名继续编辑。');
    const conflict = domainConflict(name, gateways);
    if (conflict) return formError(__nscVueAdd(__nscVueAdd('该域名在所选网关已被 ', scopeText(conflict)), ' 占用。不同环境不能绕过入口冲突。'));
    if (service && !scopeServices().some(s => s.id === service)) return formError('目标服务不属于当前项目和环境。');
    const c = emptyConfig();
    if (service) c.routes = [route(uid('r'), '/', service)];
    const d = makeDraftDomain(name, db.env, db.project, gateways, c);
    selectScope(db.project, db.env, d.id);
    ui.page = 'domain';
    ui.tab = 'config';
    ui.configView = 'semantic';
    event('创建域名草稿', scopeText(d), 'plus', d.id);
    closeModal(true);
    persist();
    updateHash();
    render();
    toast('域名已绑定至当前环境。草稿尚未发布。');
  };
  function serviceModal() {
    showModal({
      kind: 'p-service-form',
      size: 'small',
      title: '添加演示服务',
      sub: __nscVueAdd(__nscVueAdd(P().name, ' / '), E().name),
      body: __nscVueAdd(callout('仅创建本地服务记录，演示项目和环境归属。不会发现、部署或重启真实应用。', 'neutral'), __nscVueView(templates[230], [])),
      footer: __nscVueAdd(B('取消', 'close'), B('添加演示服务', 'p-service-create', 'plus', 'primary'))
    });
  }
  function createService() {
    const name = document.querySelector('#p-service-name').value.trim(),
      port = Number(document.querySelector('#p-service-port').value),
      ip = document.querySelector('#p-service-ip').value.trim();
    if (!/^[a-zA-Z0-9][a-zA-Z0-9-]{0,62}$/.test(name)) return formError('服务名称使用英文、数字和连字符。');
    if (scopeServices().some(s => (s.name || s.id) === name)) return formError('当前环境已有该名称的服务。');
    if (!Number.isInteger(port) || port < 1 || port > 65535) return formError('端口必须是 1–65535 的整数。');
    if (ip && (!/^\d+\.\d+\.\d+\.\d+$/.test(ip) || ip.split('.').some(x => Number(x) > 255))) return formError('演示 Endpoint 需为合法 IPv4 地址。');
    db.services.push({
      id: uid('svc'),
      name,
      role: name,
      project: db.project,
      env: db.env,
      port,
      endpoints: ip ? [[ip, true]] : [],
      runtime: 'manual-demo',
      image: '未部署 · 演示记录'
    });
    closeModal(true);
    persist();
    ui.page = 'services';
    updateHash();
    render();
    toast('服务记录已添加，只对当前项目 / 环境可见。');
  }
  let copyFlow = null;
  function beginCopy() {
    if (projectEnvs().length < 2) {
      toast('这个项目目前只有一个环境。请先添加目标环境。');
      return;
    }
    const candidates = db.domains.filter(d => d.project === db.project);
    if (!candidates.length) {
      toast('项目还没有可复制的域名配置。');
      return;
    }
    const source = hasDomain() ? D() : candidates[0];
    copyFlow = {
      source: source.id,
      target: '',
      name: '',
      mapping: {},
      redirects: {},
      gateways: [],
      tls: false,
      includeSnippets: false,
      reviewSnippets: false,
      step: 1,
      project: db.project
    };
    copyDialog();
  }
  function collectCopy() {
    if (!copyFlow) return;
    const source = document.querySelector('#p-copy-source'),
      target = document.querySelector('#p-copy-target'),
      name = document.querySelector('#p-copy-name');
    if (source) copyFlow.source = source.value;
    if (target) copyFlow.target = target.value;
    if (name) copyFlow.name = name.value.trim().toLowerCase();
    document.querySelectorAll('[data-copy-service]').forEach(x => copyFlow.mapping[x.dataset.copyService] = x.value);
    document.querySelectorAll('[data-copy-redirect]').forEach(x => copyFlow.redirects[x.dataset.copyRedirect] = x.value.trim());
    if (document.querySelector('[name="copy-gateway"]')) copyFlow.gateways = [...document.querySelectorAll('[name="copy-gateway"]:checked')].map(x => x.value);
    const tls = document.querySelector('#p-copy-tls');
    if (tls) copyFlow.tls = tls.checked;
    const snippets = document.querySelector('#p-copy-snippets');
    if (snippets) copyFlow.includeSnippets = snippets.checked;
    const review = document.querySelector('#p-copy-snippet-review');
    if (review) copyFlow.reviewSnippets = review.checked;
  }
  function copySource() {
    return db.domains.find(d => d.id === copyFlow?.source);
  }
  function copyConfig() {
    const s = copySource();
    return s.id === ui.domain ? work : s.draft?.config || s.active;
  }
  function copyDialog() {
    const f = copyFlow,
      s = copySource(),
      c = copyConfig(),
      target = E(f.target),
      ss = [...new Set(c.routes.filter(r => r.kind === 'proxy').map(r => r.service))],
      native = c.mode === 'native';
    let body = __nscVueView(templates[231], [step('1', '选择来源与目标', f.step === 1 ? 'now' : 'done'), step('2', '重新绑定资源', f.step === 2 ? 'now' : f.step > 2 ? 'done' : ''), step('3', '确认生成草稿', f.step === 3 ? 'now' : '')]);
    if (f.step === 1) body = __nscVueAdd(body, __nscVueView(templates[234], [__nscVueJoin(db.domains.filter(d => d.project === f.project).map(d => __nscVueView(templates[232], [d.id, d.id === f.source ? 'selected' : '', esc(E(d.env).name), esc(d.name), d.draft ? ' · 当前草稿' : ' · 已发布配置'])), ''), s.id === ui.domain && dirty() ? '当前未保存草稿，下一步会先保存' : s.draft ? '已保存草稿' : '已发布配置', __nscVueJoin(projectEnvs(f.project).filter(e => e.id !== s.env).map(e => __nscVueView(templates[233], [e.id, e.id === f.target ? 'selected' : '', esc(e.name), esc(e.slug)])), ''), esc(f.name), native ? callout('来源正在直接编辑配置文件，不能可靠拆成路由和资源绑定。本向导不复制该文件；请导出、逐项修改环境依赖，并在目标域名中重新校验。', 'amber') : callout('复用路由与参数，不复制生产 Endpoint、证书私钥、Gateway 绑定、发布记录或运行状态。', 'blue')]));
    if (f.step === 2) body = __nscVueAdd(body, __nscVueView(templates[241], [esc(E(s.env).name), esc(s.name), esc(target?.name), esc(f.name), __nscVueJoin(ss.map(id => __nscVueView(templates[236], [I('box'), esc(S(id)?.name || id), esc(id), I('arrow'), id, esc(id), __nscVueJoin(scopeServices(f.target, f.project).map(x => __nscVueView(templates[235], [x.id, f.mapping[id] === x.id ? 'selected' : '', esc(x.name || x.id), x.port, ready(x.id).length])), '')])), '') || __nscVueView(templates[237], []), __nscVueJoin(c.routes.filter(r => r.kind === 'redirect').map(r => __nscVueView(templates[238], [esc(r.path), r.id, esc(f.redirects[r.id] || ''), esc(r.value)])), ''), __nscVueJoin((target?.gateways || []).map(id => __nscVueView(templates[239], [id, f.gateways.includes(id) ? 'checked' : '', esc(G(id).name), esc(G(id).runtime)])), '') || __nscVueView(templates[240], []), f.tls ? 'checked' : '', f.includeSnippets ? 'checked' : '', f.includeSnippets ? 'open' : '', codeHTML(__nscVueJoin([c.serverSnippet, ...c.routes.map(r => r.snippet)].filter(Boolean), '\n')), f.reviewSnippets ? 'checked' : '']));
    if (f.step === 3) body = __nscVueAdd(body, __nscVueView(templates[242], [esc(P(f.project).name), esc(target.name), esc(f.name), badge(target.kind === 'production' ? '目标是生产环境' : '新草稿', target.kind === 'production' ? 'amber' : 'blue'), callout('只创建新域名的草稿。不覆盖目标已有资源，不改变来源，不发布，也不修改 DNS。', 'blue'), c.routes.length, f.includeSnippets ? ' · 已审阅自定义指令' : '', __nscVueJoin(Object.values(f.mapping).map(id => esc(S(id)?.name || id)), '、') || '没有代理服务', __nscVueJoin(f.gateways.map(id => esc(G(id).name)), ' / '), f.tls ? '使用目标域名的演示证书' : '先使用 HTTP，证书稍后配置']));
    body = __nscVueAdd(body, __nscVueView(templates[243], []));
    showModal({
      kind: 'p-copy',
      size: 'wide',
      title: '复制配置到其他环境',
      sub: __nscVueAdd(P(f.project).name, ' · 重新绑定资源，不直接克隆生产环境'),
      body,
      footer: __nscVueAdd(f.step > 1 ? B('上一步', 'p-copy-back', 'back') : B('取消', 'close'), B(f.step === 3 ? '创建目标环境草稿' : '下一步', f.step === 3 ? 'p-copy-create' : 'p-copy-next', f.step === 3 ? 'copy' : 'right', 'primary', f.step === 3 ? 'disabled id="p-copy-confirm"' : native ? 'disabled title="文件编辑模式不支持自动资源映射"' : ''))
    });
  }
  function validateCopy(step) {
    const f = copyFlow,
      s = copySource(),
      c = copyConfig(),
      e = E(f.target);
    if (!s || s.project !== f.project || f.project !== db.project) return '来源项目发生变化。';
    if (c.mode === 'native') return '文件编辑模式不能自动映射环境资源。';
    if (!e || e.project !== f.project || e.id === s.env) return '请选择同项目下的另一个目标环境。';
    if (!validHost(f.name)) return '填写合法的目标域名，例如 preview.example.net。';
    if (scopeDomains(e.id, e.project).some(d => d.name === f.name)) return '目标环境已有此域名。此向导只创建新草稿，不覆盖已有域名。';
    if (step >= 2) {
      if (!f.gateways.length) return '至少选择一个目标发布网关。';
      if (f.gateways.some(id => !e.gateways.includes(id))) return '目标网关不在目标环境可用范围。';
      const conflict = domainConflict(f.name, f.gateways);
      if (conflict) return __nscVueAdd(__nscVueAdd('相同入口已被 ', scopeText(conflict)), ' 占用。');
      for (const id of new Set(c.routes.filter(r => r.kind === 'proxy').map(r => r.service))) {
        const t = S(f.mapping[id]);
        if (!t || t.project !== e.project || t.env !== e.id) return '请为每个来源服务显式选择目标环境服务。';
      }
      for (const r of c.routes.filter(r => r.kind === 'redirect')) {
        try {
          const u = new URL(f.redirects[r.id] || '');
          if (!['http:', 'https:'].includes(u.protocol) || u.username || u.password) throw Error();
        } catch (_) {
          return '请重新填写各重定向路由的有效 HTTP / HTTPS 目标 URL。';
        }
      }
      if (f.includeSnippets && !f.reviewSnippets) return '复制自定义指令前，请审阅并确认环境专属引用。';
    }
    return '';
  }
  function copyNext() {
    collectCopy();
    const err = validateCopy(copyFlow.step);
    if (err) return formError(err);
    if (copyFlow.step === 1 && copySource().id === ui.domain && dirty() && !saveDraft(true)) return;
    copyFlow.step++;
    copyDialog();
  }
  function finishCopy() {
    if (!document.querySelector('#p-copy-final-ack')?.checked) return;
    const err = validateCopy(2);
    if (err) return formError(err);
    const f = copyFlow,
      s = copySource(),
      c = clone(copyConfig());
    c.mode = 'managed';
    c.native = '';
    c.tls = f.tls;
    c.redirect = f.tls && c.redirect;
    if (!f.includeSnippets) c.serverSnippet = '';
    c.routes = c.routes.map(r => ({
      ...r,
      id: uid('r'),
      service: r.kind === 'proxy' ? f.mapping[r.service] : '',
      value: r.kind === 'redirect' ? f.redirects[r.id] : r.value,
      snippet: f.includeSnippets ? r.snippet : ''
    }));
    const d = makeDraftDomain(f.name, f.target, f.project, [...f.gateways], c, {
      certReadyDemo: f.tls,
      copiedFrom: {
        project: s.project,
        env: s.env,
        domain: s.id,
        version: s.rev,
        time: stamp()
      }
    });
    selectScope(f.project, f.target, d.id);
    ui.page = 'domain';
    ui.tab = 'config';
    ui.configView = 'semantic';
    event('从另一环境创建草稿', __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(E(s.env).name, ' / '), s.name), ' → '), scopeText(d)), 'copy', d.id);
    closeModal(true);
    persist();
    updateHash();
    render();
    toast('目标环境草稿已创建。来源配置未改动，没有发布。');
    copyFlow = null;
  }
  serviceDetail = function (id) {
    V10.serviceDetail(id);
    if (modal) {
      const s = S(id);
      modal.sub = __nscVueJoin([P(s?.project)?.name, E(s?.env)?.name, s?.runtime].filter(Boolean), ' / ');
      renderModal();
    }
  };
  gatewaysPage = function () {
    const gs = db.gateways.filter(g => allEnvironmentView() ? listEnvironments().some(e => e.gateways.includes(g.id)) : gatewayAllowed(g));
    return __nscVueView(templates[246], [I('info', 'sm'), __nscVueJoin(gs.map(g => {
      const refs = db.environments.filter(e => e.gateways.includes(g.id));
      return __nscVueView(templates[244], [I('server'), esc(g.name), esc(g.ip), esc(g.runtime), __nscVueJoin(refs.map(e => badge(__nscVueAdd(__nscVueAdd(P(e.project).name, ' / '), e.name))), ''), B('查看域名生效状态', 'gateway-detail', 'right', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [g.id]))]);
    }), '') || __nscVueView(templates[245], []), B('设置环境可用网关', 'p-env-settings', 'settings', '', __nscVueInterpolate(["data-id=\"", "\""], [db.env]))]);
  };
  paletteResults = function (q) {
    q = q.trim().toLowerCase();
    const ds = db.domains.filter(d => (allProjectView() || d.project === db.project) && d.name.toLowerCase().includes(q)),
      ss = db.services.filter(s => (allProjectView() || s.project === db.project) && (s.name || s.id).toLowerCase().includes(q));
    return __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueView(templates[247], [allProjectView() ? '搜索全部项目及其环境' : __nscVueAdd(__nscVueAdd('仅搜索 ', esc(P().name)), ' 项目 · 可跨该项目环境')]), __nscVueJoin(ds.map(d => __nscVueView(templates[248], [d.id, I('globe'), esc(d.name), esc(P(d.project).name), esc(E(d.env).name), I('right', 'sm')])), '')), __nscVueJoin(ss.map(s => __nscVueView(templates[249], [s.id, I('box'), esc(s.name || s.id), esc(resourceScopeName(s)), esc(s.id)])), '')), !ds.length && !ss.length ? __nscVueView(templates[250], []) : '');
  };
  helpModal = function () {
    showModal({
      size: 'wide',
      title: '项目、环境与配置编辑',
      sub: 'NSC 1.1 · 离线交互原型',
      body: __nscVueView(templates[251], [callout('项目与环境是资源归属。云原生 / 运维是查看视角。表单 / 文件是编辑方式。这三件事互不替代。', 'blue')]),
      footer: __nscVueAdd(B('关闭', 'close'), B('导出工作区 JSON', 'export-state', 'download'))
    });
  };
  function domainTargetsModal() {
    const d = D();
    if (d.rev > 0) {
      toast('已发布域名需要独立的入口迁移流程。', true);
      return;
    }
    showModal({
      kind: 'domain-targets',
      size: 'small',
      title: '选择此域名的发布网关',
      sub: scopeText(),
      body: __nscVueView(templates[254], [__nscVueJoin(E().gateways.map(id => __nscVueView(templates[252], [id, d.gateways.includes(id) ? 'checked' : '', esc(G(id).name), esc(G(id).ip)])), '') || __nscVueView(templates[253], [])]),
      footer: __nscVueAdd(B('取消', 'close'), B('保存草稿目标', 'p-domain-targets-save', 'save', 'primary'))
    });
  }
  function saveDomainTargets() {
    const d = D();
    if (d.rev > 0) return;
    const ids = [...document.querySelectorAll('[name="draft-gateway"]:checked')].map(x => x.value);
    if (!ids.length) return formError('至少选择一个发布网关。');
    if (ids.some(id => !E().gateways.includes(id))) return formError('网关不在环境可用范围。');
    const conflict = domainConflict(d.name, ids, d.id);
    if (conflict) return formError(__nscVueAdd(__nscVueAdd('入口已被 ', scopeText(conflict)), ' 占用。'));
    d.gateways = ids;
    d.loaded = Object.fromEntries(ids.map(id => [id, 0]));
    ui.validation = null;
    persist();
    closeModal(true);
    render();
    toast('未发布域名的网关目标已保存，没有下发配置。');
  }
  // Capture project-layer events before the retained v1.0 central action router.
  listen(document, 'click', e => {
    const t = e.target.closest('[data-action]');
    if (!t || t.disabled) return;
    const a = t.dataset.action,
      id = t.dataset.id;
    if (!a.startsWith('p-') && a !== 'reset') return;
    e.preventDefault();
    e.stopImmediatePropagation();
    if (ui.externalConflict && !['p-file-backups', 'p-backup-export'].includes(a)) {
      toast('另一标签页已修改工作区。先导出草稿并重新载入，避免覆盖。', true);
      return;
    }
    if (deploymentTimer) {
      toast('正在模拟发布，请等待本次结果。', true);
      return;
    }
    switch (a) {
      case 'p-project':
        switchScope(id, null, 'project');
        break;
      case 'p-env':
        switchScope(db.project, id);
        break;
      case 'p-project-add':
        ensureNavigate(projectModal);
        break;
      case 'p-project-create':
        createProject();
        break;
      case 'p-env-add':
        ensureNavigate(() => envModal());
        break;
      case 'p-env-settings':
        ensureNavigate(() => envModal(id || db.env));
        break;
      case 'p-env-save':
        saveEnvironment();
        break;
      case 'p-env-delete':
        deleteEnvironment(id);
        break;
      case 'p-env-delete-confirm':
        {
          const e = E(id);
          if (!e || scopeDomains(id, e.project).length || scopeServices(id, e.project).length || projectEnvs(e.project).length < 2) {
            toast('环境不能删除。', true);
            break;
          }
          db.environments = db.environments.filter(x => x.id !== id);
          if (db.env === id) selectScope(db.project, projectEnvs()[0].id);
          ui.page = 'environments';
          closeModal(true);
          persist();
          updateHash();
          render();
          toast('空环境已删除。');
          break;
        }
      case 'p-env-domain-add':
        ensureNavigate(chooseEnvForDomain);
        break;
      case 'p-domain-targets':
        domainTargetsModal();
        break;
      case 'p-domain-targets-save':
        saveDomainTargets();
        break;
      case 'p-domain-in-env':
        selectScope(db.project, id);
        closeModal(true);
        ui.page = 'domains';
        updateHash();
        render();
        addDomainModal();
        break;
      case 'p-mode-file':
        changeMode(true);
        break;
      case 'p-mode-form':
        changeMode(false);
        break;
      case 'p-file-backups':
        fileBackups();
        break;
      case 'p-backup-export':
        {
          const f = D().fileBackups?.find(x => x.id === id);
          if (f) download(__nscVueAdd(__nscVueAdd(__nscVueAdd(D().name, '.'), f.id), '.conf'), f.text);
          break;
        }
      case 'p-service-add':
        ensureNavigate(serviceModal);
        break;
      case 'p-service-create':
        createService();
        break;
      case 'p-copy':
        ensureNavigate(beginCopy);
        break;
      case 'p-copy-next':
        copyNext();
        break;
      case 'p-copy-back':
        collectCopy();
        copyFlow.step--;
        copyDialog();
        break;
      case 'p-copy-create':
        finishCopy();
        break;
      case 'reset':
        db = seed();
        selectScope('portal', 'prod', 'd1');
        ui.page = 'project';
        ui.tab = 'overview';
        ui.configView = 'code';
        ui.externalConflict = false;
        persist();
        closeModal(true);
        updateHash();
        render();
        toast('已恢复 v1.1 演示数据。旧版本存储不受影响。');
        break;
    }
  }, true);
  listen(document, 'change', e => {
    const t = e.target;
    if (t.id === 'scope-project') {
      const next = t.value;
      t.value = listProject();
      if (next === 'all' || allProjectView()) hooks.navigate(projectHref(next));else switchScope(next);
    }
    if (t.id === 'scope-env') {
      const next = t.value;
      t.value = listEnvironment();
      hooks.navigate(environmentHref(next));
    }
    if (t.id === 'p-mode-ack') document.querySelector('#p-mode-confirm').disabled = !t.checked;
    if (t.id === 'p-copy-final-ack') document.querySelector('#p-copy-confirm').disabled = !t.checked;
    if (t.id === 'p-copy-source') {
      collectCopy();
      copyFlow.target = '';
      copyFlow.mapping = {};
      copyFlow.redirects = {};
      copyFlow.gateways = [];
      copyDialog();
    }
    if (t.id === 'p-copy-target') {
      collectCopy();
      copyFlow.mapping = {};
      copyFlow.redirects = {};
      copyFlow.gateways = [];
    }
  }, true);
  // A field correction dismisses the previous form-level error; submit revalidates.
  function clearEditedFormError(e) {
    if (e.target.closest('.modal-body')) {
      const out = document.querySelector('#p-form-error');
      if (out) out.replaceChildren();
    }
  }
  listen(document, 'input', clearEditedFormError, true);
  listen(document, 'change', clearEditedFormError, true);
  // Initialize valid scoped state before the first render. Do not silently import v1 data.
  for (const en of db.environments) ENV[en.id] = esc(en.name);
  if (!P(db.project)) {
    db.project = db.projects[0].id;
    db.env = projectEnvs()[0].id;
  }
  if (!E(db.env) || E(db.env).project !== db.project) db.env = projectEnvs()[0].id;
  selectScope(db.project, db.env);
  ui.page = 'project';

  /* NSC v1.2 — application marketplace, workload views and runtime history.
   * Offline product prototype. No Docker, SSH, DNS or registry requests.
   * Additive extension of the preserved v1.1 Domain/Nginx editor.
   */
  (() => {
    'use strict';

    const __renderContext1 = {
      get APP() {
        return APP;
      },
      get B() {
        return B;
      },
      get E() {
        return E;
      },
      get I() {
        return I;
      },
      get MODE() {
        return MODE;
      },
      get NODE() {
        return NODE;
      },
      get P() {
        return P;
      },
      get RT() {
        return RT;
      },
      get ST() {
        return ST;
      },
      get TB() {
        return TB;
      },
      get TEMPLATES() {
        return TEMPLATES;
      },
      get allEnvironmentView() {
        return allEnvironmentView;
      },
      get allProjectView() {
        return allProjectView;
      },
      get appServices() {
        return appServices;
      },
      get appSpec() {
        return appSpec;
      },
      get appState() {
        return appState;
      },
      get appTable() {
        return appTable;
      },
      get badge() {
        return badge;
      },
      get breadcrumb() {
        return breadcrumb;
      },
      get callout() {
        return callout;
      },
      get cardHead() {
        return cardHead;
      },
      get clusterSummary() {
        return clusterSummary;
      },
      get contextCrumb() {
        return contextCrumb;
      },
      get currentInstances() {
        return currentInstances;
      },
      get db() {
        return db;
      },
      get deployState() {
        return deployState;
      },
      get deployTable() {
        return deployTable;
      },
      get deploymentsPage() {
        return deploymentsPage;
      },
      get envName() {
        return envName;
      },
      get esc() {
        return esc;
      },
      get eventLabel() {
        return eventLabel;
      },
      get eventsTable() {
        return eventsTable;
      },
      get inListScope() {
        return inListScope;
      },
      get instanceTable() {
        return instanceTable;
      },
      get listDomains() {
        return listDomains;
      },
      get projectName() {
        return projectName;
      },
      get refsFor() {
        return refsFor;
      },
      get resourceScopeName() {
        return resourceScopeName;
      },
      get scopeName() {
        return scopeName;
      },
      get serviceFiltered() {
        return serviceFiltered;
      },
      get serviceState() {
        return serviceState;
      },
      get serviceTable() {
        return serviceTable;
      },
      get showModal() {
        return showModal;
      },
      get specText() {
        return specText;
      },
      get statusCN() {
        return statusCN;
      },
      get tabs() {
        return tabs;
      },
      get templates() {
        return templates;
      },
      get tpl() {
        return tpl;
      },
      get x() {
        return x;
      },
      get xCrumb() {
        return xCrumb;
      },
      get xFooter() {
        return xFooter;
      },
      get xHead() {
        return xHead;
      },
      get xMetric() {
        return xMetric;
      }
    };
    const OLD = {
      globalPage,
      projectSidebar,
      contextBar,
      projectPage,
      environmentOverview,
      serviceDetail,
      updateHash,
      parseHash,
      closeModal,
      render,
      ensureNavigate
    };
    const MODE = {
      stack: '集群应用 · Swarm Stack',
      compose: '单机应用 · Docker Compose',
      container: '单容器 · Docker',
      swarmservice: '单服务 · Docker Swarm',
      external: '外部管理'
    };
    const RT = id => db.runtimes.find(r => r.id === id);
    const APP = id => db.apps.find(a => a.id === id);
    const ST = id => db.services.find(s => s.id === id);
    const NODE = id => db.runtimes.flatMap(r => r.nodes).find(n => n.id === id);
    const IS = id => db.instances.find(t => t.id === id);
    const OP = id => db.appDeployments.find(o => o.id === id);
    const appServices = a => db.services.filter(s => s.app === a.id);
    const scopeName = (p, e) => __nscVueInterpolate(["", " / ", ""], [P(p)?.name || '未归属项目', E(e)?.name || '未分配环境']);
    const projectName = p => P(p)?.name || '未归属项目';
    const envName = e => E(e)?.name || '未分配环境';
    const xTime = () => stamp();
    const statusCN = {
      RUNNING: '运行中',
      PENDING: '等待调度',
      PREPARING: '准备启动',
      STARTING: '启动中',
      COMPLETE: '已完成',
      FAILED: '异常退出',
      SHUTDOWN: '已停止',
      REJECTED: '启动被拒绝',
      ORPHANED: '节点失联',
      UNKNOWN: '状态未知'
    };
    const x = {
      layout: db.serviceLayout || 'grouped',
      svcScope: 'environment',
      svcProject: 'all',
      svcRuntime: 'all',
      svcHealth: 'all',
      svcSearch: '',
      svcPage: 1,
      app: '',
      service: '',
      runtime: 'swarm-prod',
      appTab: 'overview',
      serviceTab: 'instances',
      runtimeTab: 'overview',
      includeHistory: false,
      marketCategory: 'all',
      marketSearch: '',
      historyTab: 'application',
      eventFilter: 'all',
      scenario: 'success',
      op: '',
      wizard: null,
      wizardError: '',
      closeTarget: null,
      selectedInstance: null
    };
    const TEMPLATES = [{
      id: 'portal',
      name: 'Web 应用套件',
      key: 'web-suite',
      glyph: 'W',
      cat: '业务应用',
      color: 'blue',
      desc: '前端 + API，体验多服务部署、按环境配置与域名接入。',
      modes: ['compose', 'stack'],
      version: '演示模板 1.0',
      services: [{
        name: 'web',
        image: 'demo/web:1.0.0',
        port: 3000,
        http: true
      }, {
        name: 'api',
        image: 'demo/api:1.0.0',
        port: 8080,
        http: true
      }],
      stateful: false,
      notes: '镜像名为占位示例；不连接镜像仓库。'
    }, {
      id: 'wordpress',
      name: 'WordPress',
      key: 'wordpress',
      glyph: 'W',
      cat: '内容管理',
      color: 'purple',
      desc: '内容管理应用与数据库。先确认数据位置，再选择访问入口。',
      modes: ['compose'],
      version: '演示模板 1.0',
      services: [{
        name: 'wordpress',
        image: 'wordpress:6.8-apache',
        port: 80,
        http: true
      }, {
        name: 'database',
        image: 'mariadb:11.4',
        port: 3306,
        http: false
      }],
      stateful: true,
      secret: true,
      notes: '此演示模板只提供单机部署；不将本地数据卷假装成集群共享存储。'
    }, {
      id: 'postgres',
      name: 'PostgreSQL',
      key: 'postgres',
      glyph: 'P',
      cat: '数据库',
      color: 'blue',
      desc: '内部数据库。默认不绑定域名，也不开放主机端口。',
      modes: ['compose', 'container'],
      version: '演示模板 1.0',
      services: [{
        name: 'postgres',
        image: 'postgres:17',
        port: 5432,
        http: false
      }],
      stateful: true,
      secret: true,
      notes: '只演示单实例，不声明数据库高可用或备份能力。'
    }, {
      id: 'redis',
      name: 'Redis',
      key: 'redis',
      glyph: 'R',
      cat: '数据库',
      color: '',
      desc: '内部缓存。将凭据与持久化配置留在当前环境。',
      modes: ['compose', 'container'],
      version: '演示模板 1.0',
      services: [{
        name: 'redis',
        image: 'redis:7.4',
        port: 6379,
        http: false
      }],
      stateful: true,
      secret: true,
      notes: '没有自动生成 Redis Cluster；模板升级不会替你迁移数据。'
    }, {
      id: 'adminer',
      name: '数据库管理工具',
      key: 'db-admin',
      glyph: 'A',
      cat: '开发工具',
      color: 'purple',
      desc: '轻量数据库管理页面。访问方式默认仅内部，先部署再接入域名。',
      modes: ['compose', 'stack', 'container', 'swarmservice'],
      version: '演示模板 1.0',
      services: [{
        name: 'adminer',
        image: 'adminer:4',
        port: 8080,
        http: true
      }],
      stateful: false,
      notes: '对外提供管理入口前需配置独立认证；本原型不替代登录保护。'
    }, {
      id: 'worker',
      name: '后台任务处理器',
      key: 'worker',
      glyph: 'Q',
      cat: '业务应用',
      color: '',
      desc: '不需要域名的应用，展示工作进程与运行实例历史。',
      modes: ['compose', 'stack', 'container', 'swarmservice'],
      version: '演示模板 1.0',
      services: [{
        name: 'worker',
        image: 'demo/worker:1.0.0',
        port: 0,
        http: false
      }],
      stateful: false,
      notes: '这是长期运行的消费者模板，不把一次性 Job 当成故障。'
    }];
    const tpl = id => TEMPLATES.find(t => t.id === id) || TEMPLATES[0];
    function node(id, name, role = 'worker', status = 'Ready', availability = 'Active', cpu = 14, used = 4, total = 16) {
      return {
        id,
        name,
        role,
        status,
        availability,
        manager: role === 'manager' ? name.endsWith('01') ? 'Leader' : 'Reachable' : null,
        cpu,
        used,
        total,
        agent: status === 'Ready',
        sample: '2026-09-14 10:32:05'
      };
    }
    function seedWorkloads() {
      if (db.workloadSchema === 12) return;
      db.workloadSchema = 12;
      db.serviceLayout = 'grouped';
      db.apps = [];
      db.instances = [];
      db.appDeployments = [];
      for (const a of db.apps) {
        a.revisions[0].spec = clone(appSpec(a));
        for (const s of a.revisions[0].spec.services) s.memory = 256;
      }
      db.runtimeEvents = [];
      db.appDrafts = [];
      db.runtimes = [{
        id: 'swarm-prod',
        name: '生产 Swarm 集群',
        type: 'swarm',
        engineId: 'DEMO-SWARM-PROD',
        connection: 'online',
        canWrite: true,
        managerRead: true,
        observed: '2026-09-14 10:32:08',
        historySince: '2026-09-12 00:00:00',
        nodes: [node('p-m1', 'manager-01', 'manager', 'Ready', 'Active', 12, 3.2), node('p-m2', 'manager-02', 'manager', 'Ready', 'Active', 8, 2.8), node('p-m3', 'manager-03', 'manager', 'Ready', 'Active', 11, 3.1), node('p-w1', 'worker-01', 'worker', 'Ready', 'Active', 31, 7.4), node('p-w2', 'worker-02', 'worker', 'Ready', 'Active', 26, 6.8), node('p-w3', 'worker-03', 'worker', 'Down', 'Active', null, null)],
        allowedEnvs: ['prod'],
        networks: ['app-overlay', 'gateway-overlay'],
        gaps: [{
          from: '10:15:12',
          to: '至今',
          node: 'p-w3',
          reason: '节点事件采集中断，状态与日志无法实时核实。'
        }]
      }, {
        id: 'swarm-test',
        name: '测试 Swarm 集群',
        type: 'swarm',
        engineId: 'DEMO-SWARM-TEST',
        connection: 'online',
        canWrite: true,
        managerRead: true,
        observed: '2026-09-14 10:32:08',
        historySince: '2026-09-12 00:00:00',
        nodes: [node('t-m1', 'test-manager-01', 'manager', 'Ready', 'Active', 12, 2.5, 8), node('t-w1', 'test-worker-01', 'worker', 'Ready', 'Active', 18, 3.1, 8), node('t-w2', 'test-worker-02', 'worker', 'Ready', 'Active', 22, 4, 8)],
        allowedEnvs: ['test', 'sandbox'],
        networks: ['app-overlay', 'gateway-overlay'],
        gaps: []
      }, {
        id: 'docker-dev',
        name: '开发 Docker 主机',
        type: 'docker',
        engineId: 'DEMO-ENGINE-DEV',
        connection: 'online',
        canWrite: true,
        observed: '2026-09-14 10:32:05',
        historySince: '2026-09-12 00:00:00',
        nodes: [node('d-h1', 'dev-host', 'host', 'Ready', 'Active', 19, 3.5, 8)],
        allowedEnvs: ['dev'],
        networks: ['app-bridge', 'gateway-bridge'],
        gaps: []
      }, {
        id: 'docker-docs',
        name: '文档 Docker 主机',
        type: 'docker',
        engineId: 'DEMO-ENGINE-DOCS',
        connection: 'online',
        canWrite: true,
        observed: '2026-09-14 10:32:05',
        historySince: '2026-09-12 00:00:00',
        nodes: [node('docs-h1', 'docs-host', 'host', 'Ready', 'Active', 6, 1.6, 4)],
        allowedEnvs: ['docs-prod'],
        networks: ['app-bridge', 'gateway-bridge'],
        gaps: []
      }];
      const defs = [['app-portal-prod', '客户门户', 'portal', 'prod', 'swarm-prod', 'stack', ['web-frontend', 'api-service', 'static-service'], true], ['app-console', '管理控制台', 'portal', 'prod', 'swarm-prod', 'swarmservice', ['console-service'], false], ['app-portal-test', '客户门户', 'portal', 'test', 'swarm-test', 'stack', ['test-web-frontend', 'test-api-service', 'test-static-service'], true], ['app-portal-sandbox', '客户门户', 'portal', 'sandbox', 'swarm-test', 'stack', ['sandbox-web-frontend', 'sandbox-api-service', 'sandbox-static-service'], true], ['app-portal-dev', '客户门户', 'portal', 'dev', 'docker-dev', 'compose', ['dev-web-frontend', 'dev-api-service', 'dev-static-service'], true], ['app-docs', '团队文档', 'docs', 'docs-prod', 'docker-docs', 'container', ['docs-web'], true]];
      for (const [id, name, project, env, runtime, mode, ids, managed] of defs) {
        const a = {
          id,
          name,
          key: id.replace('app-', ''),
          project,
          env,
          runtime,
          mode,
          managed,
          version: 3,
          source: managed ? '已保存部署定义' : '外部发现 · 未接管',
          stateful: false,
          created: '2026-09-12 09:00:00',
          revisions: [],
          draft: null
        };
        db.apps.push(a);
        for (const sid of ids) {
          const s = ST(sid);
          if (!s) continue;
          s.app = id;
          s.runtimeId = runtime;
          s.runtime = RT(runtime).name;
          s.mode = mode;
          s.managed = managed;
          s.workloadKind = 'replicated';
          s.desired = s.endpoints.filter(e => e[1]).length;
          s.port = s.port || 0;
          s.memory = 512;
          s.specVersion = 3;
          s.observedVersion = 3;
          s.health = 'healthy';
          s.stateful = false;
          s.endpoints = s.endpoints.filter(e => e[1]);
          for (let i = 0; i < s.desired; i++) {
            const rt = RT(runtime),
              ns = rt.nodes.filter(n => n.status === 'Ready' && n.role !== 'manager');
            const n = ns[i % ns.length] || rt.nodes[0];
            db.instances.push({
              id: __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(rt.type === 'swarm' ? 'task-' : 'container-', sid), '-'), __nscVueAdd(i, 1)), '-v3'),
              service: sid,
              app: id,
              runtime,
              node: n.id,
              slot: __nscVueAdd(i, 1),
              kind: rt.type === 'swarm' ? 'task' : 'container',
              containerId: __nscVueAdd(__nscVueAdd(__nscVueAdd('ctr-', sid), '-'), __nscVueAdd(i, 1)),
              state: 'RUNNING',
              desiredState: 'RUNNING',
              current: true,
              started: '2026-09-13 17:45:06',
              finished: null,
              exitCode: null,
              oomKilled: null,
              health: 'healthy',
              cause: '发布更新',
              causeType: 'planned',
              restartCount: 0,
              release: __nscVueAdd('APP-DEP-', id),
              previous: null,
              logs: '[DEMO] Service initialized\n[DEMO] Listening for requests\n[DEMO] Readiness check passed'
            });
          }
        }
        a.revisions = [{
          v: 2,
          time: '2026-09-12 09:00:00',
          note: '初次部署（演示）',
          spec: appSpec(a)
        }, {
          v: 3,
          time: '2026-09-13 17:45:00',
          note: '更新应用（演示）',
          spec: appSpec(a)
        }];
        db.appDeployments.push({
          id: __nscVueAdd('APP-DEP-', id),
          app: id,
          project,
          env,
          runtime,
          version: 3,
          previous: 2,
          time: '2026-09-13 17:45:00',
          actor: managed ? 'admin' : '外部操作 · 执行者未知',
          action: '更新应用',
          state: 'success',
          snapshot: clone(a.revisions[1].spec),
          targets: ids.slice(),
          results: Object.fromEntries(ids.map(i => [i, 'success'])),
          attempts: [{
            time: '2026-09-13 17:45:00',
            number: 1,
            logs: ['[DEMO] 已保存应用定义 r3', '[DEMO] 逐服务核实完成'],
            state: 'success'
          }],
          stages: 4,
          note: '种子记录，不是真实观测'
        });
      }
      // A successful one-off job is not an unavailable replicated service.
      const job = {
        id: 'db-migrate',
        name: '数据库迁移任务',
        project: 'portal',
        env: 'prod',
        app: 'app-portal-prod',
        runtimeId: 'swarm-prod',
        runtime: '生产 Swarm 集群',
        mode: 'stack',
        managed: true,
        workloadKind: 'job',
        desired: 1,
        port: 0,
        endpoints: [],
        image: 'demo/migrate:1.0.0',
        memory: 256,
        specVersion: 3,
        observedVersion: 3,
        health: 'not-configured',
        stateful: false
      };
      db.services.push(job);
      db.instances.push({
        id: 'task-migrate-1',
        service: job.id,
        app: job.app,
        runtime: job.runtimeId,
        node: 'p-w1',
        slot: 1,
        kind: 'task',
        containerId: 'ctr-migrate-1',
        state: 'COMPLETE',
        desiredState: 'COMPLETE',
        current: true,
        started: '2026-09-13 17:44:50',
        finished: '2026-09-13 17:44:57',
        exitCode: 0,
        oomKilled: false,
        health: 'not-applicable',
        cause: '正常完成',
        causeType: 'completed',
        restartCount: 0,
        release: 'APP-DEP-app-portal-prod',
        previous: null,
        logs: '[DEMO] Migration completed successfully. Exit 0.'
      });
      // Realistic replacement lineage: old failed task does not return to RUNNING.
      const newTask = db.instances.find(t => t.service === 'api-service' && t.slot === 2);
      newTask.previous = 'task-api-old-02';
      newTask.started = '2026-09-14 10:18:51';
      newTask.cause = '自动恢复';
      newTask.causeType = 'automatic';
      newTask.release = null;
      db.instances.push({
        ...clone(newTask),
        id: 'task-api-old-02',
        containerId: 'ctr-api-old-02',
        current: false,
        state: 'FAILED',
        desiredState: 'SHUTDOWN',
        started: '2026-09-13 17:45:06',
        finished: '2026-09-14 10:18:42',
        exitCode: 137,
        oomKilled: true,
        health: 'unhealthy',
        cause: '内存不足退出 · 已确认（演示证据）',
        causeType: 'fault',
        previous: null,
        replacement: newTask.id,
        release: 'APP-DEP-app-portal-prod',
        logs: '[DEMO] 10:18:39 allocation pressure\n[DEMO] 10:18:42 container terminated; exit=137\n[DEMO] Engine snapshot: OOMKilled=true'
      });
      const initial = db.instances.find(t => t.service === 'api-service' && t.slot === 1);
      db.instances.push({
        ...clone(initial),
        id: 'task-api-planned-01',
        containerId: 'ctr-api-planned-01',
        current: false,
        state: 'SHUTDOWN',
        desiredState: 'SHUTDOWN',
        started: '2026-09-12 09:00:15',
        finished: '2026-09-13 17:45:01',
        exitCode: 143,
        oomKilled: false,
        cause: '按计划更新',
        causeType: 'planned',
        replacement: initial.id,
        logs: '[DEMO] received SIGTERM; graceful shutdown'
      });
      // A runtime can contain services not assigned to an NSC project/application.
      const ext = {
        id: 'external-exporter',
        name: '外部监控采集器',
        project: null,
        env: null,
        app: null,
        runtimeId: 'swarm-prod',
        runtime: '生产 Swarm 集群',
        mode: 'external',
        managed: false,
        workloadKind: 'global',
        desired: 5,
        port: 9100,
        endpoints: [],
        image: 'demo/exporter:1.0',
        memory: null,
        specVersion: null,
        observedVersion: null,
        health: 'not-configured',
        stateful: false
      };
      db.services.push(ext);
      for (const n of RT('swarm-prod').nodes.filter(n => n.status === 'Ready')) db.instances.push({
        id: __nscVueAdd('task-exporter-', n.id),
        service: ext.id,
        app: null,
        runtime: ext.runtimeId,
        node: n.id,
        slot: n.id,
        kind: 'task',
        containerId: __nscVueAdd('ctr-exp-', n.id),
        current: true,
        state: 'RUNNING',
        desiredState: 'RUNNING',
        started: '2026-09-12 08:00:00',
        finished: null,
        exitCode: null,
        oomKilled: null,
        health: 'not-configured',
        cause: '外部管理',
        causeType: 'external',
        restartCount: 0,
        release: null,
        logs: null
      });
      for (const a of db.apps) {
        if (a.id === 'app-portal-prod') {
          a.revisions[1].spec = appSpec(a);
          const op = db.appDeployments.find(o => o.app === a.id);
          op.snapshot = clone(a.revisions[1].spec);
          op.targets.push(job.id);
          op.results[job.id] = 'success';
        }
      }
      db.runtimeEvents = [{
        id: 'INC-018',
        time: '2026-09-14 10:18:42',
        end: '2026-09-14 10:18:51',
        title: 'API 实例异常退出，已自动替换',
        runtime: 'swarm-prod',
        project: 'portal',
        env: 'prod',
        app: 'app-portal-prod',
        service: 'api-service',
        node: 'p-w2',
        category: 'fault',
        state: 'recovered',
        actor: 'Docker Swarm',
        task: 'task-api-old-02',
        replacement: newTask.id,
        confidence: 'confirmed',
        evidence: '退出码 137 + OOMKilled=true + 节点 oom 事件（均为演示数据）',
        release: null,
        detail: '同一服务、同一副本位置出现新 Task。恢复耗时 9 秒；旧记录保留。'
      }, {
        id: 'GAP-003',
        time: '2026-09-14 10:15:12',
        title: 'worker-03 离线，历史采集存在缺口',
        runtime: 'swarm-prod',
        project: null,
        env: null,
        app: null,
        service: null,
        node: 'p-w3',
        category: 'gap',
        state: 'open',
        actor: '采集器',
        task: null,
        confidence: 'observed',
        evidence: 'Manager 状态快照：Down；节点采集连接不可用',
        detail: '无法判断未采集窗口内是否发生容器重启。不能显示 0 次。'
      }, {
        id: 'EV-JOB-01',
        time: '2026-09-13 17:44:57',
        title: '数据库迁移任务正常完成',
        runtime: 'swarm-prod',
        project: 'portal',
        env: 'prod',
        app: 'app-portal-prod',
        service: 'db-migrate',
        node: 'p-w1',
        category: 'completed',
        state: 'closed',
        actor: '应用任务',
        task: 'task-migrate-1',
        confidence: 'confirmed',
        evidence: 'Task COMPLETE，退出码 0',
        release: 'APP-DEP-app-portal-prod',
        detail: '一次性任务完成，不触发异常告警。'
      }, {
        id: 'EV-PLAN-01',
        time: '2026-09-13 17:45:01',
        title: '部署 r3 按计划替换旧实例',
        runtime: 'swarm-prod',
        project: 'portal',
        env: 'prod',
        app: 'app-portal-prod',
        service: 'api-service',
        node: 'p-w1',
        category: 'planned',
        state: 'closed',
        actor: 'admin',
        task: 'task-api-planned-01',
        confidence: 'confirmed',
        evidence: '与 APP-DEP-app-portal-prod 的目标记录关联',
        release: 'APP-DEP-app-portal-prod',
        detail: '退出码 143 出现在已确认的更新操作中，不计入意外故障。'
      }];
      persist();
    }
    function appSpec(a) {
      return {
        app: a.id,
        name: a.name,
        project: a.project,
        env: a.env,
        runtime: a.runtime,
        mode: a.mode,
        ...(a.revisions?.at(-1)?.spec?.composeBundle ? {
          composeBundle: clone(a.revisions.at(-1).spec.composeBundle)
        } : {}),
        services: appServices(a).map(s => ({
          id: s.id,
          name: s.name || s.id,
          image: s.image,
          replicas: s.desired ?? 1,
          memory: s.memory ?? 512,
          port: s.port,
          kind: s.workloadKind || 'replicated'
        })),
        source: a.source,
        raw: a.raw || '',
        stateful: a.stateful,
        secretRef: a.secretRef || null,
        access: a.access || null
      };
    }
    seedWorkloads();
    function runtimeGate(r) {
      if (!r) return '未选择部署目标';
      if (r.connection !== 'online') return '连接不可用；先核实实际状态，不能盲目提交';
      if (!r.canWrite) return '当前连接只允许观察；尚未授权应用部署';
      if (r.type === 'swarm') {
        if (!r.managerRead) return '需要连接有管理能力的 Manager';
        const m = r.nodes.filter(n => n.role === 'manager');
        if (m.filter(n => n.manager === 'Leader' || n.manager === 'Reachable').length < __nscVueAdd(Math.floor(m.length / 2), 1)) return '管理节点未达到法定多数；禁止提交变更';
      }
      return '';
    }
    function currentInstances(s) {
      return db.instances.filter(t => t.service === s.id && t.current);
    }
    function serviceState(s) {
      const r = RT(s.runtimeId);
      if (!s.runtimeId) return {
        name: '仅登记',
        tone: '',
        key: 'external',
        detail: '未连接运行实例'
      };
      if (r?.connection !== 'online') return {
        name: '状态未知',
        tone: 'amber',
        key: 'unknown',
        detail: '连接不可用，保留最后快照'
      };
      if (!s.runtimeId) return {
        name: '仅登记',
        tone: '',
        key: 'external',
        detail: '未连接运行实例'
      };
      const ins = currentInstances(s);
      if (s.specVersion && s.observedVersion !== s.specVersion) return {
        name: '版本待核实',
        tone: 'amber',
        key: 'issue',
        detail: __nscVueAdd(__nscVueAdd(__nscVueAdd('期望 r', s.specVersion), ' / 观察 '), s.observedVersion ? __nscVueAdd('r', s.observedVersion) : '未知')
      };
      if (s.workloadKind === 'job') {
        if (ins.some(t => t.state === 'COMPLETE')) return {
          name: '已完成',
          tone: 'green',
          key: 'complete',
          detail: '一次性任务正常结束'
        };
        if (ins.some(t => ['FAILED', 'REJECTED'].includes(t.state))) return {
          name: '执行失败',
          tone: 'red',
          key: 'issue',
          detail: '查看任务退出记录'
        };
        return {
          name: '执行中',
          tone: 'blue',
          key: 'running',
          detail: '一次性任务'
        };
      }
      if (s.desired === 0) return {
        name: '已停止',
        tone: '',
        key: 'stopped',
        detail: '期望副本为 0'
      };
      const run = ins.filter(t => t.state === 'RUNNING').length;
      if (run < (s.desired ?? 1)) return {
        name: '副本不足',
        tone: 'amber',
        key: 'issue',
        detail: __nscVueInterpolate(["", " / ", " \u4E2A\u8FD0\u884C"], [run, s.desired])
      };
      if (ins.some(t => t.health === 'unhealthy')) return {
        name: '健康检查失败',
        tone: 'red',
        key: 'issue',
        detail: __nscVueInterpolate(["\u8FD0\u884C ", " / ", "\uFF0C\u5065\u5EB7\u672A\u901A\u8FC7"], [run, s.desired])
      };
      if (s.health === 'not-configured') return {
        name: '运行中 · 未设健康检查',
        tone: 'blue',
        key: 'running',
        detail: __nscVueInterpolate(["", " / ", " \u4E2A\u8FD0\u884C"], [run, s.desired])
      };
      return {
        name: '运行正常',
        tone: 'green',
        key: 'healthy',
        detail: __nscVueInterpolate(["", " / ", " \u4E2A\u8FD0\u884C"], [run, s.desired])
      };
    }
    function appState(a) {
      const s = appServices(a);
      if (RT(a.runtime)?.connection !== 'online') return {
        name: '状态未知',
        tone: 'amber'
      };
      if (!s.length) return {
        name: '待部署',
        tone: ''
      };
      const st = s.map(serviceState);
      if (st.some(i => i.key === 'issue')) return {
        name: '需要处理',
        tone: 'amber'
      };
      if (st.every(i => i.key === 'stopped')) return {
        name: '已停止',
        tone: ''
      };
      return {
        name: '运行正常',
        tone: 'green'
      };
    }
    function xMetric(...args) {
      return __applicationsViews.xMetric_45(__renderContext1, ...args);
    }
    function xHead(...args) {
      return __applicationsViews.xHead_46(__renderContext1, ...args);
    }
    function xCrumb(...args) {
      return __applicationsViews.xCrumb_47(__renderContext1, ...args);
    }
    function xFooter(...args) {
      return __applicationsViews.xFooter_48(__renderContext1, ...args);
    }
    function refsFor(s) {
      return db.domains.flatMap(d => (d.draft?.config || d.active).routes.filter(r => r.service === s.id).map(r => ({
        d,
        r
      })));
    }
    function jump(page, id = '', tab = null) {
      ensureNavigate(() => {
        closeModal(true);
        if (page === 'application') {
          const a = APP(id);
          if (!a) return;
          selectScope(a.project, a.env);
          x.app = id;
          x.appTab = tab || 'overview';
        }
        if (page === 'service') {
          const s = ST(id);
          if (!s) return;
          if (s.project && s.env) selectScope(s.project, s.env);
          x.service = id;
          x.serviceTab = tab || 'instances';
          x.includeHistory = false;
        }
        if (page === 'runtime') {
          if (!RT(id)) return;
          x.runtime = id;
          x.runtimeTab = tab || 'overview';
        }
        if (page === 'applications') x.app = '';
        ui.page = page;
        ui.menu = false;
        updateHash();
        render();
        window.scrollTo(0, 0);
      });
    }
    Object.assign(PAGE_NAMES, {
      applications: '应用',
      market: '应用市场',
      application: '应用详情',
      service: '服务详情',
      runtimes: '集群与主机',
      runtime: '集群详情',
      workloadHistory: '部署与变更',
      runtimeEvents: '运行记录'
    });
    projectSidebar = function () {
      return __nscVueView(templates[261], [ui.menu ? 'open' : '', I('back', 'sm'), db.projects.length, I('folder'), allProjectView() ? 'selected' : '', __nscVueJoin(db.projects.map(p => __nscVueView(templates[259], [p.id, !allProjectView() && p.id === db.project ? 'selected' : '', esc(p.name)])), ''), I('chevron', 'sm'), allProjectView() ? '跨项目资源总览' : esc(P()?.key), navEntry('project', '项目总览', 'grid'), navEntry('environments', '环境与域名', 'layers'), navEntry('variables', '变量与配置', 'sliders'), E()?.kind === 'production' ? '' : 'gray', allEnvironmentView() ? 'selected' : '', __nscVueJoin((allProjectView() ? [] : projectEnvs()).map(e => __nscVueView(templates[260], [e.id, !allEnvironmentView() && e.id === db.env ? 'selected' : '', esc(e.name)])), ''), I('chevron', 'sm'), navEntry('applications', '应用', 'box', db.apps.filter(inListScope).length), navEntry('services', '服务', 'layers'), navEntry('domains', '域名', 'globe', listDomains().length), navEntry('routes', '路由规则', 'route'), navEntry('configs', 'Nginx 配置', 'code'), navEntry('certificates', 'TLS / 证书', 'shield'), navEntry('workloadHistory', '部署与变更', 'history'), navEntry('market', '应用市场', 'grid'), navEntry('runtimes', '集群与主机', 'server'), navEntry('runtimeEvents', '运行记录', 'activity'), navEntry('gateways', '可用网关', 'route'), I('play', 'sm'), badge('1.3'), TB('故障与部署演示', 'x-demo', 'settings')]);
    };
    contextBar = function () {
      if (['market', 'runtimes', 'runtime', 'runtimeEvents'].includes(ui.page) || ui.page === 'services' && x.svcScope === 'all' && !allProjectView()) return '';
      return OLD.contextBar();
    };
    projectPage = function () {
      let h = OLD.projectPage();
      return insertView(h, '.project-bottom-grid', __nscVueView(templates[262], [cardHead('项目应用', '应用是一起部署和维护的一组服务；同一应用在不同环境中独立运行。', B('部署应用', 'x-install', 'plus', 'sm'), 'box'), appTable(db.apps.filter(a => a.project === db.project))]), 'before');
    };
    environmentOverview = function () {
      return insertView(OLD.environmentOverview(), '[data-action=domain-add]', __nscVueAdd(__nscVueAdd(B('部署应用', 'x-install', 'plus', 'primary'), ' '), B('绑定域名', 'domain-add', 'globe')), 'replace');
    };
    globalPage = function () {
      if (ui.page === 'applications') return applicationsPage();
      if (ui.page === 'market') return marketPage();
      if (ui.page === 'services') return servicesWorkspace();
      if (ui.page === 'application') return applicationPage();
      if (ui.page === 'service') return servicePage();
      if (ui.page === 'runtimes') return runtimesPage();
      if (ui.page === 'runtime') return runtimePage();
      if (ui.page === 'workloadHistory') return historyPage();
      if (ui.page === 'runtimeEvents') return runtimeEventsPage();
      return OLD.globalPage();
    };
    serviceDetail = function (id) {
      const s = ST(id);
      if (s?.runtimeId) return jump('service', id);
      return OLD.serviceDetail(id);
    };
    updateHash = function () {
      if (booting) return;
      if (['runtimes', 'runtime', 'runtimeEvents', 'market'].includes(ui.page)) {
        let h = __nscVueAdd(__nscVueAdd('#/', ui.page), ui.page === 'runtime' ? __nscVueAdd(__nscVueAdd(__nscVueAdd('/', encodeURIComponent(x.runtime)), '/'), x.runtimeTab) : '');
        if (location.hash !== h) history.replaceState(null, '', h);
        return;
      }
      if (ui.page === 'application' || ui.page === 'service') {
        const id = ui.page === 'application' ? x.app : x.service;
        const obj = ui.page === 'application' ? APP(id) : ST(id);
        if (!obj) return;
        const h = obj.project ? __nscVueInterpolate(["#/projects/", "/environments/", "/", "/", "/", ""], [obj.project, obj.env, ui.page, encodeURIComponent(id), ui.page === 'application' ? x.appTab : x.serviceTab]) : __nscVueInterpolate(["#/service/", "/", ""], [encodeURIComponent(id), x.serviceTab]);
        if (location.hash !== h) history.replaceState(null, '', h);
        return;
      }
      OLD.updateHash();
    };
    parseHash = function () {
      const a = location.hash.replace(/^#\/?/, '').split('/').map(v => {
        try {
          return decodeURIComponent(v);
        } catch (_) {
          return '';
        }
      });
      if (['market', 'runtimes', 'runtimeEvents'].includes(a[0])) {
        ui.page = a[0];
        return;
      }
      if (a[0] === 'runtime' && RT(a[1])) {
        ui.page = 'runtime';
        x.runtime = a[1];
        x.runtimeTab = ['overview', 'nodes', 'services', 'events'].includes(a[2]) ? a[2] : 'overview';
        return;
      }
      if (a[0] === 'service' && ST(a[1])) {
        ui.page = 'service';
        x.service = a[1];
        x.serviceTab = ['instances', 'events', 'config', 'routes'].includes(a[2]) ? a[2] : 'instances';
        return;
      }
      if (a[0] === 'projects' && ['applications', 'workloadHistory'].includes(a[4]) && selectScope(a[1], a[3])) {
        ui.page = a[4];
        return;
      }
      if (a[0] === 'projects' && ['application', 'service'].includes(a[4])) {
        const obj = a[4] === 'application' ? APP(a[5]) : ST(a[5]);
        if (obj && obj.project === a[1] && obj.env === a[3]) {
          selectScope(obj.project, obj.env);
          ui.page = a[4];
          if (a[4] === 'application') {
            x.app = obj.id;
            x.appTab = ['overview', 'config', 'history', 'events'].includes(a[6]) ? a[6] : 'overview';
          } else {
            x.service = obj.id;
            x.serviceTab = ['instances', 'events', 'config', 'routes'].includes(a[6]) ? a[6] : 'instances';
          }
          return;
        }
      }
      OLD.parseHash();
    };
    function appTable(...args) {
      return __applicationsViews.appTable_49(__renderContext1, ...args);
    }
    function applicationsPage(...args) {
      return __applicationsViews.applicationsPage_50(__renderContext1, ...args);
    }
    function marketPage(...args) {
      return __applicationsViews.marketPage_51(__renderContext1, ...args);
    }
    function serviceFiltered() {
      return db.services.filter(s => {
        if (x.svcScope === 'environment' && (s.project !== db.project || s.env !== db.env)) return false;
        if (x.svcScope === 'project' && s.project !== db.project) return false;
        if (x.svcProject !== 'all' && !allProjectView() && s.project !== x.svcProject) return false;
        if (x.svcRuntime !== 'all' && s.runtimeId !== x.svcRuntime) return false;
        const st = serviceState(s);
        if (x.svcHealth === 'issue' && !['issue', 'unknown'].includes(st.key)) return false;
        if (x.svcHealth === 'external' && s.managed) return false;
        return __nscVueInterpolate(["", " ", " ", " ", ""], [s.name || s.id, s.id, APP(s.app)?.name || '', s.image]).toLowerCase().includes(x.svcSearch.toLowerCase());
      });
    }
    function serviceTable(...args) {
      return __applicationsViews.serviceTable_52(__renderContext1, ...args);
    }
    function servicesWorkspace(...args) {
      return __applicationsViews.servicesWorkspace_53(__renderContext1, ...args);
    }
    function tabs(...args) {
      return __applicationsViews.tabs_54(__renderContext1, ...args);
    }
    function applicationPage(...args) {
      return __applicationsViews.applicationPage_55(__renderContext1, ...args);
    }
    function servicePage(...args) {
      return __applicationsViews.servicePage_56(__renderContext1, ...args);
    }
    function instanceTable(...args) {
      return __applicationsViews.instanceTable_57(__renderContext1, ...args);
    }
    function instanceDetail(id) {
      const t = IS(id);
      if (!t) return;
      const s = ST(t.service),
        prev = t.previous ? IS(t.previous) : null,
        next = t.replacement ? IS(t.replacement) : db.instances.find(i => i.previous === t.id);
      x.selectedInstance = id;
      showModal({
        kind: 'x-instance',
        size: 'xwide',
        title: __nscVueAdd('实例详情 · ', statusCN[t.state]),
        sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(s?.name || t.service, ' / '), NODE(t.node)?.name || t.node), ' · 演示快照'),
        body: __nscVueView(templates[315], [t.kind === 'task' ? 'Task ID' : 'Container ID', esc(t.id), esc(t.containerId), badge(statusCN[t.state], t.state === 'FAILED' ? 'red' : 'blue'), t.state, t.exitCode === null ? '未知 / 尚未退出' : t.exitCode, t.oomKilled === null ? '未知' : t.oomKilled, esc(t.cause), t.restartCount, t.release ? TB(esc(t.release), 'x-deployment', '', __nscVueInterpolate(["data-id=\"", "\""], [t.release])) : '无；自动恢复不创建虚假的人工部署', t.oomKilled === true ? '本例同时有 OOMKilled=true 与节点 oom 事件，才标记为内存不足。' : '单独退出码 137 不能证明内存不足；143 也不自动表示故障。', prev ? __nscVueView(templates[311], [TB(esc(prev.id), 'x-instance', '', __nscVueInterpolate(["data-id=\"", "\""], [prev.id])), statusCN[prev.state], prev.finished]) : '', t.state === 'FAILED' ? 'fail' : 'ok', t.kind === 'task' ? ' Task' : '容器', esc(t.id), t.started, t.finished || '仍在运行', next ? __nscVueView(templates[312], [TB(esc(next.id), 'x-instance', '', __nscVueInterpolate(["data-id=\"", "\""], [next.id])), statusCN[next.state], next.started]) : '', t.logs ? __nscVueView(templates[313], [esc(t.logs)]) : __nscVueView(templates[314], [])]),
        footer: __nscVueAdd(B('关闭', 'close'), B('查看服务运行记录', 'x-service-events', 'history', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [t.service])))
      });
    }
    function clusterSummary(r) {
      const managers = r.nodes.filter(n => n.role === 'manager'),
        reachable = managers.filter(n => ['Leader', 'Reachable'].includes(n.manager)).length;
      const ns = r.nodes.filter(n => n.status === 'Ready').length;
      return {
        managers,
        reachable,
        total: r.nodes.length,
        ready: ns,
        services: db.services.filter(s => s.runtimeId === r.id),
        apps: db.apps.filter(a => a.runtime === r.id),
        known: r.connection === 'online',
        quorum: r.type !== 'swarm' || reachable >= __nscVueAdd(Math.floor(managers.length / 2), 1)
      };
    }
    function runtimesPage(...args) {
      return __applicationsViews.runtimesPage_58(__renderContext1, ...args);
    }
    function runtimePage(...args) {
      return __applicationsViews.runtimePage_59(__renderContext1, ...args);
    }
    function eventLabel(...args) {
      return __applicationsViews.eventLabel_60(__renderContext1, ...args);
    }
    function eventsTable(...args) {
      return __applicationsViews.eventsTable_61(__renderContext1, ...args);
    }
    function runtimeEventsPage(...args) {
      return __applicationsViews.runtimeEventsPage_62(__renderContext1, ...args);
    }
    function eventDetail(id) {
      const e = db.runtimeEvents.find(i => i.id === id);
      if (!e) return;
      const old = IS(e.task),
        next = IS(e.replacement);
      showModal({
        kind: 'x-event',
        size: 'xwide',
        title: e.title,
        sub: __nscVueAdd(__nscVueAdd(e.id, ' · '), scopeName(e.project, e.env)),
        body: __nscVueView(templates[335], [esc(e.evidence), e.confidence === 'confirmed' ? '已确认（演示）' : e.confidence === 'inferred' ? '推测，不能当作确定根因' : '已观察，未补猜缺失信息', old || next ? __nscVueView(templates[333], [old ? __nscVueView(templates[331], [old.state === 'FAILED' ? 'fail' : '', statusCN[old.state], TB(esc(old.id), 'x-instance', '', __nscVueInterpolate(["data-id=\"", "\""], [old.id])), esc(NODE(old.node)?.name), old.exitCode]) : '', next ? __nscVueView(templates[332], [statusCN[next.state], TB(esc(next.id), 'x-instance', '', __nscVueInterpolate(["data-id=\"", "\""], [next.id])), esc(NODE(next.node)?.name), next.started]) : '']) : '', e.time.slice(11), e.category === 'fault' ? 'red' : 'amber', e.category === 'gap' ? '发现采集中断' : '观察到执行状态变化', esc(e.evidence), next ? __nscVueView(templates[334], [(e.scheduleTime || next?.started || e.time).slice(11), e.end?.slice(11) || '']) : '', eventLabel(e), esc(e.actor), esc(e.service || '不适用'), e.release ? TB(esc(e.release), 'x-deployment', '', __nscVueInterpolate(["data-id=\"", "\""], [e.release])) : '无人工部署关联；不强行推断', esc(e.detail)]),
        footer: __nscVueAdd(B('关闭', 'close'), e.service ? B('进入服务', 'x-service', 'right', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [e.service])) : B('查看采集范围', 'x-runtime-events', 'right', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [e.runtime])))
      });
    }
    function deployState(...args) {
      return __applicationsViews.deployState_63(__renderContext1, ...args);
    }
    function deployTable(...args) {
      return __applicationsViews.deployTable_64(__renderContext1, ...args);
    }
    function historyPage(...args) {
      return __shellViews.historyPage_65(__renderContext1, ...args);
    }
    const SAMPLE_COMPOSE = `services:
  web:
    image: demo/web:1.0.0
    expose:
      - "3000"
  api:
    image: demo/api:1.0.0
    expose:
      - "8080"
`;
    function createWizard(source = 'template', template = 'portal') {
      const t = tpl(template);
      return {
        id: uid('draft'),
        step: 1,
        source,
        template,
        name: t.name,
        key: t.key,
        project: db.project,
        env: db.env,
        runtime: '',
        singleMode: false,
        raw: SAMPLE_COMPOSE,
        image: 'demo/web:1.0.0',
        port: 3000,
        replicas: 1,
        memory: 512,
        secretRef: '',
        storageAck: false,
        access: 'internal',
        domain: '',
        routeService: 0,
        gateway: '',
        reviewAck: false,
        warningAck: false,
        touched: false
      };
    }
    function beginInstall(source = 'template', template = 'portal') {
      ensureNavigate(() => {
        x.wizard = createWizard(source, template);
        const compatible = db.runtimes.filter(r => r.allowedEnvs.includes(db.env));
        x.wizard.runtime = compatible[0]?.id || '';
        x.wizardError = '';
        renderWizard();
      });
    }
    function wizardMode(w) {
      const r = RT(w.runtime);
      if (w.source === 'image') return r?.type === 'swarm' ? 'swarmservice' : 'container';
      return r?.type === 'swarm' ? 'stack' : w.singleMode ? 'container' : 'compose';
    }
    /* Only a deliberately restricted subset is summarized. The full text is preserved.
       The prototype rejects unrecognized syntax; it is NOT a YAML/Compose validator. */
    function summarizeCompose(raw) {
      if (NSCWorkspace?.importCompose) return NSCWorkspace.importCompose(raw, x.wizard);
      const errors = [];
      if (raw.length > 32000) return {
        services: [],
        errors: ['演示文件上限为 32 KB；真实实现需要有大小和解析资源限制。']
      };
      if (!/^services:\s*(?:#.*)?$/m.test(raw)) errors.push('需要顶层 services:。本原型仅演示常规缩进的简单 Compose 文件。');
      if (/\t/.test(raw)) errors.push('本原型不接受 Tab 缩进，请使用空格。');
      if (/[&*][A-Za-z_-]|!include|^include:|^extends:/m.test(raw)) errors.push('本原型未实现 YAML 锚点 / include / extends 解析；不能把文件当作已完整校验。');
      if (/(?:PASSWORD|TOKEN|SECRET|API_KEY)\s*[:=]\s*[^\s$#]/i.test(raw)) errors.push('请勿在离线原型中填写真实密码或 Token。使用目标环境凭据引用。');
      const lines = raw.split('\n');
      let inServices = false,
        current = null;
      const services = [];
      for (const line of lines) {
        if (/^services:\s*(?:#.*)?$/.test(line)) {
          inServices = true;
          continue;
        }
        if (inServices && /^\S/.test(line) && !line.startsWith('#')) inServices = false;
        if (!inServices) continue;
        const sm = line.match(/^  ([A-Za-z0-9_-]+):\s*(?:#.*)?$/);
        if (sm) {
          if (services.some(s => s.name === sm[1])) errors.push(__nscVueAdd('服务名称重复：', sm[1]));
          current = {
            name: sm[1],
            image: '',
            port: 0,
            http: false
          };
          services.push(current);
          continue;
        }
        if (current) {
          const im = line.match(/^    image:\s*["']?([^"'#]+?)["']?\s*(?:#.*)?$/);
          if (im) current.image = im[1].trim();
          const pt = line.match(/^      -\s*["']?(\d+)(?:\/tcp)?["']?\s*$/);
          if (pt && current.port === 0) {
            current.port = Number(pt[1]);
            current.http = [80, 443, 3000, 8000, 8080, 8081, 9000].includes(current.port);
          }
        }
      }
      for (const [i, line] of lines.entries()) {
        if (!line.trim() || line.trim().startsWith('#')) continue;
        if (!/^(services:\s*(?:#.*)?|  [A-Za-z0-9_-]+:\s*(?:#.*)?|    image:\s*.+|    expose:\s*(?:#.*)?|      -\s*[\"']?\d+(?:\/tcp)?[\"']?\s*)$/.test(line)) errors.push(__nscVueAdd(__nscVueAdd('第 ', __nscVueAdd(i, 1)), ' 行超出演示导入支持的字段，未被静默忽略。'));
      }
      if (!services.length) errors.push('没有识别到服务。请使用示例结构或保留为待人工检查的文件。');
      if (services.length > 12) errors.push('演示流程最多 12 个服务，不是产品容量限制。');
      for (const s of services) if (!s.image) errors.push(__nscVueAdd(s.name, ' 缺少可识别的 image；原型不执行 build。'));
      return {
        services,
        errors
      };
    }
    function wizardServices(w) {
      if (w.source === 'template') return clone(tpl(w.template).services);
      if (w.source === 'image') return [{
        name: 'main',
        image: w.image,
        port: Number(w.port) || 0,
        http: Number(w.port) > 0
      }];
      return NSCWorkspace?.importCompose ? NSCWorkspace.importCompose(w.raw, w).services : summarizeCompose(w.raw).services;
    }
    function collectWizard() {
      const w = x.wizard;
      if (!w) return;
      document.querySelectorAll('[data-wf]').forEach(el => {
        const k = el.dataset.wf;
        if (el.type === 'checkbox') w[k] = el.checked;else if (['port', 'replicas', 'memory', 'routeService'].includes(k)) w[k] = Number(el.value);else w[k] = el.value;
      });
    }
    function chooseCompatibleRuntime(w) {
      const rs = db.runtimes.filter(r => r.allowedEnvs.includes(w.env));
      if (!rs.some(r => r.id === w.runtime)) w.runtime = rs[0]?.id || '';
      w.gateway = '';
      w.reviewAck = false;
      w.warningAck = false;
    }
    function wizardIssues(w) {
      const errors = [],
        warnings = [];
      const mode = wizardMode(w),
        r = RT(w.runtime),
        ss = wizardServices(w),
        t = tpl(w.template);
      if (!w.name.trim() || w.name.length > 64) errors.push('应用名称不能为空，最多 64 个字符。');
      if (!/^[a-z][a-z0-9-]{1,47}$/.test(w.key)) errors.push('应用标识需为 2–48 位小写英文、数字或连字符，并以字母开头。');
      if (db.apps.some(a => a.project === w.project && a.env === w.env && a.key === w.key)) errors.push('当前环境已有相同应用标识，请使用其他标识；不覆盖已有应用。');
      if (!P(w.project) || E(w.env)?.project !== w.project) errors.push('项目与环境不匹配。');
      if (!r?.allowedEnvs.includes(w.env)) errors.push('目标运行位置未开放给当前环境。');
      const gate = runtimeGate(r);
      if (gate) errors.push(gate);
      if (w.source === 'template' && !t.modes.includes(mode)) errors.push(__nscVueAdd(__nscVueAdd('此模板未提供 ', MODE[mode]), ' 兼容版本，请换目标或模板。'));
      if (w.source === 'image' && !/^[a-zA-Z0-9][a-zA-Z0-9_.\/:@-]+$/.test(w.image)) errors.push('请填写合法形式的镜像引用，不含空格或命令。');
      if (w.source === 'file') {
        const compiled = NSCWorkspace.importCompose(w.raw, w);
        errors.push(...compiled.errors);
        warnings.push(...compiled.result.warnings);
        if (/docker\.sock/.test(w.raw)) errors.push('离线原型不接受 Docker Socket 访问。');
      }
      if (!Number.isInteger(w.memory) || w.memory < 64 || w.memory > 65536) errors.push('内存限制需为 64–65536 MiB 的整数。');
      if (!Number.isInteger(w.replicas) || w.replicas < 1 || w.replicas > 20) errors.push('实例数需为 1–20 的整数（原型交互上限）。');
      if (w.source === 'image' && (!Number.isInteger(w.port) || w.port < 0 || w.port > 65535)) errors.push('内部服务端口需为 0–65535 的整数；0 表示不提供 HTTP。');
      if (mode === 'container' && ss.length > 1) errors.push('单容器方式只支持一个服务；多服务请使用 Compose。');
      if (mode === 'container' && w.replicas !== 1) errors.push('单容器部署的实例数必须为 1。');
      if (w.source === 'template' && t.secret && !w.secretRef) errors.push('请选择当前环境的凭据引用；不要把实际密码写进模板。');
      if (w.source === 'template' && t.stateful && !w.storageAck) errors.push('请确认数据存储位置与保留规则。');
      if (w.source === 'template' && t.stateful && w.replicas !== 1) errors.push('本模板是单实例有状态应用，不能直接复制数据服务获得高可用。');
      if (w.access === 'domain') {
        if (!ss[w.routeService]?.http) errors.push('只能为 HTTP 服务创建此域名入口；数据库不能使用 HTTP Route 接入。');
        if (!validHost(w.domain.toLowerCase().trim())) errors.push('请输入不含协议、路径、端口的合法域名。');
        if (!w.gateway || !E(w.env)?.gateways.includes(w.gateway)) errors.push('请选择目标环境可用的 Gateway。');
        const conflict = domainConflict(w.domain.toLowerCase().trim(), [w.gateway]);
        if (conflict) errors.push(__nscVueAdd(__nscVueAdd('该入口已被 ', scopeText(conflict)), ' 占用。'));
        warnings.push('这里只创建 HTTP 域名草稿，不签发证书、不修改 DNS，也不发布 Nginx。Gateway 到服务的可达性仍需真实验证。');
      }
      if (ss.some(s => !s.image.includes('@sha256:'))) warnings.push('镜像标签示例未解析为真实 digest；实际部署前需冻结可审计的镜像版本。');
      if (mode === 'compose' || mode === 'container') warnings.push('运行在一台主机上。主机离线时，不承诺 Swarm 式跨节点自动恢复。');
      if (w.source === 'template' && t.stateful) warnings.push('应用卸载默认保留数据卷；配置回退不等于数据库数据回退。');
      return {
        errors: [...new Set(errors)],
        warnings: [...new Set(warnings)]
      };
    }
    function renderWizard() {
      const w = x.wizard;
      if (!w) return;
      const ss = wizardServices(w),
        r = RT(w.runtime),
        mode = wizardMode(w);
      const titles = ['选择应用', '部署到哪里', '参数与访问', '检查并部署'];
      let inner = '';
      if (w.step === 1) inner = __nscVueView(templates[344], [__nscVueJoin([['template', '应用市场模板', '选择应用，填写参数即可', 'grid'], ['file', '导入编排文件', '保留文件，再检查部署兼容性', 'file'], ['image', '从镜像创建', '只需要一个镜像的简单应用', 'box']].map(([k, n, d, i]) => __nscVueView(templates[339], [w.source === k ? 'active' : '', k, I(i), n, d])), ''), esc(w.name), esc(w.key), w.source === 'template' ? __nscVueView(templates[341], [__nscVueJoin(TEMPLATES.map(t => __nscVueView(templates[340], [t.id, t.id === w.template ? 'selected' : '', t.name, t.services.length])), ''), esc(tpl(w.template).notes)]) : w.source === 'file' ? __nscVueView(templates[342], [B('载入示例', 'x-wsample', 'file', 'sm'), esc(w.raw)]) : __nscVueView(templates[343], [esc(w.image), w.port])]);
      if (w.step === 2) inner = __nscVueView(templates[351], [__nscVueJoin(db.projects.map(p => __nscVueView(templates[345], [p.id, p.id === w.project ? 'selected' : '', esc(p.name)])), ''), __nscVueJoin(projectEnvs(w.project).map(e => __nscVueView(templates[346], [e.id, e.id === w.env ? 'selected' : '', esc(e.name)])), ''), __nscVueJoin(db.runtimes.filter(r => r.allowedEnvs.includes(w.env)).map(r => __nscVueView(templates[347], [r.id, r.id === w.runtime ? 'selected' : '', esc(r.name), r.type === 'swarm' ? '集群' : '单机', runtimeGate(r) ? ' · 当前不可部署' : ''])), ''), I(r?.type === 'swarm' ? 'layers' : 'server'), MODE[mode], mode === 'stack' ? '由集群调度多个服务。需要检查 Stack 支持的文件范围。' : mode === 'swarmservice' ? '创建一个 Swarm Service，默认 1 个副本；不是绑死在某台节点上的独立容器。' : mode === 'container' ? '在选定 Docker 主机创建一个容器，不承诺跨节点恢复。' : '所有服务运行在这台主机上，不把 Compose 标成跨节点调度。', w.source === 'template' && r?.type === 'docker' && ss.length === 1 && tpl(w.template).modes.includes('container') ? __nscVueView(templates[348], [w.singleMode ? 'checked' : '']) : '', r ? __nscVueView(templates[350], [runtimeGate(r) || '连接与部署授权在演示中可用。生产执行仍需实时预检。', r.type === 'swarm' && w.source === 'template' && !tpl(w.template).modes.includes(mode) ? __nscVueView(templates[349], []) : '']) : '']);
      if (w.step === 3) inner = __nscVueView(templates[358], [ss.length, __nscVueJoin(ss.map(s => __nscVueView(templates[352], [esc(s.name), esc(s.image), s.port || '无 HTTP 入口'])), ''), w.replicas, mode === 'container' || w.source === 'file' ? 'disabled' : '', w.source === 'file' ? '文件部署使用 YAML 渲染出的逐服务参数，此项不覆盖文件。' : '单容器及演示有状态模板固定为 1。', w.memory, w.source === 'file' ? 'disabled' : '', w.source === 'template' && tpl(w.template).secret ? __nscVueView(templates[353], [w.env, w.secretRef ? 'selected' : '', esc(envName(w.env))]) : '', w.source === 'template' && tpl(w.template).stateful ? __nscVueView(templates[354], [w.storageAck ? 'checked' : '']) : '', w.access === 'internal' ? 'selected' : '', w.access === 'domain' ? 'selected' : '', !ss.some(s => s.http) ? 'disabled' : '', w.access === 'domain' ? __nscVueView(templates[357], [esc(w.domain), __nscVueJoin(ss.map((s, i) => s.http ? __nscVueView(templates[355], [i, i === w.routeService ? 'selected' : '', esc(s.name), s.port]) : ''), ''), __nscVueJoin((E(w.env)?.gateways || []).map(id => __nscVueView(templates[356], [id, id === w.gateway ? 'selected' : '', esc(G(id)?.name)])), ''), callout('只创建 HTTP 入口草稿。DNS、TLS、网络可达性和 Nginx 发布另行确认；不会直接把应用暴露到公网。', 'blue')]) : '']);
      if (w.step === 4) {
        const q = wizardIssues(w);
        if (w.source === 'file' && !w.reviewAck) w.parameterReview = NSCWorkspace.installVersion(w);
        inner = __nscVueView(templates[362], [esc(scopeName(w.project, w.env)), esc(w.name), badge(E(w.env)?.kind === 'production' ? '生产环境 · 请核对' : '仅此环境', E(w.env)?.kind === 'production' ? 'amber' : 'blue'), I('checkcircle'), MODE[mode], esc(r?.name), ss.length, w.source === 'file' ? '逐服务实例数和资源以解析后的 YAML 为准' : __nscVueAdd(__nscVueAdd(__nscVueAdd(mode === 'container' ? 1 : w.replicas, ' 个实例 / 服务 · '), w.memory), ' MiB / 实例'), I('checkcircle'), w.access === 'internal' ? '仅内部运行' : __nscVueAdd(__nscVueAdd('创建 ', esc(w.domain)), ' 的入口草稿'), __nscVueJoin(q.errors.map(m => __nscVueView(templates[359], [I('warning'), esc(m)])), ''), __nscVueJoin(q.warnings.map(m => __nscVueView(templates[360], [I('info'), esc(m)])), ''), w.source === 'file' ? NSCWorkspace.installReviewHTML(w) : '', esc(w.source === 'file' ? w.raw : previewWizard(w)), w.reviewAck ? 'checked' : '', q.warnings.length ? __nscVueView(templates[361], [w.warningAck ? 'checked' : '']) : '']);
      }
      const issues = w.step === 4 ? wizardIssues(w) : null;
      showModal({
        kind: 'x-wizard',
        size: 'xwide',
        title: '部署应用',
        sub: '不需要先创建域名；部署目标在确认前可以调整。',
        body: __nscVueView(templates[365], [__nscVueJoin(titles.map((n, i) => __nscVueView(templates[363], [w.step === __nscVueAdd(i, 1) ? 'active' : w.step > __nscVueAdd(i, 1) ? 'done' : '', w.step > __nscVueAdd(i, 1) ? '✓' : __nscVueAdd(i, 1), n])), ''), inner, x.wizardError ? __nscVueView(templates[364], [callout(esc(x.wizardError), 'red')]) : '', esc(w.name), esc(P(w.project)?.name), esc(envName(w.env)), esc(r?.name || '待选择'), MODE[mode], ss.length]),
        footer: __nscVueAdd(__nscVueAdd(__nscVueAdd(B('取消', 'close', '', 'ghost'), B('保存部署草稿', 'x-wsave', 'save')), w.step > 1 ? B('上一步', 'x-wback', 'back') : ''), B(w.step === 4 ? '确认并模拟部署' : '下一步', w.step === 4 ? 'x-wdeploy' : 'x-wnext', w.step === 4 ? 'send' : 'right', 'primary', __nscVueInterpolate(["id=\"x-w-primary\" ", ""], [w.step === 4 && (issues.errors.length || !w.reviewAck || issues.warnings.length && !w.warningAck) ? 'disabled' : ''])))
      });
    }
    function previewWizard(w) {
      return __nscVueAdd(__nscVueAdd(__nscVueInterpolate(["# ", " \xB7 ", "\n# ", " \u2192 ", "\nservices:\n"], [w.name, scopeName(w.project, w.env), MODE[wizardMode(w)], RT(w.runtime)?.name]), __nscVueJoin(wizardServices(w).map(s => __nscVueInterpolate(["  ", ":\n    image: ", "\n", "    # replicas: ", "; memory: ", " MiB\n"], [s.name, s.image, s.port ? __nscVueAdd(__nscVueAdd('    expose: ["', s.port), '"]\n') : '', w.replicas, w.memory])), '')), w.secretRef ? __nscVueAdd(__nscVueAdd('# secret reference: ', w.secretRef), '\n') : '');
    }
    function nextWizard() {
      collectWizard();
      const w = x.wizard;
      if (w.step === 1) {
        if (!w.name.trim() || !/^[a-z][a-z0-9-]{1,47}$/.test(w.key)) {
          x.wizardError = '请填写应用名称和有效标识。';
          return renderWizard();
        }
        if (w.source === 'file') {
          const q = NSCWorkspace.checkImportStructure(w.raw);
          if (q.errors.length) {
            x.wizardError = __nscVueJoin(q.errors, '；');
            return renderWizard();
          }
        }
      }
      if (w.step === 2) {
        if (!RT(w.runtime) || !RT(w.runtime).allowedEnvs.includes(w.env)) {
          x.wizardError = '请选择当前环境可用的部署位置。';
          return renderWizard();
        }
        const gate = runtimeGate(RT(w.runtime));
        if (gate) {
          x.wizardError = gate;
          return renderWizard();
        }
        if (w.source === 'template' && !tpl(w.template).modes.includes(wizardMode(w))) {
          x.wizardError = '当前模板不支持此部署方式。请换目标或模板。';
          return renderWizard();
        }
      }
      if (wizardMode(w) === 'container') w.replicas = 1;
      w.step = Math.min(4, __nscVueAdd(w.step, 1));
      w.reviewAck = false;
      w.warningAck = false;
      x.wizardError = '';
      renderWizard();
    }
    function saveWizard() {
      collectWizard();
      if (!x.wizard) return;
      if (/(?:PASSWORD|TOKEN|SECRET|API_KEY)\s*[:=]\s*[^\s$#]/i.test(x.wizard.raw) && x.wizard.source === 'file') {
        x.wizardError = '不要保存可能包含明文凭据的文件到离线浏览器原型。';
        return renderWizard();
      }
      const d = clone(x.wizard);
      d.savedAt = xTime();
      db.appDrafts = db.appDrafts.filter(i => i.id !== d.id);
      db.appDrafts.unshift(d);
      persist();
      x.wizard = null;
      OLD.closeModal(true);
      render();
      toast('部署草稿已保存在当前浏览器；没有提交任何部署。');
    }
    function specText(...args) {
      return __applicationsViews.specText_66(__renderContext1, ...args);
    }
    function writeGate(runtime, app = null) {
      if (ui.externalConflict) {
        toast('其他窗口已修改工作区。请先刷新并核对，当前操作未提交。', true);
        return false;
      }
      if (deploymentTimer) {
        toast('有部署正在执行，请等待结果。', true);
        return false;
      }
      const reason = runtimeGate(RT(runtime));
      if (reason) {
        toast(reason, true);
        return false;
      }
      if (app && !app.managed) {
        toast('此应用由外部工具管理，当前只读。', true);
        return false;
      }
      return true;
    }
    function newInstance(s, slot, release, previous = null, cause = '发布更新') {
      const r = RT(s.runtimeId),
        nodes = r.nodes.filter(n => n.status === 'Ready' && n.availability === 'Active'),
        n = nodes.filter(n => n.role !== 'manager')[(slot - 1) % Math.max(1, nodes.filter(n => n.role !== 'manager').length)] || nodes[0];
      if (!n) return null;
      const id = uid(r.type === 'swarm' ? 'task' : 'container');
      return {
        id,
        service: s.id,
        app: s.app,
        runtime: s.runtimeId,
        node: n.id,
        slot,
        kind: r.type === 'swarm' ? 'task' : 'container',
        containerId: r.type === 'swarm' ? uid('ctr') : id,
        state: 'RUNNING',
        desiredState: 'RUNNING',
        current: true,
        started: xTime(),
        finished: null,
        exitCode: null,
        oomKilled: null,
        health: 'healthy',
        cause,
        causeType: 'planned',
        restartCount: 0,
        release,
        previous,
        logs: '[DEMO] Created from reviewed application definition\n[DEMO] Startup and health check passed'
      };
    }
    function appendEvent(s, category, title, detail, extra = {}) {
      const ev = {
        id: uid('EV'),
        time: xTime(),
        runtime: s.runtimeId,
        project: s.project,
        env: s.env,
        app: s.app,
        service: s.id,
        node: extra.node || currentInstances(s)[0]?.node || null,
        category,
        title,
        detail,
        state: 'recorded',
        actor: 'admin · 本地演示',
        confidence: 'observed',
        evidence: '原型模拟状态变更；不是真实 Docker 事件',
        ...extra
      };
      db.runtimeEvents.unshift(ev);
      return ev;
    }
    function applyServiceSnapshot(s, desired, op) {
      const before = currentInstances(s),
        oldDef = clone(s),
        isRestart = op.kind === 'restart',
        isScale = op.kind === 'scale';
      s.image = desired.image;
      s.desired = desired.replicas;
      s.memory = desired.memory;
      s.port = desired.port;
      s.observedVersion = op.version;
      s.workloadKind = desired.kind || 'replicated';
      if (s.workloadKind === 'job') {
        s.observedVersion = op.version;
        return;
      }
      if (isRestart && RT(s.runtimeId).type === 'docker') {
        for (const t of before.filter(t => t.state === 'RUNNING')) {
          t.restartCount++;
          t.started = xTime();
          t.logs = __nscVueAdd(t.logs, '\n[DEMO] Same container restarted by reviewed action');
          t.cause = '人工重启容器';
          t.release = op.id;
        }
        appendEvent(s, 'restart', '同一容器已重启', __nscVueInterpolate(["Container ID \u4FDD\u6301\u4E0D\u53D8\uFF1B\u91CD\u542F\u8BA1\u6570 +1\u3002\u5173\u8054 ", "\u3002"], [op.id]), {
          release: op.id,
          task: before[0]?.id
        });
        return;
      }
      const kept = [];
      for (const t of before) {
        if (isScale && t.state === 'RUNNING' && t.slot <= desired.replicas) {
          kept.push(t);
          continue;
        }
        t.current = false;
        t.state = 'SHUTDOWN';
        t.desiredState = 'SHUTDOWN';
        t.finished = xTime();
        t.exitCode = 143;
        t.oomKilled = false;
        t.health = 'not-applicable';
        t.cause = '计划内停止';
        t.causeType = 'planned';
        t.release = op.id;
      }
      for (let i = 1; i <= desired.replicas; i++) {
        if (kept.some(t => t.slot === i)) continue;
        const prev = before.find(t => t.slot === i && !t.current);
        const t = newInstance(s, i, op.id, prev?.id || null, isRestart ? '人工重新创建实例' : isScale ? '调整实例数量' : '发布更新');
        if (t) {
          if (prev) prev.replacement = t.id;
          db.instances.push(t);
        }
      }
      s.endpoints = currentInstances(s).filter(t => t.state === 'RUNNING').map((t, i) => [__nscVueAdd(__nscVueAdd(__nscVueAdd('10.90.', db.services.indexOf(s) % 240), '.'), __nscVueAdd(10, i)), true]);
      appendEvent(s, 'planned', isRestart ? '按计划重新创建实例' : isScale ? '实例数量已调整' : '服务部署已核实', __nscVueInterpolate(["", " \u2192 ", " \u4E2A\u5B9E\u4F8B\uFF1B\u539F\u5B9E\u4F8B\u4E0E\u66FF\u6362\u5173\u7CFB\u5DF2\u4FDD\u7559\u3002"], [oldDef.desired ?? 0, s.desired]), {
        release: op.id,
        task: before.find(t => !t.current)?.id || null,
        replacement: db.instances.find(t => t.release === op.id && t.current && t.service === s.id)?.id || null
      });
    }
    function deployWizard() {
      collectWizard();
      const w = x.wizard;
      if (!w) return;
      const q = wizardIssues(w);
      if (q.errors.length || !w.reviewAck || q.warnings.length && !w.warningAck) {
        x.wizardError = __nscVueJoin(q.errors, '；') || '请先完成最后一步的确认。';
        return renderWizard();
      }
      if (w.source === 'file' && JSON.stringify(w.parameterReview) !== JSON.stringify(NSCWorkspace.installVersion(w))) {
        x.wizardError = '目标环境变量版本发生变化，请重新核对确认页。';
        w.reviewAck = false;
        return renderWizard();
      }
      if (!writeGate(w.runtime)) return;
      const fixed = clone(w);
      fixed.domain = fixed.domain.toLowerCase().trim();
      const id = uid('app'),
        a = {
          id,
          name: fixed.name.trim(),
          key: fixed.key,
          project: fixed.project,
          env: fixed.env,
          runtime: fixed.runtime,
          mode: wizardMode(fixed),
          managed: true,
          version: 1,
          source: fixed.source === 'template' ? __nscVueAdd('应用市场 · ', tpl(fixed.template).name) : fixed.source === 'file' ? '用户导入的编排文件' : '从镜像创建',
          raw: fixed.source === 'file' ? fixed.raw : '',
          stateful: fixed.source === 'template' && tpl(fixed.template).stateful,
          secretRef: fixed.secretRef || null,
          created: xTime(),
          revisions: [],
          draft: null,
          access: fixed.access === 'domain' ? {
            domain: fixed.domain,
            gateway: fixed.gateway,
            serviceIndex: fixed.routeService,
            created: false
          } : null
        };
      db.apps.push(a);
      const ids = [];
      for (const part of wizardServices(fixed)) {
        const s = {
          id: uid(__nscVueAdd(__nscVueAdd(fixed.key, '-'), part.name)),
          name: part.name,
          app: id,
          project: a.project,
          env: a.env,
          runtimeId: a.runtime,
          runtime: RT(a.runtime).name,
          mode: a.mode,
          managed: true,
          workloadKind: 'replicated',
          desired: fixed.replicas,
          port: part.port,
          http: part.http,
          image: part.image,
          memory: fixed.memory,
          endpoints: [],
          specVersion: 1,
          observedVersion: 0,
          health: 'healthy',
          stateful: a.stateful
        };
        db.services.push(s);
        ids.push(s.id);
      }
      if (fixed.source === 'file') NSCWorkspace.initializeImportedApp(a, fixed);
      const spec = appSpec(a);
      if (fixed.source === 'file') spec.composeBundle = clone(db.parameters13.documents[a.id].lastBundle);
      a.revisions.push({
        v: 1,
        time: xTime(),
        note: '首次部署',
        spec: clone(spec)
      });
      db.appDrafts = db.appDrafts.filter(d => d.id !== fixed.id);
      x.wizard = null;
      OLD.closeModal(true);
      selectScope(a.project, a.env);
      x.app = a.id;
      ui.page = 'application';
      x.appTab = 'overview';
      updateHash();
      createOperation(a, '部署应用', 'install', spec, ids, 0);
    }
    function maybeCreateAccessDraft(a) {
      if (!a.access || a.access.created) return;
      const acc = a.access,
        s = appServices(a)[acc.serviceIndex],
        gateway = G(acc.gateway);
      if (!s || !gateway) return;
      if (domainConflict(acc.domain, [acc.gateway], null)) {
        acc.error = '应用已部署，但域名发生占用冲突；入口草稿未创建。请在域名页重新检查。';
        return;
      }
      const c = emptyConfig();
      c.routes = [route(uid('route'), '/', s.id)];
      const d = {
        id: uid('domain'),
        name: acc.domain,
        project: a.project,
        env: a.env,
        gateways: [acc.gateway],
        rev: 0,
        active: emptyConfig(),
        loaded: {},
        draft: {
          base: 0,
          config: c,
          savedAt: xTime()
        },
        drift: null,
        created: xTime(),
        certExpiry: '',
        certReadyDemo: false,
        revisions: [],
        fileBackups: []
      };
      db.domains.push(d);
      acc.created = true;
      acc.domainId = d.id;
    }
    function createOperation(a, action, kind, spec, targets, previous) {
      const op = {
        id: uid('APP-DEP'),
        app: a.id,
        project: a.project,
        env: a.env,
        runtime: a.runtime,
        version: a.version,
        previous,
        time: xTime(),
        actor: 'admin · 本地演示',
        action,
        kind,
        state: 'running',
        snapshot: clone(spec),
        targets: [...targets],
        results: Object.fromEntries(targets.map(id => [id, 'pending'])),
        attempts: [],
        stages: 0,
        note: '内容和目标在提交时冻结；这是模拟执行。'
      };
      db.appDeployments.unshift(op);
      runAttempt(op, [...targets]);
    }
    function runAttempt(op, targets) {
      const a = APP(op.app);
      if (!a) return;
      const attempt = {
        number: __nscVueAdd(op.attempts.length, 1),
        time: xTime(),
        targets: [...targets],
        logs: [__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('[DEMO] 已固定 ', scopeName(op.project, op.env)), ' / '), a.name), ' / r'), op.version), __nscVueAdd('[DEMO] 本次目标服务: ', __nscVueJoin(targets, ', '))],
        state: 'running'
      };
      op.attempts.push(attempt);
      op.state = 'running';
      op.stages = 0;
      const scenario = x.scenario;
      x.scenario = 'success';
      persist();
      render();
      showDeployment(op.id);
      let phase = 0;
      deploymentTimer = setInterval(() => {
        phase++;
        op.stages = phase;
        attempt.logs.push(['', '[DEMO] 检查目标权限、作用域和内容版本', '[DEMO] 逐项提交固定内容', '[DEMO] 等待运行实例与健康证据', '[DEMO] 汇总执行结果'][phase] || '');
        if (phase >= 4) {
          clearInterval(deploymentTimer);
          deploymentTimer = null;
          const changedTarget = targets[targets.length - 1];
          for (const sid of targets) {
            if (scenario === 'unknown' && sid === changedTarget) {
              op.results[sid] = 'unknown';
              continue;
            }
            if (scenario === 'partial' && sid === changedTarget) {
              op.results[sid] = 'failed';
              continue;
            }
            const s = ST(sid),
              spec = op.snapshot.services.find(t => t.id === sid);
            if (s && spec) {
              applyServiceSnapshot(s, spec, op);
              op.results[sid] = 'success';
            } else op.results[sid] = 'failed';
          }
          const vals = Object.values(op.results);
          op.state = vals.includes('unknown') ? 'unknown' : vals.every(v => v === 'success') ? 'success' : vals.includes('success') ? 'partial' : 'failed';
          attempt.state = op.state;
          attempt.finished = xTime();
          op.finished = xTime();
          attempt.logs.push(__nscVueAdd(__nscVueAdd('[DEMO] ', deployState(op)[0]), scenario === 'partial' ? '；失败服务保留旧版本，未自动删除已成功资源。' : scenario === 'unknown' ? '；未确认的操作不得盲目重试。' : ''));
          if (op.state === 'success') maybeCreateAccessDraft(a);
          persist();
          render();
          showDeployment(op.id);
          return;
        }
        persist();
        showDeployment(op.id);
      }, 420);
    }
    function showDeployment(id) {
      const o = OP(id);
      if (!o) return;
      const a = APP(o.app),
        st = deployState(o);
      x.op = id;
      const latest = a?.version === o.version,
        g = runtimeGate(RT(o.runtime));
      const canRetry = ['partial', 'failed'].includes(o.state) && latest && !g;
      showModal({
        kind: 'x-deployment',
        size: 'xwide',
        title: __nscVueAdd(__nscVueAdd(o.action, ' · '), st[0]),
        sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(o.id, ' · '), scopeName(o.project, o.env)), ' / '), a?.name || o.snapshot.name),
        locked: o.state === 'running',
        body: __nscVueView(templates[372], [o.version, esc(RT(o.runtime)?.name), badge(...st), __nscVueJoin(['固定快照', '目标检查', '逐项执行', '结果核实'].map((t, i) => __nscVueView(templates[366], [o.stages > i ? 'done' : '', __nscVueAdd(i, 1), t])), ''), __nscVueJoin(o.targets.map(sid => __nscVueView(templates[367], [esc(ST(sid)?.name || sid), badge({
          success: '已核实',
          failed: '失败 · 保留原状态',
          unknown: '结果未知',
          pending: '等待执行'
        }[o.results[sid]] || '未知', o.results[sid] === 'success' ? 'green' : o.results[sid] === 'failed' ? 'red' : 'amber'), o.version, ST(sid)?.observedVersion ? __nscVueAdd('r', ST(sid).observedVersion) : '未观察', o.state !== 'running' ? TB('查看实例', 'x-service', 'right', __nscVueInterpolate(["data-id=\"", "\""], [sid])) : ''])), ''), o.state === 'unknown' ? __nscVueView(templates[368], [callout('提交结果不确定。先按同一操作 ID 核实，不能换一个新 ID 再次部署。关闭或刷新页面不会取消远端操作。', 'amber')]) : '', !latest ? __nscVueView(templates[369], [callout('应用已有更新版本。此历史记录不可直接重试；需要基于当前版本审阅恢复方案。', 'amber')]) : '', a?.access ? __nscVueView(templates[370], [callout(a.access.created ? __nscVueAdd(__nscVueAdd('应用部署已完成；', esc(a.access.domain)), ' 仅创建了入口草稿，Nginx 尚未发布。') : a.access.error ? esc(a.access.error) : '域名入口等待应用部署全部核实后创建；不会自动切换流量。', 'blue')]) : '', o.attempts.length, __nscVueJoin(o.attempts.map(at => __nscVueView(templates[371], [at.number === o.attempts.length ? 'open' : '', at.number, at.time, esc(at.state), esc(__nscVueJoin(at.logs, '\n'))])), ''), esc(specText(o.snapshot))]),
        footer: __nscVueAdd(__nscVueAdd(__nscVueAdd(B(o.state === 'running' ? '正在模拟执行' : '关闭', 'close', '', '', o.state === 'running' ? 'disabled' : ''), canRetry ? B('仅重试失败服务', 'x-retry', 'refresh', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [o.id])) : ''), o.state === 'unknown' ? B('核实此操作的结果', 'x-verify', 'search', 'primary', __nscVueInterpolate(["data-id=\"", "\" ", ""], [o.id, g ? 'disabled' : ''])) : ''), o.state !== 'running' ? B('查看应用', 'x-app', 'right', '', __nscVueInterpolate(["data-id=\"", "\""], [o.app])) : '')
      });
    }
    function retryOperation(id) {
      const o = OP(id),
        a = APP(o?.app);
      if (!o || !a || !writeGate(o.runtime, a)) return;
      if (a.version !== o.version || !['partial', 'failed'].includes(o.state)) return toast('只允许重试当前版本中已确认失败的服务。未知结果请先核实。', true);
      const targets = o.targets.filter(t => o.results[t] === 'failed');
      if (!targets.length) return;
      runAttempt(o, targets);
    }
    function verifyOperation(id) {
      const o = OP(id);
      if (!o || o.state !== 'unknown' || runtimeGate(RT(o.runtime))) return;
      showModal({
        kind: 'x-verify',
        size: 'small',
        title: '核实操作结果（模拟）',
        sub: o.id,
        body: callout('本次只模拟查询原操作，不发起新的部署。请选择将要观察到的证据，生产实现必须来自真实运行时。', 'blue'),
        footer: __nscVueAdd(__nscVueAdd(B('仍无法核实', 'close'), B('确认原操作失败', 'x-verify-result', '', '', __nscVueInterpolate(["data-id=\"", "\" data-result=\"failed\""], [id]))), B('观察到已生效', 'x-verify-result', 'checkcircle', 'primary', __nscVueInterpolate(["data-id=\"", "\" data-result=\"success\""], [id])))
      });
    }
    function verifiedResult(id, result) {
      const o = OP(id),
        a = APP(o?.app);
      if (!o || o.state !== 'unknown' || !a) return;
      if (ui.externalConflict || runtimeGate(RT(o.runtime))) return toast('无法核实，连接或工作区状态已变化。', true);
      if (a.version !== o.version) return toast('目标已有更新，不能把历史核实结果覆盖当前运行版本。', true);
      for (const sid of o.targets.filter(t => o.results[t] === 'unknown')) {
        if (result === 'success') applyServiceSnapshot(ST(sid), o.snapshot.services.find(t => t.id === sid), o);
        o.results[sid] = result;
      }
      const vals = Object.values(o.results);
      o.state = vals.every(v => v === 'success') ? 'success' : vals.includes('success') ? 'partial' : 'failed';
      o.attempts[o.attempts.length - 1].logs.push(__nscVueAdd('[DEMO] 只读核实原操作：', result === 'success' ? '观察到目标版本已生效' : '观察到原操作失败，可审阅后定向重试'));
      o.verifications = [...(o.verifications || []), {
        time: xTime(),
        result,
        source: '显式选择的演示证据'
      }];
      if (o.state === 'success') maybeCreateAccessDraft(a);
      persist();
      render();
      showDeployment(id);
    }
    function opForm(sid, kind) {
      const s = ST(sid),
        a = APP(s?.app);
      if (!s || !a || !writeGate(s.runtimeId, a)) return;
      if (s.workloadKind === 'job' || kind === 'scale' && (s.mode === 'container' || s.workloadKind !== 'replicated')) return toast('此类服务不提供该操作。', true);
      x.pendingOp = {
        service: sid,
        kind,
        base: a.version
      };
      const restartLabel = RT(s.runtimeId).type === 'swarm' ? '重新创建实例' : '重启容器';
      showModal({
        kind: 'x-op-form',
        size: 'small',
        title: kind === 'image' ? '修改镜像' : kind === 'scale' ? '调整实例数量' : restartLabel,
        sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(scopeName(s.project, s.env), ' / '), a.name), ' / '), s.name),
        body: __nscVueView(templates[373], [callout(kind === 'restart' ? RT(s.runtimeId).type === 'swarm' ? '会按计划替换当前 Task。旧 Task 和替换关系保留；这不是恢复旧 Task。' : '会重启同一容器，Container ID 不变；重启次数与日志单独记录。' : '将更新所属应用的保存定义，创建新的内容版本与部署记录。不会留下文件不一致的临时修改。', 'blue'), kind === 'image' ? '新镜像（标签或 digest）' : kind === 'scale' ? '目标实例数' : '操作原因', kind === 'scale' ? 'number' : 'text', kind === 'image' ? esc(s.image) : kind === 'scale' ? s.desired : '', kind === 'restart' ? '例如：检查更新后的连接行为' : '', s.stateful ? '有状态服务需要额外数据保护。' : '']),
        footer: __nscVueAdd(B('取消', 'close'), B('确认并模拟执行', 'x-op-confirm', 'send', 'primary'))
      });
    }
    function confirmOp() {
      const p = x.pendingOp,
        s = ST(p?.service),
        a = APP(s?.app);
      if (!p || !s || !a || !writeGate(s.runtimeId, a)) return;
      let err = '';
      const val = document.getElementById('x-op-value').value.trim();
      if (!document.getElementById('x-op-ack').checked) err = '请确认影响范围。';
      if (a.version !== p.base) err = '应用版本已变化，请重新打开并核对。';
      if (p.kind === 'image' && (!val || !/^[A-Za-z0-9][A-Za-z0-9._/:@-]*$/.test(val))) err = '请输入有效镜像引用。';
      if (p.kind === 'scale' && (!Number.isInteger(Number(val)) || Number(val) < 0 || Number(val) > 20)) err = '实例数必须为 0–20 的整数。';
      if (p.kind === 'scale' && s.stateful && Number(val) > 1) err = '本原型不将单实例数据卷直接扩成多副本。';
      if (p.kind === 'restart' && !val) err = '请填写操作原因。';
      if (err) {
        region(document.getElementById('x-op-error'), callout(esc(err), 'red'));
        return;
      }
      const previous = a.version;
      let spec = appSpec(a);
      const target = spec.services.find(t => t.id === s.id);
      if (p.kind === 'image') {
        if (val === s.image) return toast('镜像没有变化；需要重新创建实例请使用相应操作。', true);
        target.image = val;
        s.image = val;
      }
      if (p.kind === 'scale') {
        if (Number(val) === s.desired) return toast('实例数没有变化。', true);
        target.replicas = Number(val);
        s.desired = Number(val);
      }
      if (p.kind !== 'restart') {
        a.version++;
        s.specVersion = a.version;
        a.revisions.push({
          v: a.version,
          time: xTime(),
          note: p.kind === 'image' ? '修改服务镜像' : '调整服务实例数',
          spec: clone(spec)
        });
      }
      const action = p.kind === 'image' ? '修改镜像' : p.kind === 'scale' ? '调整实例数量' : RT(s.runtimeId).type === 'swarm' ? '重新创建实例' : '重启容器';
      OLD.closeModal(true);
      createOperation(a, action, p.kind, spec, [s.id], previous);
    }
    function restoreApp(id, v) {
      const a = APP(id),
        rev = a?.revisions.find(r => r.v === Number(v));
      if (!a || !rev || !writeGate(a.runtime, a)) return;
      x.restore = {
        app: id,
        version: Number(v),
        base: a.version
      };
      showModal({
        kind: 'x-restore',
        size: 'xwide',
        title: '从历史版本创建恢复草稿',
        sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(a.name, ' · r'), a.version), ' → 参考 r'), v),
        body: __nscVueView(templates[374], [callout('这只创建恢复草稿。后续需要核对镜像、服务目标与数据兼容性；不会回滚数据库，也不会直接改变正在运行的应用。', 'amber'), esc(specText(appSpec(a))), esc(specText(rev.spec))]),
        footer: __nscVueAdd(B('取消', 'close'), B('创建恢复草稿', 'x-restore-save', 'edit', 'primary'))
      });
    }
    function saveRestore() {
      const p = x.restore,
        a = APP(p?.app);
      if (!a || a.version !== p.base) return toast('应用已变化，请重新核对。', true);
      a.draft = {
        base: a.version,
        sourceVersion: p.version,
        spec: clone(a.revisions.find(r => r.v === p.version).spec),
        time: xTime()
      };
      persist();
      OLD.closeModal(true);
      jump('application', a.id, 'history');
      toast('恢复草稿已保存，尚未部署。');
    }
    function reviewRestore(id) {
      const a = APP(id);
      if (!a?.draft) return;
      showModal({
        kind: 'x-restore-review',
        size: 'xwide',
        title: '检查恢复草稿',
        sub: __nscVueAdd(__nscVueAdd(scopeName(a.project, a.env), ' / '), a.name),
        body: __nscVueView(templates[375], [callout('确认后生成新的应用版本，不修改旧记录。此演示只支持恢复当前仍存在的服务；增删服务和数据迁移必须另做计划。', 'amber'), esc(specText(a.draft.spec))]),
        footer: __nscVueAdd(B('继续保留草稿', 'close'), B('确认并模拟部署', 'x-restore-deploy', 'send', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [id])))
      });
    }
    function deployRestore(id) {
      const a = APP(id);
      if (!a?.draft || !writeGate(a.runtime, a)) return;
      if (!document.getElementById('x-restore-ack')?.checked) return toast('请确认恢复范围与数据风险。', true);
      const dr = a.draft;
      if (a.version !== dr.base) return toast('草稿基线已过期。请重新基于当前版本审阅。', true);
      if (dr.spec.services.some(s => !ST(s.id)) || dr.spec.services.length !== appServices(a).length) return toast('历史服务集合与当前不同，原型不自动删除或重新创建服务。', true);
      const prev = a.version;
      a.version++;
      const spec = clone(dr.spec);
      for (const d of spec.services) {
        const s = ST(d.id);
        s.specVersion = a.version;
        s.image = d.image;
        s.desired = d.replicas;
        s.memory = d.memory;
      }
      a.revisions.push({
        v: a.version,
        time: xTime(),
        note: __nscVueAdd(__nscVueAdd('从 r', dr.sourceVersion), ' 恢复配置'),
        spec: clone(spec)
      });
      a.draft = null;
      if (spec.composeBundle && db.parameters13) {
        const bundle = spec.composeBundle;
        const doc = db.parameters13.documents[a.id];
        if (doc) {
          doc.source = bundle.source;
          doc.runtime = bundle.runtimeEnvFile;
          doc.draft = null;
          doc.lastBundle = clone(bundle);
          if (NSCWorkspace?.docApp === a.id) NSCWorkspace.loadDoc(a.id);
        }
      }
      OLD.closeModal(true);
      createOperation(a, '恢复应用配置', 'restore', spec, spec.services.map(s => s.id), prev);
    }
    function templateInfo(...args) {
      return __applicationsViews.templateInfo_67(__renderContext1, ...args);
    }
    function runtimeInfo(...args) {
      return __applicationsViews.runtimeInfo_68(__renderContext1, ...args);
    }
    function nodeInfo(id) {
      const n = NODE(id),
        r = db.runtimes.find(r => r.nodes.some(n => n.id === id));
      if (!n || !r) return;
      showModal({
        kind: 'x-node',
        size: 'xwide',
        title: n.name,
        sub: __nscVueAdd(__nscVueAdd(r.name, ' · '), n.id),
        body: __nscVueView(templates[382], [r.connection === 'online' ? n.status : '未知', n.availability, n.role, n.manager || '不适用', r.connection === 'online' && n.agent ? '有演示样本' : '中断或无权限；不显示假 0', callout('节点维护、Drain 和移除属于集群级高风险操作。本轮只展示状态与影响，不提供假成功的变更按钮。', 'blue'), __nscVueJoin(db.instances.filter(t => t.node === id && t.current).map(t => __nscVueView(templates[380], [esc(ST(t.service)?.name || t.service), TB('查看实例', 'x-instance', 'right', __nscVueInterpolate(["data-id=\"", "\""], [t.id]))])), '') || __nscVueView(templates[381], [])]),
        footer: __nscVueAdd(B('关闭', 'close'), B('查看节点事件', 'x-runtime-events', 'history', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [r.id])))
      });
    }
    function connectForm() {
      showModal({
        kind: 'x-connect',
        size: 'small',
        title: '连接已有集群或主机',
        sub: '先观察，再决定是否授权部署',
        body: __nscVueView(templates[384], [callout('这只添加本地演示连接，不会安装 Docker、初始化 Swarm 或保存实际凭据。', 'blue'), __nscVueJoin(db.environments.map(e => __nscVueView(templates[383], [e.id, !allEnvironmentView() && e.id === db.env ? 'selected' : '', esc(scopeName(e.project, e.id))])), '')]),
        footer: __nscVueAdd(B('取消', 'close'), B('添加演示连接', 'x-connect-save', 'plus', 'primary'))
      });
    }
    function saveConnection() {
      if (ui.externalConflict) return toast('请先处理工作区版本冲突。', true);
      const name = document.getElementById('x-connect-name').value.trim(),
        type = document.getElementById('x-connect-type').value,
        env = document.getElementById('x-connect-env').value;
      if (!name || name.length > 64) return toast('请输入 1–64 字符的连接名称。', true);
      const id = uid('runtime');
      db.runtimes.push({
        id,
        name,
        type,
        engineId: __nscVueAdd('DEMO-', id),
        connection: 'online',
        canWrite: document.getElementById('x-connect-write').checked,
        managerRead: true,
        observed: xTime(),
        historySince: xTime(),
        nodes: [node(uid('node'), type === 'swarm' ? 'demo-manager-01' : 'demo-host', type === 'swarm' ? 'manager' : 'host')],
        allowedEnvs: [env],
        networks: ['demo-network'],
        gaps: []
      });
      persist();
      OLD.closeModal(true);
      jump('runtime', id);
    }
    function demoSettings() {
      showModal({
        kind: 'x-demo',
        size: 'small',
        title: '故障与部署演示',
        sub: '只改变当前浏览器的演示状态',
        body: __nscVueView(templates[385], [x.scenario === 'success' ? 'selected' : '', x.scenario === 'partial' ? 'selected' : '', x.scenario === 'unknown' ? 'selected' : '', B('查看 OOM → 新 Task → 恢复链路', 'x-event', 'activity', '', 'data-id="INC-018"'), B('查看正常结束的一次性任务', 'x-service', 'checkcircle', '', 'data-id="db-migrate"'), B('查看节点采集缺口', 'x-runtime-events', 'server', '', 'data-id="swarm-prod"')]),
        footer: __nscVueAdd(B('关闭', 'close'), B('应用演示设置', 'x-demo-save', 'checkcircle', 'primary'))
      });
    }
    function preserveSearch(fn, id) {
      const el = document.getElementById(id),
        pos = el?.selectionStart;
      fn();
      const newEl = document.getElementById(id);
      newEl?.focus();
      if (pos !== undefined && pos !== null) try {
        newEl.setSelectionRange(pos, pos);
      } catch (_) {}
      ;
    }
    closeModal = function (force = false) {
      if (!force && modal?.kind === 'x-wizard' && x.wizard) {
        collectWizard();
        showModal({
          kind: 'x-wizard-exit',
          size: 'small',
          title: '离开部署向导？',
          sub: '尚未提交部署，线上没有变化。',
          body: callout('可以将当前输入保存为本地草稿，稍后继续；或放弃本次未保存修改。', 'amber'),
          footer: __nscVueAdd(__nscVueAdd(B('继续填写', 'x-wresume'), B('放弃未保存修改', 'x-wdiscard', '', 'danger')), B('保存部署草稿', 'x-wsave', 'save', 'primary'))
        });
        return;
      }
      if (!force && modal?.kind === 'x-wizard-exit') {
        renderWizard();
        return;
      }
      return OLD.closeModal(force);
    };
    render = function () {
      seedWorkloads();
      OLD.render();
      const active = ui.page === 'application' ? 'applications' : ui.page === 'service' ? 'services' : ui.page === 'runtime' ? 'runtimes' : null;
      if (active) document.querySelector(__nscVueAdd(__nscVueAdd('.sidebar [data-page="', active), '"]'))?.classList.add('active');
    };
    listen(document, 'click', event => {
      const el = event.target.closest('[data-action]');
      if (!el || el.disabled || !el.dataset.action.startsWith('x-')) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      const a = el.dataset.action,
        id = el.dataset.id;
      switch (a) {
        case 'x-nav':
          jump(el.dataset.page);
          break;
        case 'x-install':
          beginInstall();
          break;
        case 'x-install-file':
          beginInstall('file');
          break;
        case 'x-install-image':
          beginInstall('image');
          break;
        case 'x-template':
          OLD.closeModal(true);
          beginInstall('template', id);
          break;
        case 'x-template-info':
          templateInfo(id);
          break;
        case 'x-category':
          x.marketCategory = id;
          render();
          break;
        case 'x-app':
          jump('application', id);
          break;
        case 'x-app-tab':
          x.appTab = id;
          updateHash();
          render();
          break;
        case 'x-service':
          jump('service', id);
          break;
        case 'x-service-events':
          jump('service', id, 'events');
          break;
        case 'x-service-tab':
          x.serviceTab = id;
          updateHash();
          render();
          break;
        case 'x-all-services':
          ui.allProjects = true;
          ui.allEnvironments = true;
          x.svcScope = 'all';
          x.svcPage = 1;
          jump('services');
          break;
        case 'x-layout':
          x.layout = id;
          db.serviceLayout = id;
          persist();
          render();
          break;
        case 'x-page-prev':
          x.svcPage = Math.max(1, x.svcPage - 1);
          render();
          break;
        case 'x-page-next':
          x.svcPage++;
          render();
          break;
        case 'x-runtime':
          jump('runtime', id);
          break;
        case 'x-runtime-events':
          jump('runtime', id, 'events');
          break;
        case 'x-runtime-tab':
          x.runtimeTab = id;
          updateHash();
          render();
          break;
        case 'x-runtime-info':
          runtimeInfo(id);
          break;
        case 'x-runtime-toggle':
          {
            if (deploymentTimer) return toast('请等待当前模拟部署完成。', true);
            const r = RT(id);
            if (!r) return;
            r.connection = r.connection === 'online' ? 'offline' : 'online';
            if (r.connection === 'offline') r.gaps.push({
              from: xTime().slice(11),
              to: '至今',
              node: r.nodes[0].id,
              reason: '控制连接中断，期间操作与容器事件可能缺失。'
            });else {
              r.observed = xTime();
              const g = r.gaps.findLast(g => g.to === '至今' && g.reason.startsWith('控制'));
              if (g) g.to = xTime().slice(11);
            }
            persist();
            render();
            break;
          }
        case 'x-node':
          nodeInfo(id);
          break;
        case 'x-event':
          eventDetail(id);
          break;
        case 'x-instance':
          instanceDetail(id);
          break;
        case 'x-event-filter':
          x.eventFilter = id;
          render();
          break;
        case 'x-deployment':
          showDeployment(id);
          break;
        case 'x-history-tab':
          x.historyTab = id;
          render();
          break;
        case 'x-wsource':
          collectWizard();
          x.wizard.source = id;
          x.wizard.reviewAck = false;
          x.wizard.warningAck = false;
          renderWizard();
          break;
        case 'x-wsample':
          x.wizard.raw = SAMPLE_COMPOSE;
          renderWizard();
          break;
        case 'x-wnext':
          nextWizard();
          break;
        case 'x-wback':
          collectWizard();
          x.wizard.step = Math.max(1, x.wizard.step - 1);
          x.wizardError = '';
          renderWizard();
          break;
        case 'x-wsave':
          saveWizard();
          break;
        case 'x-wdeploy':
          deployWizard();
          break;
        case 'x-wresume':
          renderWizard();
          break;
        case 'x-wdiscard':
          x.wizard = null;
          OLD.closeModal(true);
          break;
        case 'x-resume-wizard':
          {
            const d = db.appDrafts.filter(inListScope).at(-1);
            if (d) {
              x.wizard = clone(d);
              x.wizard.reviewAck = false;
              x.wizard.warningAck = false;
              x.wizardError = '';
              renderWizard();
            }
            break;
          }
        case 'x-op-form':
          opForm(id, el.dataset.kind);
          break;
        case 'x-op-confirm':
          confirmOp();
          break;
        case 'x-retry':
          retryOperation(id);
          break;
        case 'x-verify':
          verifyOperation(id);
          break;
        case 'x-verify-result':
          verifiedResult(id, el.dataset.result);
          break;
        case 'x-restore':
          restoreApp(id, el.dataset.version || el.dataset.v);
          break;
        case 'x-restore-save':
          saveRestore();
          break;
        case 'x-restore-review':
          reviewRestore(id);
          break;
        case 'x-restore-deploy':
          deployRestore(id);
          break;
        case 'x-export-app':
          {
            const a = APP(id);
            if (a) download(__nscVueAdd(a.key, '-application-snapshot.json'), JSON.stringify({
              scope: scopeName(a.project, a.env),
              spec: appSpec(a),
              note: 'Prototype data only. Not a production Compose file.'
            }, null, 2), 'application/json');
            break;
          }
        case 'x-export-events':
          download('NSC-runtime-history.json', JSON.stringify({
            demo: true,
            historyCoverage: db.runtimes.map(r => ({
              runtime: r.id,
              since: r.historySince,
              gaps: r.gaps
            })),
            events: db.runtimeEvents,
            instances: db.instances
          }, null, 2), 'application/json');
          break;
        case 'x-connect':
          connectForm();
          break;
        case 'x-connect-save':
          saveConnection();
          break;
        case 'x-demo':
          demoSettings();
          break;
        case 'x-demo-save':
          x.scenario = document.getElementById('x-demo-scenario').value;
          OLD.closeModal(true);
          toast(__nscVueAdd('下一次部署：', {
            success: '成功',
            partial: '部分失败',
            unknown: '结果未知'
          }[x.scenario]));
          break;
      }
    }, true);
    listen(document, 'change', event => {
      const el = event.target;
      if (el.matches('[data-wf]')) {
        collectWizard();
        const w = x.wizard,
          k = el.dataset.wf;
        if (!w) return;
        w.touched = true;
        if (!['reviewAck', 'warningAck'].includes(k)) {
          w.reviewAck = false;
          w.warningAck = false;
        }
        if (k === 'project') {
          const e = db.environments.find(e => e.project === w.project);
          w.env = e?.id || '';
          w.secretRef = '';
          chooseCompatibleRuntime(w);
        }
        if (k === 'env') {
          w.secretRef = '';
          chooseCompatibleRuntime(w);
        }
        if (k === 'template') {
          const t = tpl(w.template);
          w.name = t.name;
          w.key = t.key;
          w.secretRef = '';
          w.storageAck = false;
          w.replicas = 1;
        }
        if (['runtime', 'singleMode'].includes(k)) {
          if (wizardMode(w) === 'container') w.replicas = 1;
        }
        if (k === 'access' && w.access === 'domain') {
          const ss = wizardServices(w);
          if (!ss[w.routeService]?.http) w.routeService = ss.findIndex(s => s.http);
        }
        if (el.tagName === 'SELECT' || el.type === 'checkbox') renderWizard();
        return;
      }
      const filters = {
        'x-service-scope': 'svcScope',
        'x-service-runtime': 'svcRuntime',
        'x-service-health': 'svcHealth'
      };
      if (filters[el.id]) {
        x[filters[el.id]] = el.value;
        if (el.id === 'x-service-scope') {
          ui.allProjects = el.value === 'all';
          ui.allEnvironments = el.value !== 'environment';
        }
        x.svcPage = 1;
        render();
        return;
      }
      if (el.id === 'x-include-history') {
        x.includeHistory = el.checked;
        render();
        return;
      }
      if (el.id === 'x-w-file') {
        const file = el.files?.[0];
        if (!file) return;
        if (file.size > 32768) return toast('演示导入上限为 32KB。', true);
        file.text().then(text => {
          if (!x.wizard) return;
          x.wizard.raw = text;
          x.wizard.source = 'file';
          x.wizard.reviewAck = false;
          renderWizard();
        });
      }
    }, true);
    listen(document, 'input', event => {
      const el = event.target;
      if (el.id === 'x-market-search') {
        x.marketSearch = el.value;
        preserveSearch(render, el.id);
      }
      if (el.id === 'x-service-search') {
        x.svcSearch = el.value;
        x.svcPage = 1;
        preserveSearch(render, el.id);
      }
      if (el.matches('[data-wf]')) {
        collectWizard();
        if (x.wizard) {
          x.wizard.touched = true;
          if (!['reviewAck', 'warningAck'].includes(el.dataset.wf)) {
            x.wizard.reviewAck = false;
            x.wizard.warningAck = false;
          }
        }
      }
    }, true);
    listen(document, 'keydown', event => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's' && x.wizard) {
        event.preventDefault();
        event.stopImmediatePropagation();
        saveWizard();
      }
    }, true);
    listen(window, 'beforeunload', event => {
      if (x.wizard?.touched || deploymentTimer) {
        event.preventDefault();
        event.returnValue = '';
      }
    });
    // A browser refresh is not evidence that a submitted operation was cancelled.
    let recovered = false;
    for (const op of db.appDeployments.filter(o => o.state === 'running')) {
      op.state = 'unknown';
      for (const t of op.targets) if (op.results[t] === 'pending') op.results[t] = 'unknown';
      if (op.attempts.length) {
        op.attempts.at(-1).state = 'unknown';
        op.attempts.at(-1).logs.push('[DEMO] 页面离开，执行结果未核实。');
      }
      recovered = true;
    }
    if (recovered) persist();

    // Test/read-only inspection hook; no network API and no credentials.
    NSCBridge = {
      x,
      APP,
      ST,
      RT,
      NODE,
      IS,
      OP,
      jump,
      appServices,
      appSpec,
      serviceState,
      clusterSummary,
      createOperation,
      writeGate,
      collectWizard,
      beginInstall,
      showDeployment,
      instanceDetail,
      eventDetail,
      setShowDeployment: fn => {
        showDeployment = fn;
      }
    };
  })();

  /* NSC v1.3: variables, declarative application files, node drilldown.
   * Additive to v1.2. Backend operations and evidence remain simulated.
   */
  (() => {
    'use strict';

    const __renderContext2 = {
      get B() {
        return B;
      },
      get E() {
        return E;
      },
      get I() {
        return I;
      },
      get P() {
        return P;
      },
      get TB() {
        return TB;
      },
      get W() {
        return W;
      },
      get Y() {
        return Y;
      },
      get badge() {
        return badge;
      },
      get banner() {
        return banner;
      },
      get callout() {
        return callout;
      },
      get compileApp() {
        return compileApp;
      },
      get compileSummary() {
        return compileSummary;
      },
      get db() {
        return db;
      },
      get docsDirty() {
        return docsDirty;
      },
      get ensureDoc() {
        return ensureDoc;
      },
      get esc() {
        return esc;
      },
      get importCompose() {
        return importCompose;
      },
      get installVersion() {
        return installVersion;
      },
      get label() {
        return label;
      },
      get nodeData() {
        return nodeData;
      },
      get ownRows() {
        return ownRows;
      },
      get scope() {
        return scope;
      },
      get scopeChoices() {
        return scopeChoices;
      },
      get scopeKey() {
        return scopeKey;
      },
      get templates() {
        return templates;
      },
      get typelabel() {
        return typelabel;
      },
      get usage() {
        return usage;
      },
      get varsDirty() {
        return varsDirty;
      },
      get versions() {
        return versions;
      },
      get viewRows() {
        return viewRows;
      },
      get yHead() {
        return yHead;
      }
    };
    const W = NSCBridge,
      C = NSCEngine,
      oldPage = globalPage,
      oldSidebar = projectSidebar,
      oldContext = contextBar;
    const Y = NSCWorkspace = {
      layer: 'environment',
      app: 'app-portal-prod',
      view: 'source',
      file: 'compose',
      previewEnv: '',
      node: 'p-w1',
      nodeTab: 'services',
      nodeHistory: false,
      nodeSearch: '',
      instance: '',
      instanceTab: 'overview',
      operation: '',
      query: '',
      varDraft: null,
      composeWork: null,
      runtimeWork: null,
      docApp: null,
      modalEdit: null,
      review: null,
      replaceNext: false
    };
    const typelabel = {
      string: '文本',
      integer: '整数',
      boolean: '布尔值',
      memory: '内存',
      credential: '凭据引用'
    };
    const label = {
      project: '项目默认',
      environment: '环境覆盖',
      application: '应用覆盖'
    };
    const rec = (value, type = 'string', note = '') => ({
      value: String(value),
      type,
      note
    });
    const clean = obj => JSON.parse(JSON.stringify(obj));
    function seed() {
      if (db.parameters13) return;
      db.parameters13 = {
        scopes: {},
        audit: [],
        documents: {}
      };
      const init = (k, rows) => {
        db.parameters13.scopes[k] = {
          version: 1,
          rows,
          history: [{
            version: 1,
            time: '2026-09-14 10:00:00',
            rows: clean(rows),
            note: '初始演示参数'
          }]
        };
      };
      for (const p of db.projects) init(__nscVueAdd('project:', p.id), {
        IMAGE_TAG: rec('1.0.0', 'string', '镜像标签'),
        TZ: rec('UTC'),
        APP_ENV: rec('development'),
        LOG_LEVEL: rec('info'),
        HTTP_PORT: rec('8080', 'integer'),
        REPLICAS: rec('1', 'integer'),
        CPU_LIMIT: rec('0.5'),
        MEMORY_LIMIT: rec('256M', 'memory'),
        RESTART_DELAY: rec('5s')
      });
      for (const e of db.environments) init(__nscVueAdd('environment:', e.id), e.kind === 'production' ? {
        IMAGE_TAG: rec('1.2.0'),
        APP_ENV: rec('production'),
        REPLICAS: rec('2', 'integer'),
        MEMORY_LIMIT: rec('512M', 'memory'),
        CPU_LIMIT: rec('1.0'),
        DB_PASSWORD: rec(__nscVueAdd(__nscVueAdd('secret://', e.id), '/database-password@v3'), 'credential', '仅保存引用，不读取实际密码')
      } : {
        IMAGE_TAG: rec('1.3.0-rc1'),
        APP_ENV: rec(e.slug || e.id),
        LOG_LEVEL: rec('debug')
      });
      for (const a of db.apps) init(__nscVueAdd('application:', a.id), a.id === 'app-portal-prod' ? {
        MEMORY_LIMIT: rec('1G', 'memory', '本应用需要更大内存')
      } : {});
      for (const a of db.apps) ensureDoc(a);
      persist();
    }
    function scopeKey() {
      return Y.layer === 'project' ? __nscVueAdd('project:', db.project) : Y.layer === 'application' ? __nscVueAdd('application:', Y.app) : __nscVueAdd('environment:', db.env);
    }
    function scope(k) {
      return db.parameters13.scopes[k] || (db.parameters13.scopes[k] = {
        version: 0,
        rows: {},
        history: []
      });
    }
    function layerRows(k) {
      return Y.varDraft?.key === k ? Y.varDraft.rows : scope(k).rows;
    }
    function effective(project = db.project, env = db.env, app = Y.app, withDraft = true) {
      const result = Object.create(null);
      for (const [k, source] of [[__nscVueAdd('project:', project), '项目默认'], [__nscVueAdd('environment:', env), '环境覆盖'], [__nscVueAdd('application:', app), '应用覆盖']]) {
        if (k.endsWith(':') || k.startsWith('application:') && W.APP(app)?.env !== env) continue;
        for (const [key, v] of Object.entries(withDraft ? layerRows(k) : scope(k).rows)) result[key] = {
          ...v,
          source,
          scope: k,
          version: scope(k).version
        };
      }
      return result;
    }
    function viewRows() {
      if (Y.layer === 'project') return Object.fromEntries(Object.entries(layerRows(scopeKey())).map(([k, v]) => [k, {
        ...v,
        source: '项目默认',
        scope: scopeKey()
      }]));
      return effective(db.project, db.env, Y.layer === 'application' ? Y.app : '');
    }
    function ownRows() {
      return layerRows(scopeKey());
    }
    function ensureDraft() {
      const key = scopeKey();
      if (!Y.varDraft || Y.varDraft.key !== key) Y.varDraft = {
        key,
        base: scope(key).version,
        rows: clean(scope(key).rows)
      };
      return Y.varDraft;
    }
    function varsDirty() {
      return !!Y.varDraft && JSON.stringify(Y.varDraft.rows) !== JSON.stringify(scope(Y.varDraft.key).rows);
    }
    function docsDirty() {
      if (!Y.docApp) return false;
      const d = db.parameters13.documents[Y.docApp];
      return !!d && ((Y.composeWork ?? d.source) !== (d.draft?.source ?? d.source) || (Y.runtimeWork ?? d.runtime) !== (d.draft?.runtime ?? d.runtime));
    }
    function ensureDoc(a) {
      if (!a) return null;
      let d = db.parameters13.documents[a.id];
      if (d) return d;
      if (a.raw) {
        d = {
          source: a.raw,
          runtime: '',
          draft: null,
          lastBundle: null,
          mapping: Object.fromEntries(W.appServices(a).map(s => [s.name, s.id]))
        };
        db.parameters13.documents[a.id] = d;
        return d;
      }
      const runtime = W.RT(a.runtime);
      let lines = ['# NSC 示例编排定义；并非从运行容器反向还原。', '# 项目变量只有被引用才会使用；不会全部注入容器。', 'services:'];
      for (const s of W.appServices(a)) {
        const ref = s.workloadKind !== 'job';
        lines.push(__nscVueAdd(__nscVueAdd('  ', s.id), ':'), __nscVueAdd(__nscVueAdd('    image: "', (s.image || 'demo/service:1.0.0').replace(/:[^/:]+$/, ':${IMAGE_TAG:-1.0.0}')), '"'), '    environment:', '      APP_ENV: "${APP_ENV:-production}"', '      LOG_LEVEL: "${LOG_LEVEL:-info}"', '      TZ: "${TZ:-UTC}"', '    env_file:', '      - runtime.env');
        if (s.port) lines.push('    expose:', __nscVueAdd(__nscVueAdd('      - "', s.id.includes('api') ? '${HTTP_PORT:-8080}' : s.port), '"'));
        if (runtime?.type === 'swarm') {
          lines.push('    deploy:');
          if (!ref) lines.push('      mode: replicated-job');
          lines.push(__nscVueAdd('      replicas: ', ref ? '${REPLICAS:-1}' : '1'), '      resources:', '        limits:', '          cpus: "${CPU_LIMIT:-0.5}"', '          memory: "${MEMORY_LIMIT:-256M}"');
          if (ref) lines.push('      restart_policy:', '        condition: on-failure', '        delay: "${RESTART_DELAY:-5s}"');
        } else lines.push('    restart: unless-stopped', '    cpus: "${CPU_LIMIT:-0.5}"', '    mem_limit: "${MEMORY_LIMIT:-256M}"');
      }
      d = {
        source: __nscVueAdd(__nscVueJoin(lines, '\n'), '\n'),
        runtime: 'LOG_LEVEL=warning\nFEATURE_FLAGS=stable\n',
        draft: null,
        lastBundle: null
      };
      db.parameters13.documents[a.id] = d;
      return d;
    }
    function loadDoc(id) {
      const a = W.APP(id);
      const d = ensureDoc(a);
      if (!d) return;
      Y.docApp = id;
      Y.composeWork = d.draft?.source ?? d.source;
      Y.runtimeWork = d.draft?.runtime ?? d.runtime;
      Y.previewEnv = a.env;
      Y.view = 'source';
      Y.file = 'compose';
    }
    function saveVars(silent = false) {
      if (!varsDirty()) return true;
      const d = Y.varDraft,
        s = scope(d.key);
      if (ui.externalConflict || s.version !== d.base) {
        toast('变量已被其他编辑更新；保留当前输入，请重新核对差异。', true);
        return false;
      }
      s.version++;
      s.rows = clean(d.rows);
      s.history.unshift({
        version: s.version,
        time: stamp(),
        rows: clean(s.rows),
        note: '更新变量；未重新部署'
      });
      db.parameters13.audit.unshift({
        id: uid('VAR'),
        scope: d.key,
        version: s.version,
        time: stamp(),
        keys: Object.keys(s.rows),
        note: '已保存，运行中应用不变'
      });
      Y.varDraft = null;
      Y.review = null;
      persist();
      if (!silent) {
        render();
        toast('变量已保存。运行中的应用不变；重新检查应用后再部署。');
      }
      return true;
    }
    function saveDoc(silent = false) {
      const a = W.APP(Y.docApp),
        d = ensureDoc(a);
      if (!d) return true;
      if (/(?:PASSWORD|TOKEN|SECRET|PRIVATE_KEY)\s*[:=]\s*["']?(?!\$)[A-Za-z0-9]/i.test(__nscVueAdd(__nscVueAdd(Y.composeWork || '', '\n'), Y.runtimeWork || ''))) {
        toast('可能含有明文凭据；此离线原型只接受凭据引用，请移除敏感值。', true);
        return false;
      }
      if (ui.externalConflict) {
        toast('工作区有并发更新，请保留输入并重新核对。', true);
        return false;
      }
      d.draft = {
        source: Y.composeWork,
        runtime: Y.runtimeWork,
        base: a.version,
        time: stamp()
      };
      persist();
      Y.review = null;
      if (!silent) {
        render();
        toast('编排草稿已保存；没有运行 Docker 或重启实例。');
      }
      return true;
    }
    function versions(a, env = a.env) {
      return {
        project: scope(__nscVueAdd('project:', a.project)).version,
        environment: scope(__nscVueAdd('environment:', env)).version,
        application: env === a.env ? scope(__nscVueAdd('application:', a.id)).version : null
      };
    }
    function compileApp() {
      const a = W.APP(Y.docApp);
      if (!a) return {
        errors: ['应用不存在'],
        warnings: [],
        refs: [],
        runtime: [],
        text: ''
      };
      return C.compile(Y.composeWork, effective(a.project, Y.previewEnv || a.env, Y.previewEnv === a.env ? a.id : '', true), {
        target: W.RT(a.runtime)?.type === 'swarm' ? 'stack' : 'compose',
        envFiles: {
          'runtime.env': Y.runtimeWork
        }
      });
    }
    function usage(key) {
      let list = [];
      for (const a of db.apps.filter(a => a.project === db.project)) {
        const d = ensureDoc(a);
        const text = d.draft?.source ?? d.source;
        const re = new RegExp(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('\\$(?:\\{', key), '(?:[:?+\\-}]|$)|'), key), '(?:\\W|$))'));
        if (re.test(text)) {
          const v = effective(a.project, a.env, a.id, false)[key];
          list.push({
            a,
            effective: v,
            shadowed: v?.scope !== scopeKey()
          });
        }
      }
      return list;
    }
    function banner(...args) {
      return __variablesViews.banner_69(__renderContext2, ...args);
    }
    function yHead(...args) {
      return __variablesViews.yHead_70(__renderContext2, ...args);
    }
    function scopeChoices(...args) {
      return __variablesViews.scopeChoices_71(__renderContext2, ...args);
    }
    function variablesPage(...args) {
      return __variablesViews.variablesPage_72(__renderContext2, ...args);
    }
    function editVar(key = '') {
      const value = viewRows()[key] || rec('');
      Y.modalEdit = {
        key,
        original: clean(value)
      };
      showModal({
        kind: 'y-variable',
        size: 'drawer',
        title: key ? __nscVueAdd('修改变量 · ', key) : '添加变量',
        sub: __nscVueAdd(__nscVueAdd(label[Y.layer], ' · '), Y.layer === 'project' ? P().name : E().name),
        body: __nscVueView(templates[399], [esc(key), key ? 'readonly' : '', __nscVueJoin(Object.entries(typelabel).map(([k, n]) => __nscVueView(templates[398], [k, value.type === k ? 'selected' : '', n])), ''), value.type === 'credential' ? esc(value.value) : esc(value.value), esc(value.note || ''), callout('先加入当前页面的未保存修改，再统一“保存变量”。不会重启正在运行的容器。', 'blue')]),
        footer: __nscVueAdd(B('取消', 'close'), B('加入待保存修改', 'y-var-apply', 'check', 'primary'))
      });
    }
    function applyVar() {
      const k = document.getElementById('y-key').value.trim(),
        type = document.getElementById('y-type').value,
        value = document.getElementById('y-value').value,
        note = document.getElementById('y-note').value;
      let error = '';
      if (!/^[A-Z_][A-Z0-9_]{0,95}$/.test(k)) error = '变量名使用大写字母、数字和下划线，不能以数字开头。';
      if (/^(DOCKER_|COMPOSE_|NSC_)/.test(k)) error = '这是平台执行参数保留名称。连接位置和 Compose 执行选项应从专用表单配置，不能通过变量改变。';
      if (type === 'integer' && !/^-?\d+$/.test(value)) error = '请填写整数。';
      if (type === 'boolean' && !['true', 'false'].includes(value)) error = '布尔值只能是 true 或 false。';
      if (type === 'memory' && !/^\d+(?:\.\d+)?[bkmg]?(?:b)?$/i.test(value)) error = '内存值例如 512M 或 1G。';
      if (type === 'credential' && !/^secret:\/\/[A-Za-z0-9_./-]+@[A-Za-z0-9_.-]+$/.test(value)) error = '请填写有明确版本的凭据引用，不要填写实际密码。';
      if (/(PASSWORD|TOKEN|SECRET|PRIVATE_KEY)$/i.test(k) && type !== 'credential') error = '此变量名称可能包含机密，请改为“凭据引用”。';
      if (value.includes('$(') || value.includes('${')) error = '变量值按字面量保存，不执行 Shell 或变量间的递归展开。';
      if (value.length > 4096) error = '演示变量值不能超过 4096 字符。';
      if (!Y.modalEdit?.key && Object.hasOwn(ownRows(), k)) error = '当前层级已经定义此变量，请编辑已有记录。';
      if (error) {
        region(document.getElementById('y-var-error'), callout(esc(error), 'red'));
        return;
      }
      ensureDraft().rows[k] = {
        type,
        value,
        note
      };
      Y.modalEdit = null;
      closeModal(true);
      render();
    }
    function removeVar(key) {
      const current = ownRows()[key];
      if (!current) return;
      const refs = usage(key);
      showModal({
        kind: 'y-remove',
        size: 'small',
        title: Y.layer === 'project' ? '删除项目默认变量？' : '移除此处的覆盖值？',
        sub: key,
        body: callout(Y.layer === 'project' ? __nscVueInterpolate(["\u6709 ", " \u4E2A\u5E94\u7528\u5F15\u7528\u6B64\u952E\u3002\u5DF2\u6709\u90E8\u7F72\u4E0D\u53D8\uFF1B\u4E0B\u4E00\u6B21\u90E8\u7F72\u5FC5\u987B\u91CD\u65B0\u68C0\u67E5\u7F3A\u5931\u503C\u3002"], [refs.length]) : '移除后会重新使用上一层值；没有上层值时才使用文件默认值，否则检查阻断。已有部署不变。', 'amber'),
        footer: __nscVueAdd(B('保留', 'close'), B('加入待保存修改', 'y-var-remove-confirm', 'trash', 'danger', __nscVueInterpolate(["data-key=\"", "\""], [key])))
      });
    }
    function usageDialog(key) {
      const items = usage(key);
      showModal({
        kind: 'y-usage',
        size: 'xwide',
        title: __nscVueAdd(key, ' · 引用与影响'),
        sub: '基于已保存的编排定义；不是流量估计',
        body: __nscVueView(templates[402], [__nscVueJoin(items.map(({
          a,
          effective: v,
          shadowed
        }) => __nscVueView(templates[400], [esc(a.name), esc(E(a.env)?.name), esc(v?.source || '文件默认 / 未定义'), shadowed ? '被其他层覆盖，不直接生效' : '需要重新检查并部署', B('查看编排', 'y-open-compose', 'code', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [a.id]))])), '') || __nscVueView(templates[401], [])]),
        footer: B('关闭', 'close')
      });
    }
    function importEnv() {
      showModal({
        kind: 'y-env-import',
        size: 'xwide',
        title: '导入变量 · .env',
        sub: '只导入当前层级，不会上传到容器；请勿粘贴真实密码',
        body: __nscVueView(templates[403], []),
        footer: __nscVueAdd(B('取消', 'close'), B('分析导入内容', 'y-import-analyze', 'search', 'primary'))
      });
    }
    function analyzeImport() {
      const parsed = C.parseEnv(document.getElementById('y-import').value);
      for (const k of Object.keys(parsed.rows)) if (!/^[A-Z_][A-Z0-9_]*$/.test(k) || /^(DOCKER_|COMPOSE_|NSC_)/.test(k) || /(PASSWORD|TOKEN|SECRET|PRIVATE_KEY)$/i.test(k)) parsed.errors.push('包含保留名称、无效名称或疑似机密；请从变量表单设置凭据引用。');
      if (!Object.keys(parsed.rows).length) parsed.errors.push('没有可导入变量。');
      if (parsed.errors.length) {
        Y.importRows = null;
        region(document.getElementById('y-import-result'), callout(esc(__nscVueJoin([...new Set(parsed.errors)], '；')), 'red'));
        return;
      }
      Y.importRows = parsed.rows;
      region(document.getElementById('y-import-result'), __nscVueView(templates[405], [Object.keys(parsed.rows).length, Object.keys(parsed.rows).filter(k => Object.hasOwn(ownRows(), k)).length, __nscVueJoin(Object.keys(parsed.rows).map(k => __nscVueView(templates[404], [esc(k)])), ''), B('确认加入待保存修改', 'y-import-apply', 'check', 'primary')]));
    }
    function historyVars() {
      const s = scope(scopeKey());
      showModal({
        kind: 'y-history',
        size: 'xwide',
        title: '变量版本记录',
        sub: __nscVueAdd(__nscVueAdd(label[Y.layer], ' · '), scopeKey()),
        body: __nscVueView(templates[407], [callout('保存版本不是应用部署。机密记录只包含凭据引用；不能用此历史恢复数据库。', 'blue'), __nscVueJoin(s.history.map(h => __nscVueView(templates[406], [h.version, h.time, esc(h.note), esc(__nscVueJoin(Object.entries(h.rows).map(([k, v]) => __nscVueAdd(__nscVueAdd(k, ' = '), v.type === 'credential' ? '[凭据引用已脱敏]' : v.value)), '\n'))])), '') || '尚无保存记录']),
        footer: B('关闭', 'close')
      });
    }
    function composePage(...args) {
      return __variablesViews.composePage_73(__renderContext2, ...args);
    }
    function compileSummary(...args) {
      return __variablesViews.compileSummary_74(__renderContext2, ...args);
    }
    function reviewCompose() {
      const a = W.APP(Y.docApp);
      if (!a || !a.managed || Y.previewEnv !== a.env) return;
      const r = compileApp();
      const d = ensureDoc(a);
      if (r.errors.length) {
        Y.view = 'source';
        render();
        toast('请先处理参数检查中的阻断项。', true);
        return;
      }
      if (varsDirty()) {
        toast('先保存变量，再冻结应用部署内容。', true);
        return;
      }
      if (!saveDoc(true)) return;
      const expectedIds = Object.keys(d.mapping || Object.fromEntries(W.appServices(a).map(s => [s.id, s.id])));
      if (JSON.stringify(expectedIds.sort()) !== JSON.stringify(Object.keys(r.tree.services).sort())) {
        toast('服务集合有增删。此原型不自动处理迁移，请先核对应用结构。', true);
        return;
      }
      Y.review = {
        app: a.id,
        base: a.version,
        versions: versions(a),
        source: Y.composeWork,
        runtime: Y.runtimeWork,
        result: clean(r),
        time: stamp()
      };
      showModal({
        kind: 'y-compose-review',
        size: 'xwide',
        title: '审阅应用配置与参数',
        sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(P(a.project).name, ' / '), E(a.env).name), ' / '), a.name),
        body: __nscVueView(templates[425], [esc(W.RT(a.runtime).name), a.version, badge('本地模拟', 'blue'), versions(a).project, versions(a).environment, versions(a).application, __nscVueJoin(Object.entries(r.tree.services).map(([k, s]) => __nscVueView(templates[424], [esc(k), esc(s.image), s.deploy?.replicas ?? 1, esc(s.deploy?.resources?.limits?.cpus ?? s.cpus ?? '未声明'), esc(s.deploy?.resources?.limits?.memory ?? s.mem_limit ?? '未声明')])), ''), esc(r.text)]),
        footer: __nscVueAdd(B('继续编辑', 'close'), B('确认并模拟部署', 'y-compose-deploy', 'send', 'primary'))
      });
    }
    function deployCompose() {
      const f = Y.review,
        a = W.APP(f?.app);
      if (!a || !document.getElementById('y-review-ack')?.checked) return toast('请确认部署影响。', true);
      if (ui.externalConflict || a.version !== f.base || JSON.stringify(versions(a)) !== JSON.stringify(f.versions)) return toast('应用或变量版本已变化；此次确认已失效，请重新审阅。', true);
      if (!W.writeGate(a.runtime, a)) return;
      const d = ensureDoc(a),
        previous = a.version,
        spec = W.appSpec(a);
      const map = d.mapping || Object.fromEntries(W.appServices(a).map(s => [s.id, s.id]));
      for (const [key, p] of Object.entries(f.result.tree.services)) {
        const id = map[key],
          s = W.ST(id),
          ds = spec.services.find(s => s.id === id);
        if (!s || !ds) return toast('服务映射已经失效，请重新检查。', true);
        Object.assign(ds, {
          image: p.image,
          replicas: Number(p.deploy?.replicas ?? 1),
          memory: memoryMiB(p.deploy?.resources?.limits?.memory ?? p.mem_limit ?? '512M'),
          environment: f.result.runtime.find(t => t.service === key)?.env || {},
          dockerConfig: p
        });
        Object.assign(s, {
          image: ds.image,
          desired: ds.replicas,
          memory: ds.memory,
          specVersion: __nscVueAdd(a.version, 1)
        });
      }
      const bundle = {
        source: f.source,
        rendered: f.result.text,
        runtimeEnvFile: f.runtime,
        variableVersions: f.versions,
        refs: f.result.refs,
        validatedBy: 'NSC bounded demo rules, not Docker',
        frozenAt: stamp()
      };
      spec.composeBundle = clean(bundle);
      a.version++;
      a.revisions.push({
        v: a.version,
        time: stamp(),
        note: '编排与参数变更',
        spec: clean(spec)
      });
      d.source = f.source;
      d.runtime = f.runtime;
      d.draft = null;
      d.lastBundle = clean(bundle);
      Y.review = null;
      closeModal(true);
      W.createOperation(a, '部署编排与参数', 'config', spec, spec.services.map(s => s.id), previous);
    }
    function installVersion(w) {
      return {
        project: scope(__nscVueAdd('project:', w.project)).version,
        environment: scope(__nscVueAdd('environment:', w.env)).version
      };
    }
    function checkImportStructure(raw) {
      try {
        const t = C.parseYAML(raw);
        if (!t.services || Array.isArray(t.services) || typeof t.services !== 'object') throw Error('需要 services 映射。');
        return {
          errors: []
        };
      } catch (e) {
        return {
          errors: [e.message]
        };
      }
    }
    function importCompose(raw, w) {
      w = w || {
        project: db.project,
        env: db.env,
        runtime: 'swarm-prod'
      };
      const result = C.compile(raw, effective(w.project, w.env, '', false), {
        target: W.RT(w.runtime)?.type === 'docker' ? 'compose' : 'stack',
        envFiles: {}
      });
      let services = [];
      if (result.tree?.services) services = Object.entries(result.tree.services).filter(([, v]) => v && typeof v === 'object').map(([name, p]) => ({
        name,
        image: String(p.image || ''),
        port: parseInt(p.expose?.[0]) || 0,
        http: !!parseInt(p.expose?.[0]),
        replicas: Number(p.deploy?.replicas ?? 1),
        memory: memoryMiB(p.deploy?.resources?.limits?.memory ?? p.mem_limit ?? '512M')
      }));
      return {
        services,
        errors: result.errors,
        result
      };
    }
    function installReviewHTML(...args) {
      return __variablesViews.installReviewHTML_75(__renderContext2, ...args);
    }
    function initializeImportedApp(a, w) {
      const q = importCompose(w.raw, w);
      const d = ensureDoc(a);
      d.source = w.raw;
      d.mapping = Object.fromEntries(W.appServices(a).map(s => [s.name, s.id]));
      for (const part of q.services) {
        const s = W.ST(d.mapping[part.name]);
        if (s) {
          s.desired = part.replicas;
          s.memory = part.memory;
          const cfg = q.result.tree.services[part.name];
          s.workloadKind = cfg.deploy?.mode === 'replicated-job' ? 'job' : 'replicated';
        }
      }
      d.lastBundle = {
        source: w.raw,
        rendered: q.result.text,
        runtimeEnvFile: '',
        variableVersions: installVersion(w),
        refs: q.result.refs,
        frozenAt: stamp(),
        validatedBy: 'NSC bounded demo rules, not Docker'
      };
      scope(__nscVueAdd('application:', a.id));
    }
    Object.assign(Y, {
      installVersion,
      checkImportStructure,
      importCompose,
      installReviewHTML,
      initializeImportedApp
    });
    function memoryMiB(v) {
      const m = String(v).match(/^([\d.]+)([gmkb]?)/i);
      if (!m) return 512;
      const unit = m[2].toLowerCase();
      return Math.max(1, Math.round(Number(m[1]) * {
        g: 1024,
        m: 1,
        k: 1 / 1024,
        b: 1 / 1048576,
        '': 1 / 1048576
      }[unit]));
    }
    function nodeData() {
      const n = W.NODE(Y.node),
        r = db.runtimes.find(r => r.nodes.some(n => n.id === Y.node));
      const all = db.instances.filter(t => t.node === Y.node);
      return {
        n,
        r,
        all,
        current: all.filter(t => t.current),
        services: [...new Set(all.filter(t => t.current).map(t => t.service))].map(W.ST).filter(Boolean)
      };
    }
    function nodePage(...args) {
      return __infrastructureViews.nodePage_76(__renderContext2, ...args);
    }
    function instancePage(...args) {
      return __infrastructureViews.instancePage_77(__renderContext2, ...args);
    }
    function operationPage(...args) {
      return __infrastructureViews.operationPage_78(__renderContext2, ...args);
    }
    Object.assign(PAGE_NAMES, {
      variables: '变量与配置',
      compose: '编排与参数',
      node: '节点详情',
      instance: '运行实例',
      operation: '部署记录',
      notfound: '资源不存在'
    });
    contextBar = function () {
      if (['node', 'instance', 'notfound'].includes(ui.page)) return '';
      return oldContext();
    };
    globalPage = function () {
      if (ui.page === 'variables') return variablesPage();
      if (ui.page === 'compose') return composePage();
      if (ui.page === 'node') return nodePage();
      if (ui.page === 'instance') return instancePage();
      if (ui.page === 'operation') return operationPage();
      if (ui.page === 'notfound') return __nscVueAdd(yHead('无法定位这个资源', '链接中的资源不存在、已删除或不属于指定项目环境。没有回退到猜测的同名资源。', B('返回项目列表', 'nav', 'back', 'primary', 'data-page="projects"')), __nscVueView(templates[451], [esc(Y.routeError || '请检查链接或从列表重新进入。')]));
      let h = oldPage();
      if (ui.page === 'application') {
        const a = W.APP(W.x.app);
        if (a) h = insertView(h, '.hero-actions', B('编排与参数', 'y-open-compose', 'code', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [a.id])));
      }
      return h;
    };
    function showOperation(id) {
      const o = W.OP(id);
      if (!o) return;
      closeModal(true);
      Y.operation = id;
      selectScope(o.project, o.env);
      ui.page = 'operation';
      updateHash();
      render();
    }
    W.setShowDeployment(showOperation);
    function openCompose(id) {
      const a = W.APP(id);
      if (!a) return;
      ensureNavigate(() => {
        closeModal(true);
        selectScope(a.project, a.env);
        loadDoc(id);
        ui.page = 'compose';
        ui.menu = false;
        updateHash();
        render();
        window.scrollTo(0, 0);
      });
    }
    function openNode(id) {
      const n = W.NODE(id);
      if (!n) return;
      ensureNavigate(() => {
        closeModal(true);
        Y.node = id;
        Y.nodeTab = 'services';
        Y.nodeHistory = false;
        Y.nodeSearch = '';
        ui.page = 'node';
        ui.menu = false;
        updateHash();
        render();
        window.scrollTo(0, 0);
      });
    }
    function openInstance(id, kind) {
      const t = W.IS(id);
      if (!t) return;
      ensureNavigate(() => {
        closeModal(true);
        Y.instance = id;
        Y.instanceKind = kind || t.kind;
        Y.instanceTab = 'overview';
        ui.page = 'instance';
        ui.menu = false;
        updateHash();
        render();
        window.scrollTo(0, 0);
      });
    }
    listen(window, 'click', e => {
      const el = e.target.closest('[data-action]');
      if (!el || el.disabled || e.button > 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
      const a = el.dataset.action,
        id = el.dataset.id,
        key = el.dataset.key;
      if (a === 'x-node' || a === 'x-instance' || a === 'x-deployment') {
        e.preventDefault();
        e.stopImmediatePropagation();
        if (a === 'x-node') openNode(id);
        if (a === 'x-instance') openInstance(id);
        if (a === 'x-deployment') ensureNavigate(() => showOperation(id));
        return;
      }
      if (a === 'x-restore') {
        const app = W.APP(id),
          rev = app?.revisions.find(r => r.v === Number(el.dataset.version || el.dataset.v));
        if (!rev?.spec.composeBundle) {
          e.preventDefault();
          e.stopImmediatePropagation();
          toast('这份旧记录未保存完整编排与变量快照，不能可靠恢复。请从当前编排创建新草稿。', true);
          return;
        }
      }
      if (a === 'x-op-form' && ['image', 'scale'].includes(el.dataset.kind)) {
        e.preventDefault();
        e.stopImmediatePropagation();
        openCompose(W.ST(id)?.app);
        toast('镜像和数量请在编排文件或变量中修改；部署后形成同一份应用版本。');
        return;
      }
      if (!a.startsWith('y-') || a.startsWith('y-guard-')) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      switch (a) {
        case 'y-layer':
          if (id === 'application' && !Y.app) {
            toast('当前环境没有应用，请先创建或部署应用。', true);
            break;
          }
          ensureNavigate(() => {
            Y.layer = id;
            Y.varDraft = null;
            render();
          });
          break;
        case 'y-vars-save':
          saveVars();
          break;
        case 'y-var-add':
          editVar();
          break;
        case 'y-var-edit':
          editVar(key);
          break;
        case 'y-var-apply':
          applyVar();
          break;
        case 'y-var-remove':
          removeVar(key);
          break;
        case 'y-var-remove-confirm':
          delete ensureDraft().rows[key];
          closeModal(true);
          render();
          break;
        case 'y-var-usage':
          usageDialog(key);
          break;
        case 'y-var-history':
          historyVars();
          break;
        case 'y-env-import':
          importEnv();
          break;
        case 'y-import-analyze':
          analyzeImport();
          break;
        case 'y-import-apply':
          if (Y.importRows) {
            const d = ensureDraft();
            for (const [k, v] of Object.entries(Y.importRows)) d.rows[k] = rec(v);
            Y.importRows = null;
            closeModal(true);
            render();
          }
          break;
        case 'y-open-compose':
          openCompose(id);
          break;
        case 'y-compose-save':
          saveDoc();
          break;
        case 'y-compose-review':
          reviewCompose();
          break;
        case 'y-compose-deploy':
          deployCompose();
          break;
        case 'y-view':
          Y.view = id;
          render();
          break;
        case 'y-file':
          Y.file = id;
          Y.view = 'source';
          render();
          break;
        case 'y-vars-from-compose':
          ensureNavigate(() => {
            Y.app = Y.docApp;
            Y.layer = 'environment';
            Y.varDraft = null;
            ui.page = 'variables';
            render();
          });
          break;
        case 'y-node-tab':
          Y.nodeTab = id;
          Y.nodeSearch = '';
          if (el.dataset.history !== undefined) Y.nodeHistory = el.dataset.history === '1';
          render();
          break;
        case 'y-service-on-node':
          Y.nodeTab = W.RT(W.ST(id)?.runtimeId)?.type === 'swarm' ? 'tasks' : 'containers';
          Y.nodeSearch = W.ST(id)?.name || id;
          Y.nodeHistory = false;
          render();
          break;
        case 'y-node-parent':
          W.jump('runtime', id, 'nodes');
          break;
        case 'y-instance':
          openInstance(id, el.dataset.kind);
          break;
        case 'y-instance-tab':
          Y.instanceTab = id;
          render();
          break;
        case 'y-copy-link':
          if (!navigator.clipboard) {
            toast('当前浏览器无法复制，请复制地址栏链接。', true);
            break;
          }
          navigator.clipboard.writeText(location.href).then(() => toast('当前页面链接已复制。')).catch(() => toast('当前浏览器无法复制，请复制地址栏链接。', true));
          break;
      }
    }, true);
    let compileTimer;
    listen(window, 'input', e => {
      const el = e.target;
      if (el.id === 'y-compose-code') {
        if (Y.file === 'compose') Y.composeWork = el.value;else Y.runtimeWork = el.value;
        Y.review = null;
        clearTimeout(compileTimer);
        compileTimer = setTimeout(() => {
          const target = document.getElementById('y-compile-summary');
          if (target) region(target, compileSummary(compileApp()));
        }, 180);
        document.querySelector('.y-editor-status')?.classList.add('changed');
      }
      if (el.id === 'y-import') {
        Y.importRows = null;
        region(document.getElementById('y-import-result'), '');
      }
      if (['y-var-search', 'y-node-search'].includes(el.id)) {
        const pos = el.selectionStart;
        Y.replaceNext = true;
        if (el.id === 'y-var-search') Y.query = el.value;else Y.nodeSearch = el.value;
        render();
        const fresh = document.getElementById(el.id);
        fresh?.focus();
        fresh?.setSelectionRange(pos, pos);
      }
    }, true);
    listen(window, 'change', e => {
      const el = e.target;
      if (el.id === 'y-var-app') {
        const next = el.value;
        ensureNavigate(() => {
          Y.app = next;
          Y.varDraft = null;
          render();
        });
      }
      if (el.id === 'y-preview-env') {
        Y.previewEnv = el.value;
        Y.review = null;
        render();
      }
      if (el.id === 'y-node-history') {
        Y.nodeHistory = el.checked;
        render();
      }
    }, true);
    listen(window, 'keydown', e => {
      if (ui.page === 'compose' && (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
        e.preventDefault();
        e.stopImmediatePropagation();
        saveDoc();
      }
      if (ui.page === 'variables' && (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 's') {
        e.preventDefault();
        e.stopImmediatePropagation();
        saveVars();
      }
      if (e.target.id === 'y-compose-code' && e.key === 'Tab') {
        e.preventDefault();
        const t = e.target,
          start = t.selectionStart,
          end = t.selectionEnd;
        t.setRangeText('  ', start, end, 'end');
        t.dispatchEvent(new Event('input', {
          bubbles: true
        }));
      }
    }, true);
    function modalDirty() {
      if (modal?.kind === 'y-variable') {
        const original = Y.modalEdit?.original;
        return !!original && [document.getElementById('y-key')?.value !== Y.modalEdit.key, document.getElementById('y-value')?.value !== original.value, document.getElementById('y-type')?.value !== original.type, document.getElementById('y-note')?.value !== (original.note || '')].some(Boolean);
      }
      if (modal?.kind === 'y-env-import') return !!document.getElementById('y-import')?.value.trim();
      return false;
    }
    function discardExtra() {
      Y.varDraft = null;
      if (Y.docApp) loadDoc(Y.docApp);
      Y.modalEdit = null;
      Y.importRows = null;
      W.x.wizard = null;
    }
    function saveExtra() {
      if (modal?.kind === 'y-variable' && modalDirty()) {
        applyVar();
        if (modal?.kind === 'y-variable') return false;
      }
      if (modal?.kind === 'y-env-import' && modalDirty()) {
        toast('请先完成导入分析并确认，再离开页面。', true);
        return false;
      }
      if (!saveVars(true)) return false;
      if (docsDirty() && !saveDoc(true)) return false;
      return true;
    }
    Object.assign(Y, {
      seed,
      scope,
      scopeKey,
      effective,
      ensureDoc,
      loadDoc,
      varsDirty,
      docsDirty,
      modalDirty,
      saveVars,
      saveDoc,
      saveExtra,
      discardExtra,
      compileApp,
      openCompose,
      openNode,
      openInstance,
      showOperation,
      versions,
      usage
    });
    seed();
  })();
  function topbar(...args) {
    return __shellViews.topbar_79(__renderContext0, ...args);
  }
  const W = NSCBridge,
    Y = NSCWorkspace,
    enc = encodeURIComponent;
  let routeBusy = false,
    guardState = null;
  function query(obj) {
    const q = new URLSearchParams();
    for (const [k, v] of Object.entries(obj)) if (v !== undefined && v !== null && v !== '' && v !== false) q.set(k, String(v));
    return q.size ? __nscVueAdd('?', q.toString()) : '';
  }
  function routePath() {
    let path = '',
      q = {};
    const pr = __nscVueAdd('/projects/', enc(listProject())),
      root = __nscVueAdd(__nscVueAdd(pr, '/environments/'), enc(listEnvironment()));
    if (ui.page === 'notfound') return Y.badPath || '/not-found';
    if (ui.page === 'projects') path = '/projects';else if (ui.page === 'project') path = pr;else if (ui.page === 'environments') path = __nscVueAdd(pr, '/environments');else if (ui.page === 'variables') {
      path = Y.layer === 'project' ? __nscVueAdd(pr, '/variables') : __nscVueAdd(root, '/variables');
      q = {
        layer: Y.layer,
        app: Y.layer === 'application' ? Y.app : '',
        q: Y.query
      };
    } else if (ui.page === 'compose') {
      const a = W.APP(Y.docApp);
      path = __nscVueInterpolate(["/projects/", "/environments/", "/applications/", "/compose"], [enc(a?.project || db.project), enc(a?.env || db.env), enc(Y.docApp)]);
      q = {
        view: Y.view,
        file: Y.file === 'runtime' ? 'runtime' : '',
        previewEnv: Y.previewEnv !== a?.env ? Y.previewEnv : ''
      };
    } else if (ui.page === 'application') {
      path = __nscVueAdd(__nscVueAdd(root, '/applications/'), enc(W.x.app));
      q.tab = W.x.appTab;
    } else if (ui.page === 'service') {
      const s = W.ST(W.x.service);
      path = s?.project ? __nscVueInterpolate(["/projects/", "/environments/", "/services/", ""], [enc(s.project), enc(s.env), enc(s.id)]) : __nscVueAdd('/services/', enc(W.x.service));
      q = {
        tab: W.x.serviceTab,
        history: W.x.includeHistory ? '1' : ''
      };
    } else if (ui.page === 'domain') {
      path = __nscVueAdd(__nscVueAdd(root, '/domains/'), enc(ui.domain));
      q = {
        tab: ui.tab,
        editor: ui.tab === 'config' ? ui.configView : '',
        file: ui.tab === 'config' ? ui.file : ''
      };
    } else if (ui.page === 'runtime') {
      path = __nscVueAdd('/infrastructure/runtimes/', enc(W.x.runtime));
      q.tab = W.x.runtimeTab;
    } else if (ui.page === 'node') {
      const r = db.runtimes.find(r => r.nodes.some(n => n.id === Y.node));
      path = __nscVueAdd(__nscVueAdd(__nscVueAdd('/infrastructure/runtimes/', enc(r?.id || 'missing')), '/nodes/'), enc(Y.node));
      q = {
        tab: Y.nodeTab,
        history: Y.nodeHistory ? '1' : '',
        q: Y.nodeSearch
      };
    } else if (ui.page === 'instance') {
      const t = W.IS(Y.instance);
      path = __nscVueAdd(__nscVueAdd(__nscVueAdd('/infrastructure/runtimes/', enc(t?.runtime || 'missing')), '/instances/'), enc(Y.instance));
      q = {
        tab: Y.instanceTab,
        kind: Y.instanceKind
      };
    } else if (ui.page === 'operation') {
      path = __nscVueAdd('/operations/', enc(Y.operation));
    } else if (ui.page === 'runtimes') path = '/infrastructure/runtimes';else if (ui.page === 'market') path = '/market';else if (ui.page === 'runtimeEvents') {
      path = '/infrastructure/events';
      q.filter = W.x.eventFilter;
    } else {
      path = __nscVueAdd(__nscVueAdd(root, '/'), enc(ui.page));
      if (ui.page === 'services') q = {
        scope: W.x.svcScope,
        layout: W.x.layout,
        runtime: W.x.svcRuntime === 'all' ? '' : W.x.svcRuntime,
        health: W.x.svcHealth === 'all' ? '' : W.x.svcHealth,
        q: W.x.svcSearch,
        page: W.x.svcPage > 1 ? W.x.svcPage : ''
      };
      if (ui.page === 'workloadHistory') q.tab = W.x.historyTab;
    }
    if (db.view !== 'cloud') q.perspective = db.view;
    return __nscVueAdd(path, query(q));
  }
  function setScope(p, e, d) {
    if (db.project !== p || db.env !== e || d && ui.domain !== d) return selectScope(p, e, d);
    return true;
  }
  function bad(path, reason) {
    Y.badPath = path;
    Y.routeError = reason;
    ui.page = 'notfound';
  }
  function parse(path) {
    const u = new URL(path, 'https://nsc.invalid');
    let a;
    try {
      a = u.pathname.split('/').filter(Boolean).map(decodeURIComponent);
    } catch (_) {
      bad(path, '链接编码无效。');
      return;
    }
    const q = u.searchParams;
    db.view = ['cloud', 'ops'].includes(q.get('perspective')) ? q.get('perspective') : 'cloud';
    ui.menu = false;
    ui.allEnvironments = false;
    ui.allProjects = false;
    const tab = (key, values, def) => values.includes(q.get(key)) ? q.get(key) : def;
    if (a[0] === 'projects') {
      if (a.length === 1) {
        ui.page = 'projects';
        return;
      }
      const globalScope = a[1] === 'all';
      if (globalScope && (a.length !== 5 || a[2] !== 'environments' || a[3] !== 'all' || !aggregatePages.includes(a[4]))) return bad(path, '全部项目视角仅用于资源列表，请选择具体项目和环境。');
      const p = globalScope ? P() || db.projects[0] : P(a[1]);
      if (!p) return bad(path, '项目不存在。');
      const aggregate = a[2] === 'environments' && a[3] === 'all';
      if (aggregate && (a.length !== 5 || !aggregatePages.includes(a[4]))) return bad(path, '所有环境视角仅用于资源列表，请进入具体环境查看或编辑。');
      const e = a[2] === 'environments' && a[3] && !aggregate ? E(a[3]) : aggregate && E(db.env)?.project === p.id ? E(db.env) : db.environments.find(e => e.project === p.id && e.kind === 'production') || db.environments.find(e => e.project === p.id);
      if (!e || e.project !== p.id) return bad(path, '项目与环境不匹配。');
      setScope(p.id, e.id);
      ui.allEnvironments = aggregate;
      ui.allProjects = globalScope;
      if (a.length === 2) {
        ui.page = 'project';
        return;
      }
      if (a[2] === 'variables' || a[4] === 'variables') {
        ui.page = 'variables';
        Y.layer = a[2] === 'variables' ? 'project' : tab('layer', ['environment', 'application'], 'environment');
        Y.app = q.get('app') || db.apps.find(a => a.project === p.id && a.env === e.id)?.id || '';
        if (Y.layer === 'application' && (!W.APP(Y.app) || W.APP(Y.app).project !== p.id || W.APP(Y.app).env !== e.id)) return bad(path, '应用变量不属于当前项目环境。');
        Y.query = q.get('q') || '';
        return;
      }
      if (a.length === 3 && a[2] === 'environments') {
        ui.page = 'environments';
        return;
      }
      if (a[2] !== 'environments') return bad(path, '无法识别项目路径。');
      if (a[4] === 'applications' && a[5]) {
        const app = W.APP(a[5]);
        if (!app || app.project !== p.id || app.env !== e.id) return bad(path, '应用不存在或不属于此环境。');
        W.x.app = app.id;
        if (a[6] === 'compose') {
          if (Y.docApp !== app.id) Y.loadDoc(app.id);
          ui.page = 'compose';
          Y.view = tab('view', ['source', 'resolved', 'runtime', 'diff'], 'source');
          Y.file = q.get('file') === 'runtime' ? 'runtime' : 'compose';
          Y.previewEnv = q.get('previewEnv') || app.env;
          if (E(Y.previewEnv)?.project !== p.id) return bad(path, '预览环境不属于当前项目。');
        } else if (!a[6]) {
          ui.page = 'application';
          W.x.appTab = tab('tab', ['overview', 'config', 'history', 'events'], 'overview');
        } else return bad(path, '无法识别应用页面。');
        return;
      }
      if (a[4] === 'services' && a[5]) {
        const s = W.ST(a[5]);
        if (!s || s.project !== p.id || s.env !== e.id) return bad(path, '服务不属于指定项目环境。');
        ui.page = 'service';
        W.x.service = s.id;
        W.x.serviceTab = tab('tab', ['instances', 'events', 'config', 'routes'], 'instances');
        W.x.includeHistory = q.get('history') === '1';
        return;
      }
      if (a[4] === 'domains' && a[5]) {
        const d = db.domains.find(d => d.id === a[5] && d.project === p.id && d.env === e.id);
        if (!d) return bad(path, '域名不属于指定项目环境。');
        setScope(p.id, e.id, d.id);
        ui.page = 'domain';
        ui.tab = tab('tab', ['overview', 'routes', 'config', 'tls', 'history'], 'overview');
        ui.configView = tab('editor', ['semantic', 'code'], 'semantic');
        ui.file = tab('file', ['generated', 'native', 'server', 'gateway', 'route'], 'generated');
        return;
      }
      const allowed = ['overview', 'applications', 'domains', 'routes', 'services', 'configs', 'certificates', 'workloadHistory', 'gateways', 'deployments', 'events', 'help'];
      if (!allowed.includes(a[4]) || a[5]) return bad(path, '无法识别环境页面。');
      ui.page = a[4];
      if (ui.page === 'services') {
        W.x.svcScope = globalScope ? 'all' : aggregate ? 'project' : tab('scope', ['environment', 'project', 'all'], 'environment');
        ui.allProjects = W.x.svcScope === 'all';
        ui.allEnvironments = ui.allProjects || W.x.svcScope === 'project';
        W.x.layout = tab('layout', ['grouped', 'flat'], 'grouped');
        W.x.svcRuntime = q.get('runtime') || 'all';
        if (W.x.svcRuntime !== 'all' && !W.RT(W.x.svcRuntime)) return bad(path, '运行位置筛选不存在。');
        W.x.svcHealth = tab('health', ['all', 'issue', 'external'], 'all');
        W.x.svcSearch = q.get('q') || '';
        W.x.svcPage = Math.max(1, Math.min(999, parseInt(q.get('page')) || 1));
      }
      if (ui.page === 'workloadHistory') W.x.historyTab = tab('tab', ['application', 'gateway'], 'application');
      return;
    }
    if (a[0] === 'services' && a.length === 2) {
      const s = W.ST(a[1]);
      if (!s || s.project) return bad(path, '此链接仅用于未归属项目的服务。');
      ui.page = 'service';
      W.x.service = s.id;
      W.x.serviceTab = tab('tab', ['instances', 'events', 'config', 'routes'], 'instances');
      W.x.includeHistory = q.get('history') === '1';
      return;
    }
    if (a[0] === 'market' && a.length === 1) {
      ui.page = 'market';
      return;
    }
    if (a[0] === 'operations' && a.length === 2) {
      const o = W.OP(a[1]);
      if (!o) return bad(path, '部署记录不存在。');
      setScope(o.project, o.env);
      Y.operation = o.id;
      ui.page = 'operation';
      return;
    }
    if (a[0] === 'infrastructure') {
      if (a[1] === 'events' && a.length === 2) {
        ui.page = 'runtimeEvents';
        W.x.eventFilter = q.get('filter') || 'all';
        return;
      }
      if (a[1] !== 'runtimes') return bad(path, '无法识别基础设施路径。');
      if (a.length === 2) {
        ui.page = 'runtimes';
        return;
      }
      const r = W.RT(a[2]);
      if (!r) return bad(path, '运行位置不存在。');
      W.x.runtime = r.id;
      if (a.length === 3) {
        ui.page = 'runtime';
        W.x.runtimeTab = tab('tab', ['overview', 'nodes', 'services', 'events'], 'overview');
        return;
      }
      if (a[3] === 'nodes' && a.length === 5) {
        const n = r.nodes.find(n => n.id === a[4]);
        if (!n) return bad(path, '节点不属于链接指定的集群。');
        Y.node = n.id;
        Y.nodeTab = tab('tab', r.type === 'swarm' ? ['services', 'tasks', 'containers', 'events'] : ['services', 'containers', 'events'], 'services');
        Y.nodeHistory = q.get('history') === '1';
        Y.nodeSearch = q.get('q') || '';
        ui.page = 'node';
        return;
      }
      if (a[3] === 'instances' && a.length === 5) {
        const t = W.IS(a[4]);
        if (!t || t.runtime !== r.id) return bad(path, '实例不属于链接指定的运行位置。');
        Y.instance = t.id;
        Y.instanceKind = t.kind === 'container' ? 'container' : tab('kind', ['task', 'container'], 'task');
        Y.instanceTab = tab('tab', ['overview', 'logs'], 'overview');
        ui.page = 'instance';
        return;
      }
    }
    bad(path, '没有匹配到任何页面。');
  }
  function isUnsaved() {
    return dirty() || Y.varsDirty() || Y.docsDirty() || Y.modalDirty() || W.x.wizard?.touched || modal?.kind === 'route' && !equal(routeForm, routeDraftSnapshot);
  }
  function editorKey(path) {
    try {
      const u = new URL(path, 'https://nsc.invalid');
      const p = u.pathname;
      if (p.includes('/variables')) return __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(p, '|'), u.searchParams.get('layer') || ''), '|'), u.searchParams.get('app') || '');
      if (p.endsWith('/compose') || /\/domains\/[^/]+$/.test(p)) return p;
      return null;
    } catch (_) {
      return null;
    }
  }
  function modalFields() {
    return [...document.querySelectorAll('#overlay input[id],#overlay textarea[id],#overlay select[id]')].map(el => ({
      id: el.id,
      value: el.value,
      checked: el.checked
    }));
  }
  function restoreFields(g) {
    for (const item of g?.fields || []) {
      const el = document.getElementById(item.id);
      if (el) {
        el.value = item.value;
        if (el.type === 'checkbox') el.checked = item.checked;
      }
    }
  }
  function linkFor(el) {
    const a = el.dataset.action,
      id = el.dataset.id;
    const pr = __nscVueAdd(__nscVueAdd(__nscVueAdd('/projects/', enc(listProject())), '/environments/'), enc(listEnvironment()));
    if (a === 'scope-all-projects') return projectHref('all');
    if (allProjectView() && a === 'nav') {
      const page = el.dataset.page;
      if (['project', 'environments'].includes(page)) return '/projects';
      if (page === 'variables') return null;
    }
    if (a === 'p-env') return environmentHref(id);
    if (a === 'x-node') {
      const r = db.runtimes.find(r => r.nodes.some(n => n.id === id));
      return r ? __nscVueInterpolate(["/infrastructure/runtimes/", "/nodes/", "?tab=services"], [enc(r.id), enc(id)]) : null;
    }
    if (a === 'x-service') {
      const s = W.ST(id);
      return s ? s.project ? __nscVueInterpolate(["/projects/", "/environments/", "/services/", "?tab=instances"], [enc(s.project), enc(s.env), enc(id)]) : __nscVueInterpolate(["/services/", ""], [enc(id)]) : null;
    }
    if (a === 'x-app' || a === 'y-open-compose') {
      const x = W.APP(id);
      return x ? __nscVueInterpolate(["/projects/", "/environments/", "/applications/", "", ""], [enc(x.project), enc(x.env), enc(id), a === 'y-open-compose' ? '/compose' : '']) : null;
    }
    if (a === 'y-instance' || a === 'x-instance') {
      const t = W.IS(id);
      return t ? __nscVueInterpolate(["/infrastructure/runtimes/", "/instances/", "", ""], [enc(t.runtime), enc(id), query({
        kind: el.dataset.kind || t.kind
      })]) : null;
    }
    if (a === 'open-domain') {
      const d = db.domains.find(d => d.id === id);
      return d ? __nscVueInterpolate(["/projects/", "/environments/", "/domains/", "?tab=", ""], [enc(d.project), enc(d.env), enc(id), el.dataset.tab || 'overview']) : null;
    }
    if (a === 'x-runtime') return __nscVueAdd('/infrastructure/runtimes/', enc(id));
    if (a === 'x-deployment') return __nscVueAdd('/operations/', enc(id));
    if (a === 'nav') {
      const page = el.dataset.page;
      if (page === 'projects') return '/projects';
      if (page === 'project') return __nscVueAdd('/projects/', enc(db.project));
      if (page === 'environments') return __nscVueAdd(__nscVueAdd('/projects/', enc(db.project)), '/environments');
      if (page === 'market') return '/market';
      if (page === 'runtimes') return '/infrastructure/runtimes';
      if (page === 'runtimeEvents') return '/infrastructure/events';
      if (page === 'variables') return __nscVueAdd(__nscVueAdd('/projects/', enc(db.project)), allEnvironmentView() ? '/variables' : __nscVueAdd(__nscVueAdd('/environments/', enc(db.env)), '/variables?layer=environment'));
      if (allEnvironmentView() && !aggregatePages.includes(page)) return environmentHref(db.env, page);
      return __nscVueAdd(__nscVueAdd(pr, '/'), enc(page));
    }
    return null;
  }
  function guard(next, cancel = () => {}) {
    if (!isUnsaved()) {
      next();
      return;
    }
    if (guardState) return;
    guardState = {
      next,
      cancel,
      savedModal: modal,
      edit: Y.modalEdit,
      fields: modalFields()
    };
    showModal({
      kind: 'y-navigation-guard',
      size: 'small',
      title: '保留未保存的修改？',
      sub: '切换页面或使用浏览器后退，不应丢弃当前输入。',
      body: callout('保存只保留配置草稿或变量，不会触发部署。也可以放弃本次修改，或留在当前页面。', 'blue'),
      footer: __nscVueAdd(__nscVueAdd(B('继续编辑', 'y-guard-stay'), B('放弃并离开', 'y-guard-discard', '', 'danger')), B('保存后离开', 'y-guard-save', 'save', 'primary'))
    });
  }
  ensureNavigate = guard;
  const baseClose = closeModal;
  closeModal = function (force = false) {
    if (!force && modal?.kind === 'y-navigation-guard') {
      const g = guardState;
      guardState = null;
      g?.cancel();
      modal = g?.savedModal || null;
      renderModal();
      restoreFields(g);
      return;
    }
    if (!force && Y.modalDirty()) {
      guard(() => baseClose(true));
      return;
    }
    return baseClose(force);
  };
  listen(window, 'click', e => {
    const el = e.target.closest('[data-action]');
    if (!el) return;
    const act = el.dataset.action;
    if (act?.startsWith('y-guard-')) {
      e.preventDefault();
      e.stopImmediatePropagation();
      const g = guardState;
      if (!g) return;
      if (act === 'y-guard-stay') {
        guardState = null;
        g.cancel();
        modal = g.savedModal;
        Y.modalEdit = g.edit;
        renderModal();
        restoreFields(g);
        return;
      }
      if (act === 'y-guard-save') {
        modal = g.savedModal;
        Y.modalEdit = g.edit;
        if (modal) {
          renderModal();
          restoreFields(g);
        }
        if (W.x.wizard?.touched) {
          toast('请先在部署向导中保存部署草稿。', true);
          guardState = null;
          return;
        }
        if (modal?.kind === 'route' && !equal(routeForm, routeDraftSnapshot)) {
          toast('请先将路由表单应用到草稿。', true);
          guardState = null;
          return;
        }
        if (!Y.saveExtra() || dirty() && !saveDraft(true)) {
          guardState = null;
          return;
        }
      }
      if (act === 'y-guard-discard') {
        Y.discardExtra();
        work = clone(workingSaved());
      }
      guardState = null;
      baseClose(true);
      g.next();
      return;
    }
    if (el.tagName === 'A') {
      if (e.ctrlKey || e.metaKey || e.shiftKey || e.altKey || e.button !== 0) {
        e.stopImmediatePropagation();
        return;
      }
      e.preventDefault();
      e.stopImmediatePropagation();
      hooks.navigate(el.getAttribute('href'));
    }
  }, true);
  updateHash = function () {
    if (!booting && !routeBusy) {
      const replace = Y.replaceNext;
      Y.replaceNext = false;
      hooks.syncRoute(routePath(), !!replace);
    }
  };
  parseHash = function () {};
  const renderView = render;
  render = function () {
    if (booting) return;
    renderView();
    updateHash();
  };
  listen(window, 'beforeunload', e => {
    if (isUnsaved()) {
      e.preventDefault();
      e.returnValue = '';
    }
  });
  const aggregatePages = ['applications', 'services', 'domains', 'routes', 'configs', 'certificates', 'workloadHistory', 'deployments', 'events', 'gateways'];
  function allEnvironmentView() {
    return (!!ui.allEnvironments || ui.allProjects) && aggregatePages.includes(ui.page) && !(ui.page === 'services' && W.x.svcScope === 'all' && !ui.allProjects);
  }
  function listEnvironment() {
    return allEnvironmentView() ? 'all' : db.env;
  }
  function inListScope(resource) {
    return allProjectView() || resource.project === db.project && (allEnvironmentView() || resource.env === db.env);
  }
  function listDomains() {
    return db.domains.filter(inListScope);
  }
  function environmentBadge(resource) {
    return allEnvironmentView() && resource ? __nscVueView(templates[453], [esc(resourceScopeName(resource))]) : '';
  }
  function environmentHref(env, page = ui.page) {
    if (allProjectView()) return projectHref('all', page);
    const aliases = {
      application: 'applications',
      compose: 'applications',
      service: 'services',
      domain: 'domains'
    };
    page = aliases[page] || page;
    if (!aggregatePages.includes(page)) page = env === 'all' ? 'applications' : 'overview';
    const q = page === 'services' ? {
      scope: env === 'all' ? 'project' : 'environment',
      layout: W.x.layout,
      runtime: W.x.svcRuntime === 'all' ? '' : W.x.svcRuntime,
      health: W.x.svcHealth === 'all' ? '' : W.x.svcHealth,
      q: W.x.svcSearch
    } : {};
    if (page === 'workloadHistory') q.tab = W.x.historyTab;
    if (db.view !== 'cloud') q.perspective = db.view;
    return __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('/projects/', enc(db.project)), '/environments/'), enc(env)), '/'), page), query(q));
  }
  function environmentAction(event) {
    const el = event.target.closest?.('[data-action]');
    if (!el || el.disabled) return;
    const action = allProjectView() && el.dataset.action === 'nav' && el.dataset.page === 'variables' ? 'variables' : el.dataset.action;
    if (action === 'scope-create-in-env') {
      event.preventDefault();
      event.stopImmediatePropagation();
      const target = E(el.dataset.id);
      if (!target || !P(target.project) || !allProjectView() && target.project !== db.project) return;
      const intent = modal?.scopeIntent;
      ensureNavigate(() => {
        hooks.navigate(concreteScopeHref(target, intent)).then(failure => {
          if (failure) return;
          if (intent === 'domain-add') addDomainModal();else if (intent === 'p-service-add') serviceModal();else if (intent !== 'variables') envModal(target.id);
        });
      });
      return;
    }
    if (!allEnvironmentView() || !['domain-add', 'p-service-add', 'p-env-settings', 'variables'].includes(action)) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    ensureNavigate(() => showModal({
      kind: 'scope-environment',
      scopeIntent: action,
      size: 'small',
      title: allProjectView() ? '选择项目与环境' : '选择具体环境',
      sub: '请选择本次操作所属的具体项目与环境。',
      body: __nscVueView(templates[455], [__nscVueJoin(listEnvironments().map(e => __nscVueView(templates[454], [e.id, I('layers'), esc(resourceScopeName({
        project: e.project,
        env: e.id
      })), esc(e.slug), I('right', 'sm')])), '')]),
      footer: B('取消', 'close')
    }));
  }
  function allProjectView() {
    return !!ui.allProjects && aggregatePages.includes(ui.page);
  }
  function listProject() {
    return allProjectView() ? 'all' : db.project;
  }
  function listEnvironments() {
    return allProjectView() ? db.environments : projectEnvs();
  }
  function resourceScopeName(resource) {
    const name = E(resource.env)?.name || resource.env || '未归属';
    return __nscVueAdd(allProjectView() ? __nscVueAdd(P(resource.project)?.name || resource.project || '未归属', ' / ') : '', name);
  }
  function scopeListPage(page) {
    const aliases = {
      application: 'applications',
      compose: 'applications',
      service: 'services',
      domain: 'domains'
    };
    page = aliases[page] || page;
    return aggregatePages.includes(page) ? page : 'applications';
  }
  function projectHref(project, page = ui.page) {
    page = scopeListPage(page);
    const q = page === 'services' ? {
      scope: project === 'all' ? 'all' : 'project',
      layout: W.x.layout,
      runtime: W.x.svcRuntime === 'all' ? '' : W.x.svcRuntime,
      health: W.x.svcHealth === 'all' ? '' : W.x.svcHealth,
      q: W.x.svcSearch
    } : {};
    if (page === 'workloadHistory') q.tab = W.x.historyTab;
    if (db.view !== 'cloud') q.perspective = db.view;
    return __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('/projects/', enc(project)), '/environments/all/'), page), query(q));
  }
  function concreteScopeHref(target, intent) {
    return __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('/projects/', enc(target.project)), '/environments/'), enc(target.id)), '/'), intent === 'variables' ? 'variables?layer=environment' : scopeListPage(ui.page)), db.view === 'ops' ? __nscVueAdd(intent === 'variables' ? '&' : '?', 'perspective=ops') : '');
  }
  function globalScopeContext() {
    return __nscVueView(templates[456], [I('folder'), badge(__nscVueAdd(db.projects.length, ' 个项目')), I('layers', 'sm'), db.environments.length]);
  }
  const scopedLinkFor = linkFor;
  linkFor = function (el) {
    const href = scopedLinkFor(el);
    if (!href || db.view !== 'ops') return href;
    const url = new URL(href, 'https://nsc.invalid');
    url.searchParams.set('perspective', 'ops');
    return __nscVueAdd(__nscVueAdd(url.pathname, url.search), url.hash);
  };
  booting = false;
  return {
    get pageTitle() {
      return PAGE_NAMES[ui.page] || 'NSC';
    },
    get state() {
      return db;
    },
    get ui() {
      return ui;
    },
    get workspace() {
      return Y;
    },
    get workload() {
      return W.x;
    },
    get modal() {
      return modal;
    },
    get storageWarning() {
      return storageWarning;
    },
    get dirty() {
      return isUnsaved();
    },
    acceptPerspective(path) {
      db.view = new URL(path, 'https://nsc.invalid').searchParams.get('perspective') === 'ops' ? 'ops' : 'cloud';
      renderView();
    },
    route: routePath,
    guard,
    link: attrs => linkFor({
      dataset: Object.fromEntries(Object.entries(attrs).filter(([k]) => k.startsWith('data-')).map(([k, v]) => [k.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase()), v]))
    }),
    sameEditor: (a, b) => {
      const x = editorKey(a),
        y = editorKey(b);
      return !!x && x === y;
    },
    acceptRoute(path) {
      routeBusy = true;
      const keep = this.sameEditor(routePath(), path);
      baseClose(true);
      if (!keep) Y.varDraft = null;
      parse(path);
      renderView();
      document.title = __nscVueAdd(PAGE_NAMES[ui.page] || 'NSC', ' · Nginx Service Composer');
      routeBusy = false;
    },
    view(name) {
      return {
        topbar,
        sidebar: projectSidebar,
        context: contextBar,
        page: () => ui.page === 'domain' ? domainPage() : globalPage(),
        hero: heroActions,
        draft: draftBadge
      }[name]?.();
    },
    refresh: render,
    compile: () => Y.compileApp(),
    effective: (p, e, a) => Y.effective(p, e, a),
    serviceState: id => W.serviceState(W.ST(id)),
    clusterSummary: id => W.clusterSummary(W.RT(id)),
    dispose() {
      disposers.forEach(fn => fn());
      if (deploymentTimer) clearInterval(deploymentTimer);
      document.body.style.overflow = '';
    }
  };
}
