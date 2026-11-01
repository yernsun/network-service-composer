import { serviceInstances as _serviceInstances, serviceInstanceSummary as _serviceInstanceSummary } from '../data/service-instances';
import { translate as _t, labels as _labels, translatedAttribute as _a, localizeModal as _modal, observedVersion as _observedVersion } from '../i18n';
import { serviceName as _serviceName, serviceSearch as _serviceSearch } from '../data/service-references';
import { knownStatusTone } from '../data/status-tone';
import { effectiveManagedNginxConfig } from '../data/nginx-defaults';
import * as __infrastructureViews from "../views/infrastructure/renderers.js";
import * as __variablesViews from "../views/variables/renderers.js";
import * as __applicationsViews from "../views/applications/renderers.js";
import * as __projectsViews from "../views/projects/renderers.js";
import * as __shellViews from "../views/shell/renderers.js";
import * as __domainsViews from "../views/domains/renderers.js";
import NSCEngine from '../mock/compiler.js';
import { validateCompose as validateComposeDocument } from '../compose/validator.js';
import { deploymentReady, composeSession, composeSource } from '../compose/session.js';
import { view as __nscVueView, add as __nscVueAdd, join as __nscVueJoin, interpolateView as __nscVueInterpolate, insertView, highlightLine, clearRegions, setRegion } from '../components/view';
import originalTemplates from '../views/blueprints.json';
import { deploymentBlueprints } from '../compose/blueprints.js';
import { releaseStatusView } from '../components/release-status.js';
import { cloneVNode, h, isVNode, reactive } from 'vue';
import { View, nodes } from '../components/view';
const templates = deploymentBlueprints(originalTemplates);
function replaceRouteServiceField(body, renderTarget) {
  let replaced = false;
  function walk(items) {
    return items.flatMap(item => {
      if (!isVNode(item)) return [item];
      if (!replaced && Array.isArray(item.children) && item.children.some(child => isVNode(child) && child.props?.id === 'rf-service')) {
        replaced = true;
        return nodes(renderTarget(item));
      }
      if (!Array.isArray(item.children)) return [item];
      const copy = cloneVNode(item);
      copy.children = walk(item.children);
      return [copy];
    });
  }
  return new View(walk(nodes(body)));
}
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
  const esc = (x) => String(x ?? '').replace(/[&<>"']/g, (c) => ({
    '&': '&amp;',
    '<': '&lt;',
    '>': '&gt;',
    '"': '&quot;',
    "'": '&#39;'
  })[c]);
  const clone = (x) => JSON.parse(JSON.stringify(x));
  const equal = (a, b) => JSON.stringify(a) === JSON.stringify(b);
  const KEY = 'network-service-composer-vue-v1.3';
  const ENV = {
    prod: 'Production',
    test: 'Testing',
    sandbox: 'Sandbox',
    dev: 'Development',
    'docs-prod': 'Production'
  };
  const MATCH = _labels({
    prefix: () => _t("archive.message_6fd076957136"),
    exact: () => _t("archive.message_391fe725d014"),
    prefer: () => _t("archive.message_006e6d973b8a")
  });
  const KINDS = _labels({
    proxy: () => _t("archive.message_7b3f1a698530"),
    return: () => _t("archive.message_2a595acdfbba"),
    redirect: () => _t("archive.message_f421626bbe48")
  });
  const stamp = () => new Date().toLocaleString('sv-SE', {
    timeZone: 'Asia/Shanghai'
  }).slice(0, 19);
  const uid = (p) => {
    if (p === 'route' && hooks.data.saveDomainDraft) {
      const bytes = crypto.getRandomValues(new Uint8Array(16));
      let stamp = BigInt(Date.now());
      for (let index = 5; index >= 0; index--) { bytes[index] = Number(stamp & 255n); stamp >>= 8n }
      bytes[6] = (bytes[6] & 15) | 112; bytes[8] = (bytes[8] & 63) | 128;
      const hex = [...bytes].map(value => value.toString(16).padStart(2, '0')).join('');
      return `route_${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
    }
    return __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(p, '-'), Date.now().toString(36)), '-'), Math.random().toString(36).slice(2, 6));
  };
  const badge = (text, type = '', dot = false) => {
    const tone = knownStatusTone(text, type);
    return __nscVueView(templates[51], [tone, dot ? __nscVueView(templates[0], [tone === 'amber' ? 'amber' : tone === 'red' ? 'red' : tone === '' ? 'gray' : '']) : '', esc(text)]);
  };
  const B = (text, action, icon = '', cls = '', attrs = '') => __nscVueView(templates[52], [cls, action, attrs, icon ? I(icon) : '', text]);
  const TB = (text, action, icon = '', attrs = '') => __nscVueView(templates[53], [action, attrs, text, icon ? I(icon, 'sm') : '']);
  const callout = (text, type = 'blue', icon = 'info') => __nscVueView(templates[54], [type, I(icon), text]);
  const cardHead = (title, sub = '', action = '', icon = '') => __nscVueView(templates[56], [icon ? I(icon) : '', title, sub ? __nscVueView(templates[55], [sub]) : '', action]);
  const route = (id, path, service, extra = {}) => ({
    id,
    name: service ? __nscVueAdd(service, ' route') : _t("archive.message_da7d17db5d64"),
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
    return hooks.data.state;
  }
  let storageAvailable = false,
    storageWarning = '',
    db;
  db = hooks.data.state;
  let ui = {
    page: 'project',
    domain: null,
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
  let work = clone((() => { const domain = db.domains.find((d) => d.project === db.project && d.env === db.env); return domain?.draft?.config || domain?.active || emptyConfig() })()),
    modal = null,
    routeForm = null,
    routeTargetMode = 'service',
    routeDraftSnapshot = null,
    deploymentTimer = null,
    focusBeforeModal = null;
  let routeEditorError = '';
  function D() {
    return db.domains.find((d) => d.id === ui.domain) || db.domains[0];
  }
  function S(id) {
    return db.services.find((s) => s.id === id);
  }
  function G(id) {
    return db.gateways.find((g) => g.id === id);
  }
  function ready(id) {
    const s = S(id);
    return s ? s.endpoints.filter((e) => e[1]) : [];
  }
  function offline(gid) {
    return db.gateways.find((g) => g.id === gid)?.available !== true;
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
    return;
  }
  function event(title, detail = '', type = 'info', domain = ui.domain) {
    return;
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
    if (saved) saved.textContent = dirty() ? _t("compose.unsaved") : D().draft ? _t("archive.message_4e3a54fefb1a") : _t("archive.message_86a803a4a946");
  }
  function saveDraft(silent = false) {
    return hooks.data.unsupported("saveDraft", formError);
  }
  function ensureNavigate(fn) {
    if (deploymentTimer) {
      toast(_t("archive.message_7861c015d0ca"), true);
      return;
    }
    if (!dirty()) {
      fn();
      return;
    }
    showModal(_modal(() => ({
      title: _t("archive.message_fa3084b385bb"),
      sub: __nscVueAdd(D().name, _t("archive.message_058e1acd0747")),
      body: callout(_t("archive.message_d4f24988b59f"), 'neutral'),
      footer: __nscVueAdd(__nscVueAdd(B(_t("dirty.continue"), 'close', '', 'ghost'), B(_t("dirty.discard"), 'discard-leave')), B(_t("archive.message_8050cd151ef0"), 'save-leave', 'save', 'primary')),
      size: 'small',
      next: fn
    })));
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
      const d = db.domains.find((d) => d.name === decodeURIComponent(parts[1] || ''));
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
    const known = d.gateways.filter((id) => d.rev > 0 && !offline(id) && d.loaded[id] === d.rev && !d.drift?.gateway?.includes(id));
    return {
      known: known.length,
      total: d.gateways.length,
      mixed: d.gateways.some((id) => d.loaded[id] !== undefined && d.loaded[id] !== d.rev),
      unknown: d.gateways.length === 0 || d.gateways.some((id) => offline(id) || d.loaded[id] === undefined)
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
    return __nscVueView(templates[62], [I('menu'), I('search', 'sm'), I('help'), I('bell'), ui.menu ? 'open' : '', db.env === 'prod' ? '' : db.env === 'staging' ? 'amber' : 'gray', __nscVueJoin(Object.entries(ENV).map(([v, n]) => __nscVueView(templates[61], [v, v === db.env ? 'selected' : '', n])), ''), I('chevron', 'sm'), navigation(), I('play', 'sm'), badge('OFFLINE'), TB(_t("archive.message_cdd6630b67b5"), 'scenarios', 'right'), I('help'), I('external', 'sm'), storageWarning ? callout(esc(storageWarning), 'amber') : '', ui.externalConflict ? callout(__nscVueAdd(__nscVueAdd(__nscVueAdd(_t("archive.message_20450f89240a"), TB(_t("archive.exportCurrent"), 'export')), ' · '), TB(_t("archive.reloadState"), 'reload-state')), 'red') : '', ui.page === 'domain' ? domainPage() : globalPage()]);
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
    for (const [key, label] of [['mode', _t("archive.message_6d68531608c6")], ['tls', 'HTTPS'], ['redirect', _t("archive.message_09ee65c32a3e")], ['body', _t("archive.message_f01cfe41b966")], ['serverSnippet', _t("archive.message_50d5ad08db2d")], ['native', _t("archive.message_6a6f77237f46")]]) if ((b.mode !== 'native' || ['mode', 'native'].includes(key)) && !equal(a[key], b[key])) out.push({
      title: label,
      detail: key === 'serverSnippet' ? _t("archive.message_38ddb79ec02f") : key === 'native' ? _t("archive.message_5472f91442b0") : _t("archive.message_0501b3d6a0fb"),
      old: typeof a[key] === 'boolean' ? a[key] ? _t("archive.message_8da97ddda990") : _t("console.close") : ['native', 'serverSnippet'].includes(key) ? _t("archive.message_a3e28df7f82f") : a[key],
      new: typeof b[key] === 'boolean' ? b[key] ? _t("archive.message_8da97ddda990") : _t("console.close") : ['native', 'serverSnippet'].includes(key) ? _t("archive.message_bf7a7211f538") : b[key]
    });
    if (b.mode === 'native') return out;
    const map = new Map(a.routes.map((r) => [r.id, r]));
    for (const r of b.routes) {
      const old = map.get(r.id);
      if (!old) {
        out.push({
          title: __nscVueAdd(_t("archive.message_d763c8f36471"), r.path),
          detail: __nscVueAdd(__nscVueAdd(KINDS[r.kind], ' → '), _serviceName(db.services, r.service) || r.value),
          old: _t("archive.message_80a57e03f071"),
          new: _t("variables.add")
        });
        continue;
      }
      map.delete(r.id);
      const fields = {
        path: _t("archive.message_f92b5d1f891e"),
        match: _t("archive.message_e1f0f1cb1ab4"),
        kind: _t("archive.message_9101b3c545e4"),
        service: _t("archive.message_a2571deed069"),
        timeout: _t("archive.message_8fb313c01c48"),
        connect: _t("archive.message_ae5ead4d8907"),
        strip: _t("archive.message_ba97b65148a2"),
        websocket: 'WebSocket',
        enabled: _t("archive.message_d334c402ead1"),
        snippet: _t("archive.message_79bd97ec2617"),
        code: _t("archive.message_e4b4140a0311"),
        value: _t("archive.message_28ad983da52d")
      };
      for (const [k, label] of Object.entries(fields)) if (!equal(old[k], r[k])) out.push({
        title: __nscVueAdd(__nscVueAdd(r.path, ' · '), label),
        detail: k === 'timeout' ? 'proxy_read_timeout' : k === 'connect' ? 'proxy_connect_timeout' : _t("archive.message_0caf5f0bea2e"),
        old: k === 'service' ? _serviceName(db.services, old[k]) : k === 'snippet' ? _t("archive.message_6d1b52bd27c8") : k === 'timeout' || k === 'connect' ? __nscVueAdd(old[k], 's') : typeof old[k] === 'boolean' ? old[k] ? _t("archive.message_8da97ddda990") : _t("console.close") : old[k],
        new: k === 'service' ? _serviceName(db.services, r[k]) : k === 'snippet' ? _t("archive.message_3c763dcb97e1") : k === 'timeout' || k === 'connect' ? __nscVueAdd(r[k], 's') : typeof r[k] === 'boolean' ? r[k] ? _t("archive.message_8da97ddda990") : _t("console.close") : r[k]
      });
    }
    for (const old of map.values()) out.push({
      title: __nscVueAdd(_t("archive.message_b5e1bb61b32e"), old.path),
      detail: _t("archive.message_789ade4acfbc"),
      old: _t("archive.message_de8184da1ef8"),
      new: _t("console.delete")
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
    return hooks.data.command("runDiagnostic", arguments);
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
    const rs = c.routes.filter((r) => r.enabled),
      svcs = [...new Set(rs.filter((r) => r.kind === 'proxy').map((r) => r.service))],
      defaults = effectiveManagedNginxConfig(c);
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
    if (defaults.bodyFallback) lines.push('    # NSC default applied: client_max_body_size was unset or invalid.');
    lines.push(__nscVueAdd(__nscVueAdd('    client_max_body_size ', defaults.body), ';'), '');
    if (c.serverSnippet.trim()) lines.push(...c.serverSnippet.split('\n').map((l) => __nscVueAdd('    ', l)), '');
    for (const r of rs) {
      lines.push(__nscVueInterpolate(["    # Route: ", " \u2192 ", ""], [r.id, r.kind === 'proxy' ? r.service : r.kind]));
      const mod = r.match === 'exact' ? '= ' : r.match === 'prefer' ? '^~ ' : '';
      lines.push(__nscVueInterpolate(["    location ", "", " {"], [mod, r.path]));
      if (r.kind === 'proxy') {
        const routeDefaults = defaults.route(r);
        lines.push(__nscVueInterpolate(["        proxy_pass http://", "", ";"], [upstreamName(r.service, d), r.strip ? '/' : '']), '        proxy_http_version 1.1;', '        proxy_set_header Host $host;', '        proxy_set_header X-Real-IP $remote_addr;', '        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;', '        proxy_set_header X-Forwarded-Proto $scheme;');
        if (routeDefaults.connectFallback) lines.push('        # NSC default applied: proxy_connect_timeout was unset or invalid.');
        lines.push(__nscVueInterpolate(["        proxy_connect_timeout ", "s;"], [routeDefaults.connect]));
        if (routeDefaults.readFallback) lines.push('        # NSC default applied: proxy_read_timeout was unset or invalid.');
        lines.push(__nscVueInterpolate(["        proxy_read_timeout ", "s;"], [routeDefaults.read]));
        if (r.websocket) lines.push('        proxy_set_header Upgrade $http_upgrade;', '        proxy_set_header Connection "upgrade";');else lines.push('        proxy_set_header Connection "";');
      } else lines.push(__nscVueInterpolate(["        return ", " ", ";"], [r.code, nginxQuote(r.value)]));
      if (r.snippet.trim()) lines.push('        # User-managed Route extension', ...r.snippet.split('\n').map((l) => __nscVueAdd('        ', l)));
      lines.push('    }', '');
    }
    if (!rs.some((r) => r.path === '/' && r.match !== 'exact')) lines.push('    location / { return 404; }');
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
    return hooks.data.domainHistoryPage ? hooks.data.domainHistoryPage(D()) : __shellViews.historyPage_20(__renderContext0, ...args);
  }
  const PAGE_NAMES = _labels({
    overview: () => _t("archive.message_ae674e5f7d35"),
    domains: 'Domains',
    routes: 'Routes',
    services: 'Services',
    gateways: 'Gateways',
    certificates: () => _t("archive.message_8b26d9ddad5b"),
    configs: () => _t("archive.message_845e79578dbc"),
    deployments: () => _t("archive.message_e2901f5a3da5"),
    events: () => _t("archive.message_f98dfe0b4d54")
  });
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
  function setPerspective(view) {
    db.view = view === 'ops' ? 'ops' : 'cloud';
    if (!ui.externalConflict) persist();
    ui.menu = false;
    render();
    toast(db.view === 'cloud' ? _t("archive.message_f26b380e2b4a") : _t("archive.message_988a62d2c98f"));
  }
  function aboutModal() {
    showModal(_modal(() => ({
      size: 'small',
      title: 'Network Service Composer for Nginx',
      sub: _t("archive.message_d6eb52fe16e7"),
      body: __nscVueView(templates[177], [callout(_t("archive.message_93632182391d"), 'neutral')]),
      footer: B(_t("console.close"), 'close')
    })));
  }
  let preserveWizardFocus = false;
  function showModal(spec) {
    if (!modal) focusBeforeModal = document.activeElement;
    modal = spec;
    renderModal();
  }
  function renderModal(focus = true) {
    if (booting) return;
    document.body.style.overflow = modal ? 'hidden' : '';
    hooks.render();
    if (focus && modal && !preserveWizardFocus) {
      const focusedModal = modal;
      setTimeout(() => {
        if (modal !== focusedModal) return;
        const host = document.getElementById('overlay');
        if (host?.contains(document.activeElement)) return;
        const el = host?.querySelector('[autofocus],input:not([type="checkbox"]):not(:disabled),textarea:not(:disabled),select:not(:disabled)') || host?.querySelector('button:not(:disabled)');
        el?.focus();
      }, 30);
    }
  }
  function closeModal(force = false) {
    if (modal?.locked && !force) return;
    if (modal?.kind === 'route' && !force && routeDraftSnapshot && !equal(routeForm, routeDraftSnapshot)) {
      showModal(_modal(() => ({
        title: _t("archive.message_39b5b2a964cf"),
        sub: _t("archive.message_369fac6b1104"),
        body: callout(_t("archive.message_e4bf2a111df2"), 'amber'),
        footer: __nscVueAdd(B(_t("dirty.continue"), 'route-resume'), B(_t("archive.message_b683e0ae6876"), 'route-discard', '', 'danger')),
        size: 'small',
        kind: 'route-exit'
      })));
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
      toast(_t("archive.message_e31d2cb940e0"), true);
      navigate('domain', 'config', ui.domain, false);
      ui.file = 'native';
      render();
      return;
    }
    const r = work.routes.find((r) => r.id === id);
    routeForm = r ? clone(r) : route(uid('route'), '/new/', db.services.find((s) => s.status !== 'REMOVED' && s.env === D().env && s.project === D().project)?.id || '', {
      name: _t("archive.message_821c27a22d90")
    });
    routeTargetMode = routeForm.externalUrl ? 'custom' : 'service';
    routeDraftSnapshot = clone(routeForm);
    showRouteForm();
  }
  function showRouteForm(error = '') {
    routeEditorError = error;
    const r = routeForm;
    ui.selectedRoute = r.id;
    const upstreamPath = r.strip ? '/users' : __nscVueAdd(r.path.replace(/\/$/, ''), '/users');
    showModal(_modal(() => ({
      kind: 'route',
      size: 'drawer',
      title: work.routes.some((x) => x.id === r.id) ? _t("archive.message_1985fb7f06b9") : _t("archive.message_d3aef56cde5a"),
      sub: __nscVueAdd(esc(D().name), _t("archive.message_dcb686045234")),
      body: __nscVueView(templates[142], [esc(r.path), __nscVueJoin(Object.entries(MATCH).map(([v, t]) => __nscVueView(templates[135], [v, v === r.match ? 'selected' : '', t, v === 'prefix' ? _t("archive.message_4b399ad51401") : ''])), ''), __nscVueJoin([['proxy', 'box'], ['return', 'checkcircle'], ['redirect', 'external']].map(([k, ic]) => __nscVueView(templates[136], [r.kind === k ? 'selected' : '', k, I(ic), KINDS[k]])), ''), r.kind === 'proxy' ? __nscVueView(templates[138], [__nscVueJoin(db.services.filter((s) => s.status !== 'REMOVED' && s.env === D().env && s.project === D().project).map((s) => __nscVueView(templates[137], [s.id, s.id === r.service ? 'selected' : '', esc(_serviceName(db.services, s.id)), s.port, ready(s.id).length])), ''), r.timeout, r.connect, r.strip ? 'on' : '', r.strip, r.websocket ? 'on' : '', r.websocket, esc(r.path), esc(upstreamPath)]) : __nscVueView(templates[140], [__nscVueJoin((r.kind === 'return' ? [200, 204, 403, 404, 503] : [301, 302, 307, 308]).map((n) => __nscVueView(templates[139], [n, Number(r.code) === n ? 'selected' : '', n])), ''), r.kind === 'redirect' ? _t("archive.message_6ee2b0cd4724") : _t("archive.message_e4c5dead4d24"), esc(r.value), r.kind === 'redirect' ? 'https://example.com/' : 'ok']), r.enabled ? 'on' : '', r.enabled, callout(_t("archive.message_aebb4cb52171"), 'neutral'), error ? __nscVueView(templates[141], [esc(error)]) : '']),
      footer: __nscVueView(templates[3], [work.routes.some((x) => x.id === r.id) ? B(_t("archive.message_e3ee7318f5a0"), 'route-delete', 'trash', 'danger sm') : '', B(_t("console.cancel"), 'close', '', 'ghost'), B(_t("archive.message_2354c137ceb3"), 'route-apply', 'check', 'primary')])
    })));
  }
  function checkRoute(r, exceptId = r.id) {
    if (!/^\/[a-zA-Z0-9_./~!@+\-]*$/.test(r.path) || r.path.includes('//') || r.path.includes('/../')) return _t("archive.message_8c847524546c");
    if (!Object.hasOwn(MATCH, r.match)) return _t("archive.message_1737781a351b");
    if (work.routes.some((x) => x.id !== exceptId && x.enabled && r.enabled && x.path === r.path && x.match === 'exact' === (r.match === 'exact'))) return _t("archive.message_a478564214fc");
    if (r.kind === 'proxy') {
      const s = S(r.service);
      if (!s || s.env !== D().env) return _t("archive.message_179e609175eb");
      if (!Number.isInteger(r.timeout) || r.timeout < 1 || r.timeout > 3600) return _t("archive.message_dc7c4264fd14");
      if (!Number.isInteger(r.connect) || r.connect < 1 || r.connect > 300) return _t("archive.message_014648cb4fec");
      if (r.strip && !r.path.endsWith('/')) return _t("archive.message_47eada3bf42d");
      if (r.strip && r.match === 'exact') return _t("archive.message_d2c96dba2ac1");
    }
    if (r.kind === 'redirect' && !/^https?:\/\/[^\s"{};]+$/.test(r.value)) return _t("archive.message_1e6942fac060");
    if (r.kind === 'return' && r.value.includes('$')) return _t("archive.message_34903d80f7ad");
    return '';
  }
  function applyRoute() {
    const err = checkRoute(routeForm);
    if (err) return showRouteForm(err);
    const i = work.routes.findIndex((x) => x.id === routeForm.id);
    if (i >= 0) work.routes[i] = clone(routeForm);else work.routes.push(clone(routeForm));
    closeModal(true);
    invalidate();
    render();
    toast(_t("archive.message_f419d8f4cdec"));
  }
  function requestRouteDelete() {
    showModal(_modal(() => ({
      kind: 'route-delete',
      size: 'small',
      title: _t("archive.message_f5383ad6795f"),
      sub: esc(routeForm.path),
      body: callout(_t("archive.message_e1497fa166b7"), 'amber'),
      footer: __nscVueAdd(B(_t("archive.message_16b38790d59b"), 'route-resume'), B(_t("archive.message_a3ea3c17b401"), 'route-delete-confirm', 'trash', 'danger'))
    })));
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
          issues.push(_t("archive.message_60afc9f14ee2"));
          depth = 0;
        }
      }
    }
    if (quote) issues.push(_t("archive.message_0366ef7bae41"));
    if (depth) issues.push(_t("archive.message_5d9c30f9f206"));
    return issues;
  }
  function validateConfig() {
    const errors = [],
      warnings = [],
      d = D();
    if (stale()) errors.push(_t("archive.message_9a46d9397736"));
    if (d.drift && !d.drift.reviewed) errors.push(_t("archive.message_40003545c32e"));
    if (d.gateways.some(offline)) errors.push(_t("archive.message_dcabdefbc37d"));
    if (d.drift?.reviewed) warnings.push(_t("archive.message_d2091f0d9aca"));
    if (work.mode === 'managed') {
      if (!/^\d+[kKmM]?$/.test(work.body)) errors.push(_t("archive.message_d146226accd1"));
      if (work.tls && d.rev === 0 && !d.certReadyDemo) errors.push(_t("archive.message_c7b2b732295f"));
      for (const r of work.routes) {
        const err = checkRoute(r);
        if (err) errors.push(__nscVueAdd(__nscVueAdd(r.path, '：'), err));
        if (r.enabled && r.kind === 'proxy' && !ready(r.service).length) errors.push(__nscVueAdd(r.path, _t("archive.message_45b3e1d25ed6")));
        if (r.enabled && r.match === 'prefix' && r.path !== '/' && !r.path.endsWith('/')) warnings.push(__nscVueAdd(r.path, _t("archive.message_e5521e4e93ff")));
        if (r.enabled && r.kind === 'return' && r.code === 204 && r.value) warnings.push(__nscVueAdd(r.path, _t("archive.message_5705dd9225fd")));
      }
      const snippets = [[_t("archive.message_50d5ad08db2d"), work.serverSnippet], ...work.routes.map((r) => [__nscVueAdd(r.path, _t("archive.message_8667315d2d07")), r.snippet])];
      for (const [label, text] of snippets) {
        const clean = text.replace(/#.*$/gm, '').trim();
        if (!clean) continue;
        const protectedDirective = /(^|;)\s*(listen|server_name|location|server|upstream|ssl_certificate(?:_key)?|proxy_pass|proxy_read_timeout|proxy_connect_timeout|client_max_body_size|proxy_set_header)\b/m;
        if (protectedDirective.test(clean)) errors.push(__nscVueAdd(label, _t("archive.message_c176bdffbd06")));
        if (/[{}]/.test(clean)) errors.push(__nscVueAdd(label, _t("archive.message_93068d11cc60")));
        if (clean.split('\n').some((l) => l.trim() && !l.trim().endsWith(';'))) errors.push(__nscVueAdd(label, _t("archive.message_4c54a8adb9b0")));
        const known = ['add_header', 'gzip', 'gzip_types', 'expires', 'proxy_buffering', 'proxy_buffers', 'proxy_buffer_size', 'client_body_timeout', 'keepalive_timeout', 'client_header_timeout'];
        for (const l of clean.split('\n')) {
          const directive = l.trim().split(/\s+/)[0];
          if (directive && !known.includes(directive)) warnings.push(__nscVueAdd(__nscVueAdd(__nscVueAdd(label, _t("archive.message_2062243996da")), directive), _t("archive.message_00c986219662")));
        }
        for (const e of tokenCheck(text)) errors.push(__nscVueAdd(__nscVueAdd(label, '：'), e));
      }
    } else {
      if (!work.native.trim()) errors.push(_t("archive.message_1c408037e54a"));
      for (const e of tokenCheck(work.native)) errors.push(__nscVueAdd(_t("archive.message_bcb107bdeb85"), e));
      warnings.push(_t("archive.message_228bbf76b4d5"));
    }
    const hash = signature();
    ui.validation = {
      errors: [...new Set(errors)],
      warnings: [...new Set(warnings)],
      hash,
      time: stamp()
    };
    event(_t("archive.message_d80c729e5659"), errors.length ? __nscVueAdd(errors.length, _t("archive.message_108874cef610")) : _t("archive.message_28f05b5191ff"), errors.length ? 'warning' : 'check');
    return ui.validation;
  }
  function validationModal() {
    const result = validateConfig();
    const domain = D();
    const signatureAtCheck = signature();
    const withoutSnippets = config => ({ ...config, serverSnippet: '', routes: config.routes.map(route => ({ ...route, snippet: '' })) });
    const serverEligible = work.mode === 'managed' && domain.active.mode === 'managed' && equal(withoutSnippets(work), withoutSnippets(domain.active)) && typeof hooks.data.previewDomainConfig === 'function';
    const candidate = serverEligible ? { poolID: domain.pool_id, domainID: domain.id, serverSnippet: work.serverSnippet, routes: work.routes.map(route => ({ id: route.id, snippet: route.snippet })) } : null;
    let serverStatus = serverEligible ? 'pending' : 'skipped';
    let serverLine = null;
    const spec = () => _modal(() => ({
      kind: 'validation',
      title: result.errors.length || serverStatus === 'failed' ? _t('archive.message_b9efd15cf931') : _t('archive.message_d00bfbb98e27'),
      sub: __nscVueAdd(__nscVueAdd(_t('archive.message_aa52ff6b83a2'), result.time), ' UTC+8'),
      body: __nscVueAdd(__nscVueAdd(callout(_t('domainValidation.localScope'), 'blue'), __nscVueView(templates[144], [
        __nscVueJoin(result.errors.map(issue => validationItem(issue, _t('archive.message_537d3f74530f'), 'red', 'warning')), ''),
        result.errors.length ? '' : validationItem(_t('domainValidation.localPassed'), _t('domainValidation.localScope'), 'green', 'checkcircle'),
        __nscVueJoin(result.warnings.map(issue => validationItem(issue, _t('archive.message_e7a95264e105'), 'amber', 'warning')), ''),
        validationItem(_t(`domainValidation.server${serverStatus}`), serverLine ? _t('domainValidation.serverLine', { v0: serverLine }) : _t(serverStatus === 'skipped' ? 'domainValidation.localScope' : 'domainValidation.serverScope'), serverStatus === 'failed' ? 'red' : serverStatus === 'passed' ? 'green' : 'muted', serverStatus === 'failed' ? 'warning' : serverStatus === 'passed' ? 'checkcircle' : 'info'),
      ])), callout(_t('domainValidation.noNginxTest'), 'neutral')),
      footer: B(_t('archive.message_16b38790d59b'), 'close'),
    }));
    showModal(spec());
    refreshHeader();
    if (!candidate) return;
    void hooks.data.previewDomainConfig(candidate).then(() => {
      if (modal?.kind !== 'validation' || signature() !== signatureAtCheck || D().id !== domain.id) return;
      serverStatus = 'passed';
      const updated = spec(); modal.title = updated.title; modal.body = updated.body; renderModal(false);
    }).catch(error => {
      if (modal?.kind !== 'validation' || signature() !== signatureAtCheck || D().id !== domain.id) return;
      serverStatus = 'failed'; serverLine = Number(error?.params?.line) || null;
      const updated = spec(); modal.title = updated.title; modal.body = updated.body; renderModal(false);
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
      toast(_t("archive.message_efe8e034c9b7"));
      return;
    }
    showModal(_modal(() => ({
      kind: publish ? 'publish-confirm' : 'impact',
      size: 'wide',
      title: publish ? _t("archive.message_8c878e1a3643") : _t("archive.message_cfb9a3192589"),
      sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(esc(D().name), _t("archive.message_f1c9b3bfd4a6")), D().draft?.base ?? D().rev), _t("archive.message_98d786712000")),
      body: __nscVueView(templates[151], [changes.length, D().gateways.length, work.mode === 'native' ? _t("community.evidence_state.UNKNOWN") : new Set(work.routes.filter((r) => r.kind === 'proxy').map((r) => r.service)).size, work.mode === 'native' ? _t("archive.message_5ba36016616c") : _t("archive.message_cfc5f24eabff"), callout(publish ? __nscVueView(templates[148], []) : _t("archive.message_a1f62433fdca"), publish ? 'amber' : 'blue'), changesHTML(changes, 12), __nscVueJoin(D().gateways.map((id) => __nscVueView(templates[149], [I('server'), G(id).name, G(id).ip, ENV[G(id).env], badge(offline(id) ? _t("community.evidence_state.UNKNOWN") : _t("archive.message_c25b6b145491"), offline(id) ? 'amber' : 'blue')])), ''), callout(work.mode === 'native' ? _t("archive.message_d781821e8ea3") : _t("archive.message_da38c691a60e"), 'neutral'), diffHTML(renderConfig(D().active), renderConfig(work)), publish ? __nscVueView(templates[150], [esc(changes[0]?.title || _t("archive.message_b4a109eeb906")), D().gateways.length]) : '']),
      footer: __nscVueAdd(B(_t("archive.message_16b38790d59b"), 'close', '', 'ghost'), publish ? B(_t("archive.message_8a064331d158"), 'release-start', 'send', 'primary', 'disabled id="confirm-publish"') : B(valid ? _t("archive.message_9d0ba94ffa78") : _t("archive.message_c78eb633d2f8"), valid ? 'publish' : 'validate', valid ? 'send' : 'checkcircle', 'primary', changed() ? '' : 'disabled'))
    })));
  }
  function startRelease() {
    return hooks.data.unsupported("startRelease", formError);
  }
  function simulateDeployment(dep, retry) {
    return hooks.data.unsupported("simulateDeployment", formError);
  }
  function showDeployment(dep, stage = 4, running = false) {
    showModal(_modal(() => ({
      kind: 'deployment',
      deployment: dep.id,
      locked: running,
      size: 'wide',
      title: running ? __nscVueAdd(_t("archive.message_78608d0cb149"), dep.version) : dep.status === 'success' ? _t("archive.message_666b257df641") : _t("archive.message_9052f697bf03"),
      sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(dep.id, _t("archive.message_665a2416c548")), dep.version), ' · '), esc(db.domains.find((d) => d.id === dep.domain).name)),
      body: __nscVueAdd(callout(_t("archive.message_10f2772f848d"), 'neutral'), __nscVueView(templates[154], [__nscVueJoin([_t("archive.message_7790a12e3d3f"), _t("archive.message_bcc2cb7ee3cb"), _t("archive.message_670f0a1d31c0"), _t("archive.message_c2cc2ea751db")].map((t, i) => step(String(__nscVueAdd(i, 1)), t, i < stage ? 'done' : i === stage ? 'now' : '')), ''), running ? Math.max(8, stage * 27) : 100, __nscVueJoin(dep.targets.map((id) => __nscVueView(templates[152], [I('server'), G(id).name, dep.version, G(id).ip, badge(dep.results[id] === 'success' ? __nscVueAdd(_t("archive.message_ace37b2334d3"), dep.version) : dep.results[id] === 'failed' ? _t("archive.message_27c7e73eb4f3") : _t("archive.message_966984a19a2f"), dep.results[id] === 'success' ? 'green' : dep.results[id] === 'failed' ? 'red' : 'blue')])), ''), esc(__nscVueJoin(dep.logs, '\n')), !running && dep.status === 'partial' ? __nscVueView(templates[153], [callout(_t("archive.message_e7ef0ab7c035"), 'amber')]) : ''])),
      footer: running ? __nscVueView(templates[155], []) : __nscVueAdd(B(_t("console.close"), 'close'), dep.status === 'partial' ? __nscVueAdd(B(_t("archive.message_6e97dc373b0e"), 'restore-deployment', 'history', '', __nscVueInterpolate(["data-id=\"", "\""], [dep.id])), B(_t("archive.message_a70b925af371"), 'retry-deployment', 'refresh', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [dep.id]))) : B(_t("archive.message_9b3ecc04098b"), 'go-ops', 'server', 'primary', __nscVueInterpolate(["data-domain=\"", "\""], [dep.domain])))
    })));
  }
  function retryDeployment(id) {
    return hooks.data.unsupported("retryDeployment", formError);
  }
  function serviceDetail(id) {
    const s = S(id);
    if (!s) {
      toast(_t("archive.message_41e44f0efe37"));
      return;
    }
    const refs = db.domains.flatMap((d) => (d.draft?.config || d.active).routes.filter((r) => r.service === id).map((r) => ({
      d,
      r
    })));
    showModal(_modal(() => ({
      size: 'drawer',
      title: esc(_serviceName(db.services, s.id)),
      sub: _t("archive.message_029c82091a96"),
      body: __nscVueAdd(callout(_t("archive.message_30ae33e00c49"), 'neutral'), __nscVueView(templates[158], [ENV[s.env], s.runtime, esc(s.image), s.port, I('layers'), __nscVueJoin(s.endpoints.map(([ip, ok]) => __nscVueView(templates[156], [ip, s.port, badge(ok ? 'Ready' : 'Not Ready', ok ? 'green' : 'amber'), ok ? _t("archive.message_451755e3831f") : _t("archive.message_ac83d3972fe9")])), ''), I('route'), __nscVueJoin(refs.map(({
        d,
        r
      }) => __nscVueView(templates[157], [esc(d.name), esc(r.path), MATCH[r.match], TB(_t("archive.message_a38c17169aed"), 'resource-route', 'right', __nscVueInterpolate(["data-domain=\"", "\" data-id=\"", "\""], [d.id, r.id]))])), '')])),
      footer: B(_t("console.close"), 'close')
    })));
  }
  function gatewayDetail(id) {
    const g = G(id);
    if (!g) return;
    const ds = db.domains.filter((d) => d.gateways.includes(id));
    showModal(_modal(() => ({
      size: 'drawer',
      title: esc(g.name),
      sub: __nscVueAdd(__nscVueAdd(g.ip, ' / '), g.runtime),
      body: __nscVueAdd(callout(_t("archive.message_68edf37af4c8"), 'neutral'), __nscVueView(templates[160], [I('globe'), __nscVueJoin(ds.map((d) => __nscVueView(templates[159], [d.id, esc(d.name), d.rev, offline(id) ? badge(_t("community.evidence_state.UNKNOWN"), 'amber') : badge(_observedVersion(d.loaded[id]), d.loaded[id] === d.rev ? 'green' : 'amber')])), ''), I('lock')])),
      footer: B(_t("console.close"), 'close')
    })));
  }
  function domainSettings() {
    const d = D();
    showModal(_modal(() => ({
      size: 'drawer',
      title: _t("archive.message_119c43c4ff5f"),
      sub: __nscVueAdd(esc(d.name), _t("archive.message_88fb0e9b7607")),
      body: __nscVueView(templates[162], [esc(d.name), ENV[d.env], d.id, d.created, work.mode === 'native' ? _t("archive.message_b85a16d528cd") : _t("archive.message_6e9a39b85bd7"), I('server'), __nscVueJoin(d.gateways.map((id) => __nscVueView(templates[161], [I('server'), G(id).name, G(id).ip, ENV[G(id).env], badge(_t("archive.message_d1672f5617fe"), 'blue')])), ''), callout(_t("archive.message_6062b46fd2ca"), 'amber')]),
      footer: __nscVueAdd(B(_t("console.close"), 'close'), B(_t("archive.message_eb807696a744"), 'settings-to-config', 'code', 'primary'))
    })));
  }
  function addDomainModal(error = '') {
    showModal(_modal((modal) => ({
      kind: 'add-domain',
      size: 'small',
      title: _t("archive.message_d28c9d6f5ce9"),
      sub: _t("archive.message_8011edfeec16"),
      body: __nscVueView(templates[165], [esc(modal?.newName || ''), ENV[db.env], __nscVueJoin(db.services.filter((s) => s.status !== 'REMOVED' && s.env === db.env && s.project === db.project).map((s) => __nscVueView(templates[163], [s.id, esc(_serviceName(db.services, s.id))])), ''), callout(_t("archive.message_cbf124c2d068"), 'neutral'), error ? __nscVueView(templates[164], [esc(error)]) : '']),
      footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("archive.message_cff614e85faa"), 'domain-create', 'plus', 'primary'))
    }), modal));
  }
  function createDomain() {
    return addDomainModal();
  }
  function ownershipModal(native = true) {
    showModal(_modal(() => ({
      size: 'small',
      title: native ? _t("archive.message_ed5a2a809a7a") : _t("archive.message_a9c81869722a"),
      sub: __nscVueAdd(esc(D().name), _t("archive.message_33a8cefe5e9d")),
      body: __nscVueAdd(callout(native ? _t("archive.message_e80d3cc356ae") : _t("archive.message_e1136192709f"), 'amber'), __nscVueView(templates[166], [native ? _t("archive.message_17ae5459bfe9") : _t("archive.message_c0f31d2838fb"), native ? _t("archive.message_21f2a430ea12") : _t("archive.message_90f1348aac07")])),
      footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(native ? _t("archive.message_c8dfc8f34d19") : _t("archive.message_d42611f14f4d"), native ? 'native-confirm' : 'managed-confirm', 'check', 'primary'))
    })));
  }
  function driftModal() {
    const dr = D().drift;
    if (!dr) {
      toast(_t("archive.message_bbd16c6046e9"));
      return;
    }
    showModal(_modal(() => ({
      size: 'wide',
      kind: 'drift',
      title: _t("archive.message_546fd229f376"),
      sub: __nscVueAdd(__nscVueAdd(G(dr.gateway).name, ' · '), esc(D().name)),
      body: __nscVueAdd(callout(_t("archive.message_c9e38f6081c3"), 'amber'), __nscVueView(templates[167], [diffHTML(renderConfig(D().active), dr.content)])),
      footer: __nscVueAdd(__nscVueAdd(B(_t("archive.message_fb1c97956dae"), 'close', '', 'ghost'), B(_t("archive.message_27d4a91613ff"), 'drift-restore', 'history')), B(_t("archive.message_daea317325e3"), 'drift-adopt', 'edit', 'primary'))
    })));
  }
  function conflictModal() {
    showModal(_modal(() => ({
      kind: 'conflict',
      size: 'wide',
      title: _t("archive.message_d2d064b0c1db"),
      sub: __nscVueInterpolate([_t("archive.message_5f691455a564"), _t("archive.message_a153fd37fd9f"), ""], [D().draft?.base ?? D().rev, D().rev]),
      body: __nscVueAdd(callout(_t("archive.message_b27b25e8d406"), 'red'), __nscVueView(templates[168], [diffHTML(renderConfig(D().active), renderConfig(work))])),
      footer: __nscVueAdd(__nscVueAdd(B(_t("archive.message_fb1c97956dae"), 'close', '', 'ghost'), B(_t("archive.message_490ba0accb95"), 'conflict-current')), B(_t("archive.message_728e86cef063"), 'conflict-rebase', 'check', 'primary', 'disabled id="rebase-button"'))
    })));
  }
  function scenarioModal() {
    showModal(_modal(() => ({
      size: 'wide',
      title: _t("archive.message_cdd6630b67b5"),
      sub: _t("archive.message_bb3e089eb851"),
      body: __nscVueView(templates[170], [__nscVueJoin([['normal', 'checkcircle', _t("archive.message_07904b4d1abb"), _t("archive.message_9da3bbefa2a6")], ['partial', 'server', _t("archive.message_e4c65d1c7632"), _t("archive.message_3ae6388682b8")], ['offline', 'activity', _t("archive.message_23c5d8412051"), _t("archive.message_90c2128a1011")], ['drift', 'compare', _t("archive.message_763ec79bc435"), _t("archive.message_331cedf63abb")], ['conflict', 'users', _t("archive.message_13957fbcc55c"), _t("archive.message_7598aa3e2cdc")]].map(([id, ic, t, p]) => __nscVueView(templates[169], [db.scenario === id ? 'active' : '', id, I(ic), t, p])), ''), I('refresh'), callout(__nscVueAdd(_t("archive.message_43bc68a26067"), esc(D().name)), 'neutral')]),
      footer: B(_t("console.close"), 'close')
    })));
  }
  function setScenario(value) {
    return hooks.data.unsupported("setScenario", formError);
  }
  function helpModal() {
    showModal(_modal(() => ({
      size: 'wide',
      title: _t("archive.message_e630b0ec5be0"),
      sub: _t("archive.message_a13c31e593f5"),
      body: __nscVueView(templates[171], [callout(_t("archive.message_fce9cb6cdef7"), 'neutral'), callout(_t("archive.message_9f2d8fe2120f"), 'amber')]),
      footer: __nscVueAdd(B(_t("console.close"), 'close'), B(_t("archive.message_88caf516f1a2"), 'export-state', 'download'))
    })));
  }
  function palette() {
    if (modal?.kind === 'route' && !equal(routeForm, routeDraftSnapshot)) {
      toast(_t("archive.message_8d24537efc08"), true);
      return;
    }
    showModal(_modal(() => ({
      kind: 'palette',
      size: 'small',
      body: __nscVueView(templates[172], [paletteResults('')])
    })));
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
    const r = D().revisions.find((r) => r.v === Number(v));
    if (!r) return;
    showModal(_modal(() => ({
      size: 'small',
      title: __nscVueAdd(__nscVueAdd(_t("archive.message_77b738137352"), r.v), _t("archive.message_e6dfa134a534")),
      sub: esc(D().name),
      body: callout(_t("archive.message_fb6f24c35ea5"), 'amber'),
      footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("archive.message_ef1e2c5864e3"), 'restore-confirm', 'history', 'primary', __nscVueInterpolate(["data-version=\"", "\""], [r.v])))
    })));
  }
  // Central action router. UI never makes fetch/XHR calls.
  listen(document, 'click', (e) => {
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
          const d = db.domains.find((x) => x.name === 'example.com');
          navigate('domain', 'overview', d.id);
          break;
        }
      case 'menu':
        ui.menu = !ui.menu;
        render();
        break;
      case 'view':
        setPerspective(target.dataset.view);
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
          Promise.resolve(saveDraft(true)).then(ok => { if (ok) { closeModal(true); fn(); } });
          break;
        }
      case 'route-add':
        openRoute('');
        break;
      case 'route-edit':
        if (target.dataset.domain && (allEnvironmentView() || target.dataset.domain !== ui.domain)) {
          ensureNavigate(() => {
            const d = db.domains.find((d) => d.id === target.dataset.domain);
            if (d) hooks.navigate(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('/projects/', enc(d.project)), '/environments/'), enc(d.env)), '/domains/'), enc(d.id)), '?tab=routes')).then((failure) => {
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
      case 'route-target-mode':
        routeTargetMode = val === 'custom' ? 'custom' : 'service';
        if (routeTargetMode === 'service') routeForm.externalUrl = '';
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
        requestRouteDelete();
        break;
      case 'route-delete-confirm':
        work.routes = work.routes.filter((r) => r.id !== routeForm.id);
        ui.selectedRoute = work.routes[0]?.id;
        closeModal(true);
        invalidate();
        render();
        toast(_t("archive.message_bddef07fac7e"));
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
          toast(_t("archive.message_1126b2afaeaf"), true);
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
        toast(_t("archive.message_46488dce4b47"));
        break;
      case 'managed-confirm':
        work.mode = 'managed';
        work.native = '';
        ui.file = 'generated';
        ui.configView = 'semantic';
        closeModal(true);
        invalidate();
        render();
        toast(_t("archive.message_e5404cc95500"));
        break;
      case 'format-code':
        {
          const input = document.querySelector('#code-input');
          if (input) {
            input.value = __nscVueJoin(input.value.split('\n').map((l) => l.trimEnd()), '\n').replace(/\n{4,}/g, '\n\n\n');
            input.dispatchEvent(new Event('input', {
              bubbles: true
            }));
            toast(_t("archive.message_ed53f38fe49d"));
          }
          break;
        }
      case 'copy-config':
        {
          const text = renderConfig(work);
          if (navigator.clipboard?.writeText) navigator.clipboard.writeText(text).then(() => toast(_t("archive.message_337f004fbc57"))).catch(() => copyFallback(text));else copyFallback(text);
          break;
        }
      case 'export':
        download(__nscVueAdd(D().name, '.draft.conf'), renderConfig(work));
        toast(_t("archive.message_d13d16c1cb2b"));
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
        toast(_t("archive.message_68173c1028a3"));
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
        showModal(_modal(() => ({
          kind: 'diagnose',
          title: _t("archive.message_b22c7ea3ad9b"),
          sub: __nscVueAdd(esc(D().name), _t("archive.message_b847417fbd69")),
          size: 'small',
          body: diagnosticHTML(),
          footer: B(_t("console.close"), 'close')
        })));
        break;
      case 'run-diag':
        runDiagnostic();
        break;
      case 'release-start':
        startRelease();
        break;
      case 'deployment-detail':
        {
          const dep = db.deployments.find((x) => x.id === id);
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
          const dep = db.deployments.find((x) => x.id === id);
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
          const r = D().revisions.find((x) => x.v === Number(target.dataset.version));
          if (r) showModal(_modal(() => ({
            size: 'wide',
            title: __nscVueAdd(__nscVueAdd('Revision v', r.v), _t("archive.message_e9506c5e0a39")),
            sub: __nscVueAdd(__nscVueAdd(r.time, ' UTC+8 · '), esc(r.note)),
            body: codeHTML(renderConfig(r.config)),
            footer: B(_t("console.close"), 'close')
          })));
          break;
        }
      case 'restore-revision':
        restoreRevision(target.dataset.version);
        break;
      case 'restore-confirm':
        hooks.data.unsupported("restore-confirm", formError);
        break;
      case 'drift':
        driftModal();
        break;
      case 'drift-adopt':
        hooks.data.unsupported("drift-adopt", formError);
        break;
      case 'drift-restore':
        showModal(_modal(() => ({
          size: 'small',
          title: _t("archive.message_4af0f39c1d0b"),
          body: callout(_t("archive.message_0cad8277ece0"), 'amber'),
          footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("archive.message_89f2412b67b1"), 'drift-restore-confirm', 'history', 'primary'))
        })));
        break;
      case 'drift-restore-confirm':
        hooks.data.unsupported("drift-restore-confirm", formError);
        break;
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
        toast(_t("archive.message_42affdbeeb35"));
        break;
      case 'conflict-rebase':
        hooks.data.unsupported("conflict-rebase", formError);
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
        aboutModal();
        break;
      case 'reset-confirm':
        showModal(_modal(() => ({
          size: 'small',
          title: _t("archive.message_3adf6b9122c0"),
          body: callout(_t("archive.message_1926abf667f3"), 'amber'),
          footer: __nscVueAdd(__nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("archive.message_e9d3dd690a4e"), 'export-state', 'download')), B(_t("archive.message_96f2cb4f04d3"), 'reset', 'refresh', 'danger'))
        })));
        break;
      case 'reset':
        hooks.data.unsupported("reset", formError);
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
    toast(ok ? _t("archive.message_0db8404cc216") : _t("archive.message_583c73fe6dd3"), !ok);
  }
  listen(document, 'input', (e) => {
    const t = e.target;
    if (t.dataset.rf && routeForm) {
      const key = t.dataset.rf;
      routeForm[key] = ['timeout', 'connect', 'code'].includes(key) ? Number(t.value) : t.value;
      if (key === 'path') {
        const el = document.querySelector('#route-path-preview');
        if (el) el.textContent = __nscVueAdd(__nscVueAdd(__nscVueAdd(_t("archive.message_ebf2c23fd61f"), routeForm.path), _t("archive.message_faada43f2f95")), routeForm.strip ? '/users' : __nscVueAdd(routeForm.path.replace(/\/$/, ''), '/users'));
      }
      return;
    }
    if (t.id === 'code-input') {
      const f = t.dataset.file;
      if (f === 'native') work.native = t.value;else if (f === 'server') work.serverSnippet = t.value;else if (f === 'route') {
        const r = work.routes.find((r) => r.id === ui.selectedRoute) || work.routes[0];
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
  listen(document, 'change', (e) => {
    const t = e.target;
    if (t.id === 'env-select') {
      const desired = t.value;
      ensureNavigate(() => {
        db.env = desired;
        const d = db.domains.find((d) => d.env === desired);
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
  listen(document, 'scroll', (e) => {
    if (e.target.id === 'code-input') {
      const g = document.querySelector('#code-gutter');
      if (g) g.scrollTop = e.target.scrollTop;
    }
  }, true);
  listen(document, 'keydown', (e) => {
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
      const items = [...document.querySelectorAll('#overlay button:not(:disabled),#overlay input:not(:disabled),#overlay select:not(:disabled),#overlay textarea:not(:disabled),#overlay [tabindex="0"]')].filter((el) => el.offsetParent !== null);
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
  listen(window, 'beforeunload', (e) => {
    if (dirty() || deploymentTimer || modal?.kind === 'route' && !equal(routeForm, routeDraftSnapshot)) {
      e.preventDefault();
      e.returnValue = '';
    }
  });
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
    return hooks.data.state;
  }
  const V10 = {
    shell,
    globalPage,
    domainPage,
    configPage,
    semanticConfig,
    validateConfig,
    validationModal,
    checkRoute,
    saveDraft,
    serviceDetail,
    impactModal,
    showRouteForm,
    helpModal,
    gatewaysPage,
    closeModal
  };
  const KIND_NAMES = _labels({
    production: () => _t("archive.message_76ab7d3b41f2"),
    development: () => _t("archive.message_9ec428625fee"),
    testing: () => _t("archive.message_6aa8f49cc992"),
    sandbox: () => _t("archive.message_2a8ce2b15d65"),
    custom: () => _t("archive.message_4eafa9e925b3")
  });
  function P(id = db.project) {
    return db.projects.find((p) => p.id === id);
  }
  function E(id = db.env) {
    return db.environments.find((e) => e.id === id);
  }
  function projectEnvs(id = db.project) {
    return db.environments.filter((e) => e.project === id);
  }
  function scopeDomains(env = db.env, project = db.project) {
    return db.domains.filter((d) => d.project === project && d.env === env);
  }
  function scopeServices(env = db.env, project = db.project) {
    return db.services.filter((s) => s.status !== 'REMOVED' && s.project === project && s.env === env);
  }
  function gatewayAllowed(g, env = db.env) {
    return !!E(env)?.gateways.includes(g.id);
  }
  function scopeText(d = D()) {
    return __nscVueJoin([P(d.project)?.name, E(d.env)?.name, d.name].filter(Boolean), ' / ');
  }
  function hasDomain() {
    return !!db.domains.find((d) => d.id === ui.domain && d.project === db.project && d.env === db.env);
  }
  D = function () {
    return db.domains.find((d) => d.id === ui.domain && d.project === db.project && d.env === db.env) || {
      id: null,
      name: _t("archive.message_a8038634893a"),
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
    if (!P(project) || (env ? !e || e.project !== project : projectEnvs(project).length > 0)) return false;
    db.project = project;
    db.env = env || "";
    const d = scopeDomains().find((d) => d.id === domain) || scopeDomains()[0];
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
        const d = db.domains.find((d) => d.id === id);
        if (!d) {
          toast(_t("archive.message_f2e5b5fc1ab5"), true);
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
    if (!env && P(project) && !projectEnvs(project).length) {
      ensureNavigate(() => hooks.navigate('/projects/' + enc(project) + query(db.view === 'ops' ? {
        perspective: 'ops'
      } : {})));
      return;
    }
    const target = env || projectEnvs(project).find((e) => e.kind === 'production')?.id || projectEnvs(project)[0]?.id;
    if (!P(project) || !E(target) || E(target).project !== project) {
      toast(_t("archive.message_e81f1948c740"), true);
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
      toast(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(_t("archive.message_eb21bf740fc5"), P().name), ' / '), E().name), _t("archive.message_5e8d5d5f1f3f")));
    });
  }
  updateHash = function () {
    if (booting) return;
    const root = __nscVueAdd('#/projects/', encodeURIComponent(db.project));
    const h = ui.page === 'projects' ? '#/projects' : ui.page === 'project' ? root : ui.page === 'environments' ? __nscVueAdd(root, '/environments') : ui.page === 'domain' ? __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(root, '/environments/'), encodeURIComponent(db.env)), '/domains/'), encodeURIComponent(ui.domain)), '/'), ui.tab) : __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(root, '/environments/'), encodeURIComponent(db.env)), '/'), ui.page);
    if (location.hash !== h) history.replaceState(null, '', h);
  };
  parseHash = function () {
    const a = location.hash.replace(/^#\/?/, '').split('/').map((x) => {
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
        toast(_t("archive.message_33fd5274ee07"), true);
        return;
      }
      const env = a[2] === 'environments' && a[3] ? E(a[3]) : projectEnvs(p.id).find((e) => e.kind === 'production') || projectEnvs(p.id)[0];
      if (!env || env.project !== p.id) {
        ui.page = 'projects';
        toast(_t("archive.message_a56c55abbaa5"), true);
        return;
      }
      if (a[4] === 'domains' && a[5]) {
        const d = db.domains.find((d) => d.id === a[5] && d.project === p.id && d.env === env.id);
        if (!d) {
          selectScope(p.id, env.id);
          ui.page = 'domains';
          toast(_t("archive.message_6e776f1b1f4c"), true);
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
      const ds = db.domains.filter((d) => d.name === a[1]);
      if (ds.length === 1) {
        selectScope(ds[0].project, ds[0].env, ds[0].id);
        ui.page = 'domain';
        ui.tab = ['overview', 'routes', 'config', 'tls', 'history'].includes(a[2]) ? a[2] : 'overview';
      } else {
        ui.page = 'projects';
        toast(_t("archive.message_7407bb23d321"), true);
      }
    }
  };
  function navEntry(page, label, icon, count = '') {
    if (count === '') {
      if (page === 'services') count = db.services.filter((s) => s.status !== 'REMOVED' && inListScope(s)).length;
      if (page === 'routes') count = listDomains().reduce((total, domain) => total + (domain.draft?.config || domain.active).routes.length, 0);
      if (page === 'certificates') count = listDomains().length;
    }
    return __projectsViews.navEntry_34(__renderContext0, page, label, icon, count);
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
      label: _t("archive.message_222952431147"),
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
      domains: _t("archive.message_222952431147"),
      routes: _t("prototype.routing_rules"),
      services: _t("cluster.services"),
      configs: _t("archive.message_3f90761c6e4a"),
      gateways: _t("prototype.available_gateways"),
      certificates: _t("prototype.tls_certificates"),
      deployments: _t("archive.message_7290d89a6d74"),
      events: _t("archive.message_f98dfe0b4d54")
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
    if (work.mode === 'native') return __nscVueView(templates[206], [I('terminal'), B(_t("archive.message_ac3be09c6136"), 'file', 'code', 'primary', 'data-file="native"'), B(_t("archive.message_94aef91038b9"), 'managed-mode', 'settings'), B(_t("archive.message_1794d2c5f18e"), 'p-file-backups', 'history')]);
    return __nscVueView(templates[207], [I('globe'), esc(D().name), esc(P().name), esc(E().name), work.tls ? 'on' : '', work.tls, work.redirect ? 'on' : '', !work.tls ? 'disabled' : '', work.redirect, I('settings'), esc(work.body), B(_t("archive.message_77f2f2829738"), 'route-edit', 'route', 'sm', __nscVueAdd(__nscVueAdd('data-id="', work.routes.find((r) => r.path === '/api/')?.id || work.routes[0]?.id || ''), '"')), I('edit'), B(_t("archive.message_50d5ad08db2d"), 'file', 'code', 'sm', 'data-file="server"'), B(_t("archive.message_79bd97ec2617"), 'file', 'route', 'sm', 'data-file="route"'), I('terminal'), B(_t("archive.message_980c76391e93"), 'native-mode', 'terminal', 'sm'), (D().fileBackups?.length || 0) > 0 ? __nscVueAdd(' ', B(_t("archive.message_604c3fab9525"), 'p-file-backups', 'history', 'sm')) : '']);
  };
  saveDraft = function (silent = false) {
    if (typeof hooks.data.saveDomainDraft !== 'function') return hooks.data.unsupported('saveDraft', formError);
    if (work.mode === 'native') return hooks.data.unsupported('saveDraft', formError);
    if (!dirty()) return true;
    const domain = D(), candidate = clone(work), active = clone(domain.active);
    return hooks.data.saveDomainDraft(domain, active.routes, candidate).then(saved => {
      domain.draft = { config: candidate, base: domain.rev, revision: saved.revision };
      work = clone(candidate);
      ui.validation = null;
      if (!silent) toast(_t('domainPublication.saved'));
      render();
      return true;
    }).catch(error => { formError(error?.code || error?.message || String(error)); return false; });
  };
  checkRoute = function (r, exceptId = r.id) {
    if (r === routeForm && r.kind === 'proxy' && routeTargetMode === 'custom' && !r.externalUrl) return _t('domainPublication.targetRequired');
    if (r.kind === 'proxy' && r.externalUrl) {
      try { const url = new URL(r.externalUrl); if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || /[\s"{};$\\]/.test(r.externalUrl)) return _t('domainPublication.invalidURL'); }
      catch { return _t('domainPublication.invalidURL'); }
      if (!/^\/[a-zA-Z0-9_./~!@+\-]*$/.test(r.path) || r.path.includes('//') || r.path.includes('/../')) return _t('archive.message_8c847524546c');
      if (!Number.isInteger(r.timeout) || r.timeout < 1 || r.timeout > 3600 || !Number.isInteger(r.connect) || r.connect < 1 || r.connect > 300) return _t('domainPublication.invalidTimeout');
      if (work.routes.some(x => x.id !== exceptId && x.enabled && r.enabled && x.path === r.path && x.match === r.match)) return _t('archive.message_a478564214fc');
      return '';
    }
    if (r.kind === 'return' && (![200, 204, 403, 404, 503].includes(Number(r.code)) || String(r.value).length > 4096 || /[\r\n\0$]/.test(r.value))) return _t('domainPublication.invalidResponse');
    if (r.kind === 'redirect') {
      if (![301, 302, 307, 308].includes(Number(r.code))) return _t('domainPublication.invalidResponse');
      try { const url = new URL(r.value); if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || /[\r\n\0$]/.test(r.value)) return _t('domainPublication.invalidURL'); }
      catch { return _t('domainPublication.invalidURL'); }
    }
    const err = V10.checkRoute(r, exceptId);
    if (err) return err;
    const s = S(r.service);
    if (r.kind === 'proxy' && !r.externalUrl && (!s || s.project !== D().project || s.env !== D().env)) return _t("archive.message_439d22716877");
    return '';
  };
  function domainConflict(name, targets, exclude = null) {
    return db.domains.find((d) => d.id !== exclude && d.name === name && d.gateways.some((g) => targets.includes(g)));
  }
  validateConfig = function () {
    const v = V10.validateConfig(),
      d = D();
    for (const route of work.routes) if (route.enabled && route.kind === 'proxy') {
      const readyError = __nscVueAdd(route.path, _t('archive.message_45b3e1d25ed6'));
      if (route.externalUrl) {
        const legacyServiceError = `${route.path}：${_t('archive.message_179e609175eb')}`;
        v.errors = v.errors.filter(error => error !== readyError && error !== legacyServiceError);
        const routeError = checkRoute(route);
        if (routeError) v.errors.push(`${route.path}：${routeError}`);
      }
      else if (!ready(route.service).length) {
        v.errors = v.errors.filter(error => error !== readyError);
        v.warnings.push(`${route.path}: ${_t('domainPublication.zeroReady')}`);
      }
    }
    if (!hasDomain()) v.errors.push(_t("archive.message_bb66190a4405"));
    if (!E(d.env) || E(d.env).project !== d.project) v.errors.push(_t("archive.message_4bfe68e254d3"));
    if (!d.gateways.length) v.errors.push(_t("archive.message_02fbd39d0899"));
    if (d.gateways.some((id) => !E(d.env)?.gateways.includes(id))) v.errors.push(_t("archive.message_f3e298b99937"));
    const conflict = domainConflict(d.name, d.gateways, d.id);
    if (conflict) v.errors.push(__nscVueAdd(__nscVueAdd(_t("archive.message_90f01b223947"), scopeText(conflict)), _t("archive.message_6ea5941659aa")));
    if (work.mode === 'native') v.errors.push(_t('domainPublication.nativeUnsupported'));
    if (work.routes.some(route => route.kind === 'proxy' && route.externalUrl)) v.warnings.push(_t('domainPublication.externalRisk'));
    v.errors = [...new Set(v.errors)];
    return v;
  };
  validationModal = function () {
    if (typeof hooks.data.validatePoolDraft !== 'function') return V10.validationModal();
    const local = validateConfig();
    const domain = D();
    let detail = local.errors.length ? local.errors.join('\n') : _t('domainPublication.validating');
    const display = () => {
      const next = _modal(() => ({ kind: 'validation', title: _t('archive.message_41b0af8c5c9f'), sub: scopeText(domain),
        body: __nscVueAdd(callout(detail, local.errors.length ? 'red' : 'blue'), local.warnings.length ? callout(local.warnings.join('\n'), 'amber') : ''),
        footer: B(_t('console.close'), 'close') }));
      if (modal?.kind === 'validation') { modal.title = next.title; modal.body = next.body; renderModal(false); }
      else showModal(next);
    };
    display();
    if (local.errors.length) return;
    void (async () => {
      if (dirty() && !(await saveDraft(true))) return;
      const saved = domain.draft;
      if (!saved?.revision) throw new Error(_t('domainPublication.saveFirst'));
      let validation = await hooks.data.validatePoolDraft(domain.pool_id, saved.revision);
      for (let attempts = 0; attempts < 200 && !['SUCCEEDED', 'FAILED', 'EXPIRED', 'CANCELLED'].includes(validation.status); attempts++) {
        await new Promise(resolve => setTimeout(resolve, 1500));
        if (ui.domain !== domain.id) return;
        try { validation = await hooks.data.readValidation(domain.pool_id, validation.id); }
        catch { detail = _t('domainPublication.readInterrupted'); if (modal?.kind === 'validation') display(); continue; }
        detail = `${_t('domainPublication.validating')} ${Object.entries(validation.results || {}).map(([id, result]) => `${G(id)?.name || id}: ${result.status}`).join(', ')}`;
        if (modal?.kind === 'validation') display();
      }
      if (validation.status === 'SUCCEEDED') {
        ui.validation = { ...local, validation, hash: signature(), errors: [] };
        detail = _t('domainPublication.validated');
      } else {
        ui.validation = null;
        detail = `${validation.status}: ${Object.entries(validation.results || {}).map(([id, result]) => `${G(id)?.name || id}: ${result.error || result.status}`).join(', ')}`;
        local.errors.push(detail);
      }
      if (modal?.kind === 'validation') display();
      refreshHeader();
    })().catch(error => { ui.validation = null; detail = error?.code || error?.message || String(error); local.errors.push(detail); if (modal?.kind === 'validation') display(); });
  };
  impactModal = function (publish = false) {
    if (publish && typeof hooks.data.publishPoolDraft !== 'function') return hooks.data.unsupported('startRelease', formError);
    V10.impactModal(publish);
    if (modal?.kind === 'publish-confirm' || modal?.kind === 'impact') {
      modal.sub = __nscVueAdd(__nscVueAdd(scopeText(), _t("archive.message_f1c9b3bfd4a6")), D().draft?.base ?? D().rev);
      const poolTargets = db.gateways.filter(gateway => gateway.pool_id === D().pool_id);
      const zeroReady = work.routes.filter(route => route.enabled && route.kind === 'proxy' && !route.externalUrl && !ready(route.service).length);
      modal.body = __nscVueAdd(callout(_t('domainPublication.poolImpact', { v0: poolTargets.length }), publish ? 'amber' : 'blue'), __nscVueView([{ tag: 'div', attrs: [['class', 'mt16']], children: [
        { tag: 'p', attrs: [['class', 'field-label']], children: [_t('domainPublication.targetGateways')] },
        ...poolTargets.map(gateway => ({ tag: 'p', attrs: [['class', 'hint']], children: [`${gateway.name}: ${gateway.status || (gateway.available ? 'READY' : 'UNKNOWN')}`] })),
        ...(zeroReady.length ? [{ tag: 'p', attrs: [['class', 'hint']], children: [`${zeroReady.map(route => route.path).join(', ')}: ${_t('domainPublication.zeroReady')}`] }] : []),
        ...(work.routes.some(route => route.kind === 'proxy' && route.externalUrl) ? [{ tag: 'p', attrs: [['class', 'hint']], children: [_t('domainPublication.externalRisk')] }] : []),
        ...(publish ? [{ tag: 'label', attrs: [['class', 'mt16']], children: [{ tag: 'input', attrs: [['id', 'publish-ack'], ['type', 'checkbox']], children: [] }, _t('domainPublication.confirmPool')] }] : []),
      ] }], []));
      renderModal();
    }
  };
  startRelease = function () {
    if (typeof hooks.data.publishPoolDraft !== 'function') return hooks.data.unsupported('startRelease', formError);
    const validation = ui.validation?.validation, domain = D();
    if (!validation || ui.validation.hash !== signature()) { validationModal(); return; }
    closeModal(true);
    let detail = _t('domainPublication.publishing');
    let release = null;
    let failed = false;
    const display = () => {
      const next = _modal(() => ({ kind: 'pool-release', title: _t('delivery.publish'), sub: scopeText(domain), body: release ? releaseStatusView(release, detail, failed) : callout(detail, failed ? 'red' : 'blue'), footer: B(_t('console.close'), 'close') }));
      if (modal?.kind === 'pool-release') { modal.body = next.body; renderModal(false); }
      else showModal(next);
    };
    display();
    void (async () => {
      release = await hooks.data.publishPoolDraft(domain.pool_id, validation);
      detail = '';
      if (modal?.kind === 'pool-release') display();
      for (let attempts = 0; attempts < 200 && !['SUCCEEDED', 'FAILED', 'PARTIAL', 'UNKNOWN', 'STOPPED'].includes(release.status); attempts++) {
        await new Promise(resolve => setTimeout(resolve, 1500));
        if (ui.domain !== domain.id) return;
        try { release = await hooks.data.readRelease(domain.pool_id, release.id); }
        catch { detail = _t('domainPublication.readInterrupted'); if (modal?.kind === 'pool-release') display(); continue; }
        detail = '';
        if (modal?.kind === 'pool-release') display();
      }
      if (modal?.kind === 'pool-release') display();
      await hooks.data.refresh();
      work = clone(workingSaved());
      ui.validation = null;
      render();
    })().catch(error => { detail = error?.code || error?.message || String(error); failed = true; if (modal?.kind === 'pool-release') display(); });
  };
  showRouteForm = function (error = '') {
    V10.showRouteForm(error);
    if (modal?.kind === 'route') {
      modal.sub = __nscVueAdd(scopeText(), _t("archive.message_d844440edfde"));
      if (routeForm.kind === 'proxy') modal.body = replaceRouteServiceField(modal.body, serviceField => {
        const selector = __nscVueView([{ tag: 'div', attrs: [['class', 'field route-target-choice']], children: [
          { tag: 'label', attrs: [['class', 'field-label'], ['id', 'rf-target-mode-label']], children: [_t('domainPublication.targetMode')] },
          { tag: 'div', attrs: [['class', 'y-segments'], ['role', 'group'], ['aria-labelledby', 'rf-target-mode-label']], children: [
            { tag: 'button', attrs: [['type', 'button'], ['class', `NSCEXPR0END`], ['data-action', 'route-target-mode'], ['data-value', 'service'], ['aria-pressed', 'NSCEXPR1END']], children: [_t('domainPublication.internalService')] },
            { tag: 'button', attrs: [['type', 'button'], ['class', `NSCEXPR2END`], ['data-action', 'route-target-mode'], ['data-value', 'custom'], ['aria-pressed', 'NSCEXPR3END']], children: [_t('domainPublication.customTarget')] },
          ] },
        ] }], [routeTargetMode === 'service' ? 'selected' : '', routeTargetMode === 'service', routeTargetMode === 'custom' ? 'selected' : '', routeTargetMode === 'custom']);
        const target = routeTargetMode === 'service' ? serviceField : __nscVueView([{ tag: 'div', attrs: [['class', 'field']], children: [
          { tag: 'label', attrs: [['class', 'field-label'], ['for', 'rf-external-url']], children: [_t('domainPublication.externalTarget'), { tag: 'span', attrs: [['class', 'required']], children: ['*'] }] },
          { tag: 'input', attrs: [['id', 'rf-external-url'], ['data-rf', 'externalUrl'], ['type', 'url'], ['required', ''], ['value', 'NSCEXPR0END'], ['placeholder', 'https://upstream.example/']], children: [] },
          { tag: 'p', attrs: [['class', 'hint']], children: [_t('domainPublication.externalRisk')] },
        ] }], [routeForm.externalUrl || '']);
        return __nscVueJoin([selector, target], '');
      });
      renderModal();
    }
  };
  domainSettings = function () {
    const d = D();
    showModal(_modal(() => ({
      size: 'drawer',
      title: _t("archive.message_4ffe1300cee8"),
      sub: scopeText(),
      body: __nscVueView(templates[211], [esc(P(d.project).name), badge(E(d.env).name, E(d.env).kind === 'production' ? 'green' : 'blue'), esc(d.name), work.mode === 'native' ? _t("archive.message_82fb5d137a14") : _t("archive.message_6e9a39b85bd7"), d.id, __nscVueJoin(d.gateways.map((id) => __nscVueView(templates[209], [I('server'), esc(G(id).name), badge(_t("archive.message_f70c0007f30f"), 'blue')])), ''), d.rev === 0 ? __nscVueView(templates[210], [B(_t("archive.message_51f3aca891b2"), 'p-domain-targets', 'server', 'sm')]) : '', callout(_t("archive.message_0b4f0f9e5940"), 'blue'), B(_t("prototype.copy_configuration"), 'p-copy', 'copy', 'sm')]),
      footer: __nscVueAdd(B(_t("console.close"), 'close'), B(_t("archive.message_8e6f0a5e0f74"), 'settings-to-config', 'code', 'primary'))
    })));
  };
  function modeSnapshotText() {
    const c = clone(work);
    c.mode = 'managed';
    c.native = '';
    return renderConfig(c);
  }
  ownershipModal = function (native = true) {
    const d = D();
    showModal(_modal(() => ({
      kind: 'mode-switch',
      size: native ? 'small' : 'wide',
      modeDirection: native ? 'file' : 'form',
      title: native ? _t("archive.message_ed5a2a809a7a") : _t("archive.message_3a5f9e9c47c7"),
      sub: __nscVueAdd(scopeText(), _t("archive.message_7b8a2970e290")),
      body: __nscVueView(templates[215], [callout(native ? __nscVueView(templates[212], []) : __nscVueView(templates[213], []), 'amber'), I('file'), native ? _t("archive.message_64c6b00d8354") : _t("archive.message_d9793e93b9d7"), native ? _t("archive.message_7da05ea9df50") : _t("archive.message_dbd84db2b4e6"), I('settings'), native ? _t("archive.message_6a130c1c9e53") : _t("archive.message_da07230b3178"), native ? _t("archive.message_b57b6fe295b9") : _t("archive.message_5ab6e0a8629f"), I('send'), !native ? __nscVueView(templates[214], [diffHTML(work.native, modeSnapshotText(), true).replace(__nscVueAdd(__nscVueAdd('· v', D().rev), _t("archive.message_56fe2afa4cfe")), _t("archive.message_038aaaa9e519"))]) : '', native ? _t("archive.message_5478c48c818d") : _t("archive.message_b98e4a67612d")]),
      footer: __nscVueAdd(B(native ? _t("archive.message_adba1f6aa25a") : _t("archive.message_66e51098a991"), 'close'), B(native ? _t("archive.message_5e8a90d82a32") : _t("archive.message_f8367f1078c3"), native ? 'p-mode-file' : 'p-mode-form', native ? 'code' : 'settings', 'primary', 'id="p-mode-confirm" disabled'))
    })));
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
        reason: _t("archive.message_961899df9bcb"),
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
    event(toFile ? _t("archive.message_fc2ee2c061c6") : _t("archive.message_2acca2fca8e6"), _t("archive.message_a2f68c1ab521"), 'edit');
    closeModal(true);
    invalidate();
    updateHash();
    render();
    toast(toFile ? _t("archive.message_dc566e9ac50c") : _t("archive.message_f3d80934bfbe"));
  }
  function fileBackups() {
    const list = D().fileBackups || [];
    showModal(_modal(() => ({
      size: 'wide',
      title: _t("archive.message_9709d8ec678e"),
      sub: scopeText(),
      body: __nscVueAdd(callout(_t("archive.message_acb4938aa47c"), 'neutral'), __nscVueView(templates[218], [list.length ? __nscVueJoin(list.map((x) => __nscVueView(templates[216], [cardHead(x.time, esc(x.reason), B(_t("archive.message_9496692ac628"), 'p-backup-export', 'download', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [x.id]))), codeHTML(x.text)])), '') : __nscVueView(templates[217], [])])),
      footer: __nscVueAdd(B(_t("console.close"), 'close'), B(_t("archive.message_88caf516f1a2"), 'export-state', 'download'))
    })));
  }
  let formErrorMessage = '';
  function formError(message) {
    if (modal?.kind === 'p-project-form' || modal?.kind === 'p-env-form') {
      formErrorMessage = String(message);
      hooks.render();
      return;
    }
    const el = document.querySelector('#p-form-error');
    if (el) {
      region(el, callout(esc(message), 'red'));
      el.scrollIntoView({
        block: 'nearest'
      });
    } else toast(message, true);
  }
  function validHost(name) {
    return name.length <= 253 && name.includes('.') && name.split('.').every((l) => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(l));
  }
  function projectModal() {
    formErrorMessage = '';
    showModal(_modal(() => ({
      kind: 'p-project-form',
      size: 'small',
      title: _t("archive.message_43d7d2b51d75"),
      sub: _t("archive.message_8702cd40eeac"),
      body: ''
    })));
  }
  function createProject() {
    return hooks.data.command("createProject", arguments);
  }
  function gatewayChoices(selected = [], name = 'p-gateway') {
    return __nscVueView(templates[221], [__nscVueJoin(db.gateways.map((g) => {
      const n = db.environments.filter((e) => e.gateways.includes(g.id)).length;
      return __nscVueView(templates[220], [name, g.id, selected.includes(g.id) ? 'checked' : '', esc(g.name), esc(g.runtime), n]);
    }), '')]);
  }
  function envModal(editId = null) {
    formErrorMessage = '';
    const e = E(editId),
      editing = !!e;
    showModal(_modal(() => ({
      kind: 'p-env-form',
      envId: editId,
      size: 'drawer',
      title: editing ? _t("archive.message_f812dc4f80ae") : _t("archive.message_a2a1229f025d"),
      sub: __nscVueAdd(P().name, _t("archive.message_3feade5b0935")),
      body: ''
    })));
  }
  function saveEnvironment() {
    return hooks.data.command("saveEnvironment", arguments);
  }
  function deleteEnvironment(id) {
    const e = E(id);
    if (projectEnvs(e.project).length === 1) return formError(_t("archive.message_44971c532899"));
    if (scopeDomains(id, e.project).length || scopeServices(id, e.project).length) return formError(_t("archive.message_b3bce83185c7"));
    showModal(_modal(() => ({
      size: 'small',
      title: _t("archive.message_ada4cf95f084"),
      sub: esc(e.name),
      body: callout(_t("archive.message_6bf31ac164d5"), 'amber'),
      footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("archive.message_1bc3c7b5de2c"), 'p-env-delete-confirm', 'trash', 'danger', __nscVueInterpolate(["data-id=\"", "\""], [id])))
    })));
  }
  function chooseEnvForDomain() {
    showModal(_modal(() => ({
      size: 'small',
      title: _t("archive.message_d01030e31a52"),
      sub: P().name,
      body: __nscVueView(templates[225], [__nscVueJoin(projectEnvs().map((e) => __nscVueView(templates[224], [e.id, I('layers'), esc(e.name), scopeDomains(e.id).length, esc(e.slug), I('right', 'sm')])), '')]),
      footer: B(_t("console.cancel"), 'close')
    })));
  }
  addDomainModal = function () {
    return hooks.navigate(__nscVueInterpolate(["/projects/", "/environments/", "/domains?create=1"], [encodeURIComponent(db.project), encodeURIComponent(db.env)]));
  };
  createDomain = function () {
    return addDomainModal();
  };
  function serviceModal() {
    showModal(_modal(() => ({
      kind: 'p-service-form',
      size: 'small',
      title: _t("archive.message_659d106ab7e8"),
      sub: __nscVueAdd(__nscVueAdd(P().name, ' / '), E().name),
      body: __nscVueAdd(callout(_t("archive.message_c3059e1e15fd"), 'neutral'), __nscVueView(templates[230], [])),
      footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("archive.message_659d106ab7e8"), 'p-service-create', 'plus', 'primary'))
    })));
  }
  function createService() {
    return hooks.data.unsupported("createService", formError);
  }
  let copyFlow = null;
  function beginCopy() {
    if (projectEnvs().length < 2) {
      toast(_t("archive.message_d66249c96378"));
      return;
    }
    const candidates = db.domains.filter((d) => d.project === db.project);
    if (!candidates.length) {
      toast(_t("archive.message_7c24e21c3e6b"));
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
    document.querySelectorAll('[data-copy-service]').forEach((x) => copyFlow.mapping[x.dataset.copyService] = x.value);
    document.querySelectorAll('[data-copy-redirect]').forEach((x) => copyFlow.redirects[x.dataset.copyRedirect] = x.value.trim());
    if (document.querySelector('[name="copy-gateway"]')) copyFlow.gateways = [...document.querySelectorAll('[name="copy-gateway"]:checked')].map((x) => x.value);
    const tls = document.querySelector('#p-copy-tls');
    if (tls) copyFlow.tls = tls.checked;
    const snippets = document.querySelector('#p-copy-snippets');
    if (snippets) copyFlow.includeSnippets = snippets.checked;
    const review = document.querySelector('#p-copy-snippet-review');
    if (review) copyFlow.reviewSnippets = review.checked;
  }
  function copySource() {
    return db.domains.find((d) => d.id === copyFlow?.source);
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
      ss = [...new Set(c.routes.filter((r) => r.kind === 'proxy').map((r) => r.service))],
      native = c.mode === 'native';
    let body = __nscVueView(templates[231], [step('1', _t("archive.message_f7c7ca04afa0"), f.step === 1 ? 'now' : 'done'), step('2', _t("archive.message_7fc638f00b20"), f.step === 2 ? 'now' : f.step > 2 ? 'done' : ''), step('3', _t("archive.message_2cc781edb5a3"), f.step === 3 ? 'now' : '')]);
    if (f.step === 1) body = __nscVueAdd(body, __nscVueView(templates[234], [__nscVueJoin(db.domains.filter((d) => d.project === f.project).map((d) => __nscVueView(templates[232], [d.id, d.id === f.source ? 'selected' : '', esc(E(d.env).name), esc(d.name), d.draft ? _t("archive.message_7b42be0f22c7") : _t("archive.message_d7b2efb832b0")])), ''), s.id === ui.domain && dirty() ? _t("archive.message_de4b3e96f40e") : s.draft ? _t("archive.message_66f7003e49ba") : _t("archive.message_57fcd1dd1c43"), __nscVueJoin(projectEnvs(f.project).filter((e) => e.id !== s.env).map((e) => __nscVueView(templates[233], [e.id, e.id === f.target ? 'selected' : '', esc(e.name), esc(e.slug)])), ''), esc(f.name), native ? callout(_t("archive.message_5542d8b5f007"), 'amber') : callout(_t("archive.message_1462a23701c4"), 'blue')]));
    if (f.step === 2) body = __nscVueAdd(body, __nscVueView(templates[241], [esc(E(s.env).name), esc(s.name), esc(target?.name), esc(f.name), __nscVueJoin(ss.map((id) => __nscVueView(templates[236], [I('box'), esc(_serviceName(db.services, id)), esc(id), I('arrow'), id, esc(_serviceName(db.services, id)), __nscVueJoin(scopeServices(f.target, f.project).map((x) => __nscVueView(templates[235], [x.id, f.mapping[id] === x.id ? 'selected' : '', esc(_serviceName(db.services, x.id)), x.port, ready(x.id).length])), '')])), '') || __nscVueView(templates[237], []), __nscVueJoin(c.routes.filter((r) => r.kind === 'redirect').map((r) => __nscVueView(templates[238], [esc(r.path), r.id, esc(f.redirects[r.id] || ''), esc(r.value)])), ''), __nscVueJoin((target?.gateways || []).map((id) => __nscVueView(templates[239], [id, f.gateways.includes(id) ? 'checked' : '', esc(G(id).name), esc(G(id).runtime)])), '') || __nscVueView(templates[240], []), f.tls ? 'checked' : '', f.includeSnippets ? 'checked' : '', f.includeSnippets ? 'open' : '', codeHTML(__nscVueJoin([c.serverSnippet, ...c.routes.map((r) => r.snippet)].filter(Boolean), '\n')), f.reviewSnippets ? 'checked' : '']));
    if (f.step === 3) body = __nscVueAdd(body, __nscVueView(templates[242], [esc(P(f.project).name), esc(target.name), esc(f.name), badge(target.kind === 'production' ? _t("archive.message_f37905fd6d9c") : _t("archive.message_8b71337f2999"), target.kind === 'production' ? 'amber' : 'blue'), callout(_t("archive.message_78c2c983554a"), 'blue'), c.routes.length, f.includeSnippets ? _t("archive.message_bf6ab62d3e92") : '', __nscVueJoin(Object.values(f.mapping).map((id) => esc(_serviceName(db.services, id))), '、') || _t("archive.message_c7c08811f7d8"), __nscVueJoin(f.gateways.map((id) => esc(G(id).name)), ' / '), f.tls ? _t("archive.message_41d0bf650c0a") : _t("archive.message_cb3af7459b0e")]));
    body = __nscVueAdd(body, __nscVueView(templates[243], []));
    showModal(_modal(() => ({
      kind: 'p-copy',
      size: 'wide',
      title: _t("prototype.copy_configuration"),
      sub: __nscVueAdd(P(f.project).name, _t("archive.message_724502190586")),
      body,
      footer: __nscVueAdd(f.step > 1 ? B(_t("archive.message_da336fdc0dbd"), 'p-copy-back', 'back') : B(_t("console.cancel"), 'close'), B(f.step === 3 ? _t("archive.message_4c3eacb22e8b") : _t("console.next"), f.step === 3 ? 'p-copy-create' : 'p-copy-next', f.step === 3 ? 'copy' : 'right', 'primary', f.step === 3 ? 'disabled id="p-copy-confirm"' : native ? _a("disabled title=\"", "archive.message_62ed2b7d3bb9", "\"") : ''))
    })));
  }
  function validateCopy(step) {
    const f = copyFlow,
      s = copySource(),
      c = copyConfig(),
      e = E(f.target);
    if (!s || s.project !== f.project || f.project !== db.project) return _t("archive.message_717b785d16db");
    if (c.mode === 'native') return _t("archive.message_f65f11ea501a");
    if (!e || e.project !== f.project || e.id === s.env) return _t("archive.message_730486863bf6");
    if (!validHost(f.name)) return _t("archive.message_ed66f7ef1766");
    if (scopeDomains(e.id, e.project).some((d) => d.name === f.name)) return _t("archive.message_420e61c97707");
    if (step >= 2) {
      if (!f.gateways.length) return _t("archive.message_76124161e443");
      if (f.gateways.some((id) => !e.gateways.includes(id))) return _t("archive.message_345f9711eed5");
      const conflict = domainConflict(f.name, f.gateways);
      if (conflict) return __nscVueAdd(__nscVueAdd(_t("archive.message_4c2cf534e916"), scopeText(conflict)), _t("archive.message_ce7ca5546346"));
      for (const id of new Set(c.routes.filter((r) => r.kind === 'proxy').map((r) => r.service))) {
        const t = S(f.mapping[id]);
        if (!t || t.project !== e.project || t.env !== e.id) return _t("archive.message_2c41223e41ff");
      }
      for (const r of c.routes.filter((r) => r.kind === 'redirect')) {
        try {
          const u = new URL(f.redirects[r.id] || '');
          if (!['http:', 'https:'].includes(u.protocol) || u.username || u.password) throw Error();
        } catch (_) {
          return _t("archive.message_f130f0a77bea");
        }
      }
      if (f.includeSnippets && !f.reviewSnippets) return _t("archive.message_2ad751d3bac1");
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
    return hooks.data.unsupported("finishCopy", formError);
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
    const gs = db.gateways.filter((g) => allEnvironmentView() ? listEnvironments().some((e) => e.gateways.includes(g.id)) : gatewayAllowed(g));
    return __nscVueView(templates[246], [I('info', 'sm'), __nscVueJoin(gs.map((g) => {
      const refs = db.environments.filter((e) => e.gateways.includes(g.id));
      return __nscVueView(templates[244], [I('server'), esc(g.name), esc(g.ip), esc(g.runtime), __nscVueJoin(refs.map((e) => badge(__nscVueAdd(__nscVueAdd(P(e.project).name, ' / '), e.name))), ''), B(_t("archive.message_2520858903c9"), 'gateway-detail', 'right', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [g.id]))]);
    }), '') || __nscVueView(templates[245], []), B(_t("archive.message_c96172a4b45a"), 'p-env-settings', 'settings', '', __nscVueInterpolate(["data-id=\"", "\""], [db.env]))]);
  };
  paletteResults = function (q) {
    q = q.trim().toLowerCase();
    const ds = db.domains.filter((d) => (allProjectView() || d.project === db.project) && d.name.toLowerCase().includes(q)),
      ss = db.services.filter((s) => s.status !== 'REMOVED' && (allProjectView() || s.project === db.project) && _serviceSearch(db.services, s.id).includes(q));
    return __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueView(templates[247], [allProjectView() ? _t("archive.message_273ec15f9553") : __nscVueAdd(__nscVueAdd(_t("archive.message_2b54fee2ad35"), esc(P().name)), _t("archive.message_08d8662812cb"))]), __nscVueJoin(ds.map((d) => __nscVueView(templates[248], [d.id, I('globe'), esc(d.name), esc(P(d.project).name), esc(E(d.env).name), I('right', 'sm')])), '')), __nscVueJoin(ss.map((s) => __nscVueView(templates[249], [s.id, I('box'), esc(_serviceName(db.services, s.id)), esc(resourceScopeName(s)), esc(s.id)])), '')), !ds.length && !ss.length ? __nscVueView(templates[250], []) : '');
  };
  helpModal = function () {
    showModal(_modal(() => ({
      size: 'wide',
      title: _t("archive.message_9f14d77cd9c4"),
      sub: _t("archive.message_0d1216e12bfe"),
      body: __nscVueView(templates[251], [callout(_t("archive.message_9f813bff94c7"), 'blue')]),
      footer: __nscVueAdd(B(_t("console.close"), 'close'), B(_t("archive.message_88caf516f1a2"), 'export-state', 'download'))
    })));
  };
  function domainTargetsModal() {
    const d = D();
    if (d.rev > 0) {
      toast(_t("archive.message_0f05bc2df4d0"), true);
      return;
    }
    showModal(_modal(() => ({
      kind: 'domain-targets',
      size: 'small',
      title: _t("archive.message_c1608da4cd5b"),
      sub: scopeText(),
      body: __nscVueView(templates[254], [__nscVueJoin(E().gateways.map((id) => __nscVueView(templates[252], [id, d.gateways.includes(id) ? 'checked' : '', esc(G(id).name), esc(G(id).ip)])), '') || __nscVueView(templates[253], [])]),
      footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("archive.message_1576506d3549"), 'p-domain-targets-save', 'save', 'primary'))
    })));
  }
  function saveDomainTargets() {
    return hooks.data.unsupported("saveDomainTargets", formError);
  }
  // Capture project-layer events before the retained v1.0 central action router.
  listen(document, 'click', (e) => {
    const t = e.target.closest('[data-action]');
    if (!t || t.disabled || t.dataset.vueControl === 'true') return;
    const a = t.dataset.action,
      id = t.dataset.id;
    if (!a.startsWith('p-') && a !== 'reset') return;
    e.preventDefault();
    e.stopImmediatePropagation();
    if (ui.externalConflict && !['p-file-backups', 'p-backup-export'].includes(a)) {
      toast(_t("archive.message_d86dfab74539"), true);
      return;
    }
    if (deploymentTimer) {
      toast(_t("archive.message_7cbb758e89de"), true);
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
        hooks.data.command("deleteEnvironment", [id]);
        break;
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
          const f = D().fileBackups?.find((x) => x.id === id);
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
        hooks.data.unsupported("reset", formError);
        break;
    }
  }, true);
  listen(document, 'change', (e) => {
    const t = e.target;
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
    db.project = db.projects[0]?.id || '';
    db.env = projectEnvs()[0]?.id || '';
  }
  if (!E(db.env) || E(db.env).project !== db.project) db.env = projectEnvs()[0]?.id || '';
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
      get serviceLogsPage() {
        return (id, mode) => hooks.data.serviceLogsPage?.(id, mode) || '';
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
    const MODE = _labels({
      stack: () => _t("archive.message_0010dedd5bfc"),
      compose: () => _t("archive.message_018576a0d8d9"),
      container: () => _t("archive.message_6d5e3e86d2c0"),
      swarmservice: () => _t("archive.message_50fd33e93b06"),
      external: () => _t("archive.message_27b97fffee25")
    });
    const RT = (id) => db.runtimes.find((r) => r.id === id);
    const APP = (id) => db.apps.find((a) => a.id === id);
    const ST = (id) => db.services.find((s) => s.id === id);
    const NODE = (id) => db.runtimes.flatMap((r) => r.nodes).find((n) => n.id === id);
    const IS = (id) => db.instances.find((t) => t.id === id);
    const OP = (id) => db.appDeployments.find((o) => o.id === id);
    const appServices = (a) => db.services.filter((s) => s.app === a.id && s.status !== 'REMOVED');
    const scopeName = (p, e) => __nscVueInterpolate(["", " / ", ""], [P(p)?.name || _t("archive.message_d52ef0c7853e"), E(e)?.name || _t("archive.message_eac29b7b220f")]);
    const projectName = (p) => P(p)?.name || _t("archive.message_d52ef0c7853e");
    const envName = (e) => E(e)?.name || _t("archive.message_eac29b7b220f");
    const xTime = () => stamp();
    const statusCN = _labels({
      RUNNING: () => _t("archive.message_1f0eb99b7ed0"),
      PENDING: () => _t("archive.message_fea0fc6011d9"),
      PREPARING: () => _t("archive.message_0253452208ad"),
      STARTING: () => _t("archive.message_33439d263173"),
      COMPLETE: () => _t("cluster.state_complete"),
      FAILED: () => _t("archive.message_206aef655681"),
      SHUTDOWN: () => _t("archive.message_f006455e3baf"),
      REJECTED: () => _t("archive.message_5720f59a24ca"),
      ORPHANED: () => _t("archive.message_aaf5e5fef170"),
      UNKNOWN: () => _t("archive.message_93e474c68ed5")
    });
    const x = {
      layout: db.serviceLayout || 'grouped',
      svcScope: 'environment',
      svcProject: 'all',
      svcRuntime: 'all',
      svcHealth: 'all',
      svcSearch: '',
      includeRemoved: false,
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
    const TEMPLATES = hooks.data.templates;
    const tpl = (id) => hooks.data.template(id);
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
      return hooks.data.state;
    }
    function appSpec(a) {
      return hooks.data.applicationSpec(a);
    }
    seedWorkloads();
    function runtimeGate(r) {
      if (!r) return _t("archive.message_a53de7c66141");
      if (r.connection !== 'online') return _t("archive.message_a3044e5ede85");
      if (!r.canWrite) return _t("archive.message_6b323f24d63f");
      if (r.type === 'swarm') {
        if (!r.managerRead) return _t("archive.message_b01b42035d25");
        const m = r.nodes.filter((n) => n.role === 'manager');
        if (m.filter((n) => n.manager === 'Leader' || n.manager === 'Reachable').length < __nscVueAdd(Math.floor(m.length / 2), 1)) return _t("archive.message_eae2aea10feb");
      }
      return '';
    }
    function currentInstances(s, history = false) {
      return _serviceInstances(db.instances, s, RT(s.runtimeId), history);
    }
    function serviceState(s) {
      if (s.status === 'REMOVED') return { name: _t('services.removed'), tone: '', key: 'removed', detail: _t('services.removed') };
      const r = RT(s.runtimeId);
      if (!s.runtimeId) return {
        name: _t("archive.message_5a78fa394548"),
        tone: '',
        key: 'external',
        detail: _t("archive.message_1e00bbdda568")
      };
      const summary = _serviceInstanceSummary(db.instances, s, r);
      if (summary.unknown) return {
        name: _t("archive.message_93e474c68ed5"),
        tone: 'amber',
        key: 'unknown',
        detail: _t("archive.message_61dce4d7115e")
      };
      const ins = summary.current;
      if (s.specVersion && s.observedVersion !== s.specVersion) return {
        name: _t("archive.message_bc315a82514a"),
        tone: 'amber',
        key: 'issue',
        detail: __nscVueAdd(__nscVueAdd(__nscVueAdd(_t("archive.message_3d0a3d9eb82c"), s.specVersion), _t("archive.message_cb9aa4b3a6f2")), s.observedVersion ? __nscVueAdd('r', s.observedVersion) : _t("community.evidence_state.UNKNOWN"))
      };
      if (s.workloadKind === 'job') {
        if (ins.some((t) => t.state === 'COMPLETE')) return {
          name: _t("cluster.state_complete"),
          tone: 'green',
          key: 'complete',
          detail: _t("archive.message_a9b5ba6b3555")
        };
        if (ins.some((t) => ['FAILED', 'REJECTED'].includes(t.state))) return {
          name: _t("archive.message_9344597efd49"),
          tone: 'red',
          key: 'issue',
          detail: _t("archive.message_036c4a15d868")
        };
        return {
          name: _t("archive.message_5026a63b58cd"),
          tone: 'blue',
          key: 'running',
          detail: _t("archive.message_3950bad5b197")
        };
      }
      if (s.desired === 0) return {
        name: _t("archive.message_f006455e3baf"),
        tone: '',
        key: 'stopped',
        detail: _t("archive.message_46f7d1358f30")
      };
      const run = summary.running.length;
      if (run < (s.desired ?? 1)) return {
        name: _t("archive.message_c474e26ff2b5"),
        tone: 'amber',
        key: 'issue',
        detail: __nscVueInterpolate(["", " / ", _t("archive.message_dd94afdae69f")], [run, s.desired])
      };
      if (summary.unhealthy) return {
        name: _t("archive.message_db422791a2a7"),
        tone: 'red',
        key: 'issue',
        detail: __nscVueInterpolate([_t("archive.message_a29348be5310"), " / ", _t("archive.message_938095699609")], [run, s.desired])
      };
      if (s.health === 'not-configured') return {
        name: _t("archive.message_f07ad111f9d4"),
        tone: 'blue',
        key: 'running',
        detail: __nscVueInterpolate(["", " / ", _t("archive.message_dd94afdae69f")], [run, s.desired])
      };
      if (summary.running.some(t => !['healthy', 'not-configured'].includes(t.health))) return {
        name: _t("community.evidence_state.UNKNOWN"), tone: 'amber', key: 'unknown',
        detail: __nscVueInterpolate(["", " / ", _t("archive.message_dd94afdae69f")], [run, s.desired])
      };
      return {
        name: _t("archive.message_b3b0a282ed87"),
        tone: 'green',
        key: 'healthy',
        detail: __nscVueInterpolate(["", " / ", _t("archive.message_dd94afdae69f")], [run, s.desired])
      };
    }
    function appState(a) {
      const s = appServices(a);
      if (RT(a.runtime)?.connection !== 'online') return {
        name: _t("archive.message_93e474c68ed5"),
        tone: 'amber'
      };
      if (!s.length) return {
        name: _t("archive.message_1f55855535c5"),
        tone: ''
      };
      const st = s.map(serviceState);
      if (st.some((i) => i.key === 'issue')) return {
        name: _t("community.active_issue"),
        tone: 'amber'
      };
      if (st.every((i) => i.key === 'stopped')) return {
        name: _t("archive.message_f006455e3baf"),
        tone: ''
      };
      return {
        name: _t("archive.message_b3b0a282ed87"),
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
      return db.domains.flatMap((d) => (d.draft?.config || d.active).routes.filter((r) => r.service === s.id).map((r) => ({
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
      applications: () => _t("archive.message_63c73c4730f4"),
      market: () => _t("archive.message_f2e7caa9f119"),
      application: () => _t("prototype.application_details"),
      service: () => _t("archive.message_46705301c2d5"),
      activity: () => _t("activityHub.title"),
      installation: () => _t("installation.title"),
      ha: () => _t("ha.title"),
      runtimes: () => _t("prototype.clusters_hosts"),
      runtime: () => _t("archive.message_1dbf586017ba"),
      workloadHistory: () => _t("prototype.deployments_changes"),
      runtimeEvents: () => _t("prototype.runtime_records")
    });
    projectSidebar = function () {
      return __nscVueView(templates[261], [ui.menu ? 'open' : '', I('back', 'sm'), db.projects.length, I('folder'), allProjectView() ? 'selected' : '', __nscVueJoin(db.projects.map((p) => __nscVueView(templates[259], [p.id, !allProjectView() && p.id === db.project ? 'selected' : '', esc(p.name)])), ''), I('chevron', 'sm'), allProjectView() ? _t("archive.message_e2c00c9bebfe") : esc(P()?.key), navEntry('project', _t("prototype.project_overview"), 'grid'), navEntry('environments', _t("prototype.environments_domains"), 'layers'), navEntry('variables', _t("prototype.variables_configuration"), 'sliders'), E()?.kind === 'production' ? '' : 'gray', allEnvironmentView() ? 'selected' : '', __nscVueJoin((allProjectView() ? [] : projectEnvs()).map((e) => __nscVueView(templates[260], [e.id, !allEnvironmentView() && e.id === db.env ? 'selected' : '', esc(e.name)])), ''), I('chevron', 'sm'), navEntry('applications', _t("archive.message_63c73c4730f4"), 'box', db.apps.filter(inListScope).length), navEntry('services', _t("cluster.services"), 'layers'), navEntry('domains', _t("archive.message_222952431147"), 'globe', listDomains().length), navEntry('routes', _t("prototype.routing_rules"), 'route'), navEntry('configs', _t("archive.message_3f90761c6e4a"), 'code'), navEntry('certificates', _t("prototype.tls_certificates"), 'shield'), navEntry('workloadHistory', _t("prototype.deployments_changes"), 'history'), navEntry('market', _t("archive.message_f2e7caa9f119"), 'grid'), navEntry('runtimes', _t("prototype.clusters_hosts"), 'server'), navEntry('runtimeEvents', _t("prototype.runtime_records"), 'activity'), navEntry('gateways', _t("prototype.available_gateways"), 'route'), I('play', 'sm'), badge('1.3'), TB(_t("prototype.failover_demo"), 'x-demo', 'settings')]);
    };
    contextBar = function () {
      if (['market', 'runtimes', 'runtime', 'runtimeEvents'].includes(ui.page) || ui.page === 'services' && x.svcScope === 'all' && !allProjectView()) return '';
      return OLD.contextBar();
    };
    projectPage = function () {
      let h = OLD.projectPage();
      return insertView(h, '.project-bottom-grid', __nscVueView(templates[262], [cardHead(_t("archive.message_bc7ee2da1c61"), _t("archive.message_1d11ba6ce1d0"), B(_t("archive.message_3a4a165a145b"), 'x-install', 'plus', 'sm'), 'box'), appTable(db.apps.filter((a) => a.project === db.project))]), 'before');
    };
    environmentOverview = function () {
      return insertView(OLD.environmentOverview(), '[data-action=domain-add]', __nscVueAdd(__nscVueAdd(B(_t("archive.message_3a4a165a145b"), 'x-install', 'plus', 'primary'), ' '), B(_t("archive.message_dd799f925f94"), 'domain-add', 'globe')), 'replace');
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
      const a = location.hash.replace(/^#\/?/, '').split('/').map((v) => {
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
		x.runtimeTab = ['overview', 'nodes', 'services', 'events', 'resources'].includes(a[2]) ? a[2] : 'overview';
        return;
      }
      if (a[0] === 'service' && ST(a[1])) {
        ui.page = 'service';
        x.service = a[1];
        x.serviceTab = ['instances', 'events', 'logs', 'config', 'routes'].includes(a[2]) ? a[2] : 'instances';
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
            x.serviceTab = ['instances', 'events', 'logs', 'config', 'routes'].includes(a[6]) ? a[6] : 'instances';
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
      return db.services.filter((s) => {
        if (s.status === 'REMOVED' && !x.includeRemoved) return false;
        if (x.svcScope === 'environment' && (s.project !== db.project || s.env !== db.env)) return false;
        if (x.svcScope === 'project' && s.project !== db.project) return false;
        if (x.svcProject !== 'all' && !allProjectView() && s.project !== x.svcProject) return false;
        if (x.svcRuntime !== 'all' && s.runtimeId !== x.svcRuntime) return false;
        const st = serviceState(s);
        if (x.svcHealth === 'issue' && !['issue', 'unknown'].includes(st.key)) return false;
        if (x.svcHealth === 'running' && !['healthy', 'running', 'complete'].includes(st.key)) return false;
        if (x.svcHealth === 'external' && s.managed) return false;
        return __nscVueInterpolate(["", " ", " ", " ", ""], [_serviceName(db.services, s.id), s.id, APP(s.app)?.name || '', s.image]).toLowerCase().includes(x.svcSearch.toLowerCase());
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
      const page = __applicationsViews.servicePage_56(__renderContext1, ...args);
      const service = ST(x.service);
      const app = APP(service?.app);
      if (!service || !app || !service.managed || service.system || app.system) return page;
      const attrs = kind => __nscVueInterpolate(["data-id=\"", "\" data-kind=\"", "\""], [service.id, kind]);
      const actions = __nscVueAdd(service.workloadKind === 'replicated' ? __nscVueAdd(
        B(_t('serviceOps.scale'), 'x-op-form', 'layers', '', attrs('scale')),
        B(_t('serviceOps.stop'), 'x-op-form', 'pause', '', attrs('stop'))) : '',
        B(_t('serviceOps.remove'), 'x-op-form', 'trash', '', attrs('remove_runtime')));
      let result = insertView(page, '.hero-actions', actions);
      if (service.removedAt && service.status !== 'REMOVED') {
        const notice = __nscVueAdd(callout(_t('serviceOps.removedHint'), 'amber'),
          B(_t('serviceOps.redeploy'), 'x-app', 'box', '', __nscVueInterpolate(["data-id=\"", "\""], [app.id])));
        result = insertView(result, '.x-head', notice, 'after');
      }
      return result;
    }
    function instanceTable(...args) {
      return __applicationsViews.instanceTable_57(__renderContext1, ...args);
    }
    function instanceDetail(id) {
      const t = IS(id);
      if (!t) return;
      const s = ST(t.service),
        prev = t.previous ? IS(t.previous) : null,
        next = t.replacement ? IS(t.replacement) : db.instances.find((i) => i.previous === t.id);
      x.selectedInstance = id;
      showModal(_modal(() => ({
        kind: 'x-instance',
        size: 'xwide',
        title: __nscVueAdd(_t("archive.message_0b94a3412ab4"), statusCN[t.state]),
        sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(_serviceName(db.services, t.service), ' / '), NODE(t.node)?.name || t.node), _t("archive.message_ecd8d607a9df")),
        body: __nscVueView(templates[315], [t.kind === 'task' ? 'Task ID' : 'Container ID', esc(t.id), esc(t.containerId), badge(statusCN[t.state], t.state === 'FAILED' ? 'red' : 'blue'), t.state, t.exitCode === null ? _t("archive.message_c921e5e203d2") : t.exitCode, t.oomKilled === null ? _t("community.evidence_state.UNKNOWN") : t.oomKilled, esc(t.cause), t.restartCount, t.release ? TB(esc(t.release), 'x-deployment', '', __nscVueInterpolate(["data-id=\"", "\""], [t.release])) : _t("archive.message_35f64c0b71cf"), t.oomKilled === true ? _t("archive.message_103548232e77") : _t("archive.message_393a154111c7"), prev ? __nscVueView(templates[311], [TB(esc(prev.id), 'x-instance', '', __nscVueInterpolate(["data-id=\"", "\""], [prev.id])), statusCN[prev.state], prev.finished]) : '', t.state === 'FAILED' ? 'fail' : 'ok', t.kind === 'task' ? ' Task' : _t("cluster.kind.container"), esc(t.id), t.started, t.finished || _t("archive.message_8086898af87b"), next ? __nscVueView(templates[312], [TB(esc(next.id), 'x-instance', '', __nscVueInterpolate(["data-id=\"", "\""], [next.id])), statusCN[next.state], next.started]) : '', t.logs ? __nscVueView(templates[313], [esc(t.logs)]) : __nscVueView(templates[314], [])]),
        footer: __nscVueAdd(B(_t("console.close"), 'close'), B(_t("archive.message_ab3d0a436bda"), 'x-service-events', 'history', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [t.service])))
      })));
    }
    function clusterSummary(r) {
      const managers = r.nodes.filter((n) => n.role === 'manager'),
        reachable = managers.filter((n) => ['Leader', 'Reachable'].includes(n.manager)).length;
      const ns = r.nodes.filter((n) => n.status === 'Ready').length;
      return {
        managers,
        reachable,
        total: r.nodes.length,
        ready: ns,
        services: db.services.filter((s) => s.status !== 'REMOVED' && s.runtimeId === r.id),
        apps: db.apps.filter((a) => a.runtime === r.id),
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
      if (hooks.data.timelinePage && ui.page === 'application' && x.appTab === 'events') return hooks.data.timelinePage({ applicationID: x.app });
      if (hooks.data.timelinePage && ui.page === 'service' && x.serviceTab === 'events') return hooks.data.timelinePage({ serviceID: x.service });
      return __applicationsViews.eventsTable_61(__renderContext1, ...args);
    }
    function runtimeEventsPage(...args) {
      return hooks.data.timelinePage ? hooks.data.timelinePage() : __applicationsViews.runtimeEventsPage_62(__renderContext1, ...args);
    }
    function eventDetail(id) {
      const e = db.runtimeEvents.find((i) => i.id === id);
      if (!e) return;
      const old = IS(e.task),
        next = IS(e.replacement);
      showModal(_modal(() => ({
        kind: 'x-event',
        size: 'xwide',
        title: e.title,
        sub: __nscVueAdd(__nscVueAdd(e.id, ' · '), scopeName(e.project, e.env)),
        body: __nscVueView(templates[335], [esc(e.evidence), e.confidence === 'confirmed' ? _t("archive.message_97b4712e95e3") : e.confidence === 'inferred' ? _t("archive.message_8fb0007db6a2") : _t("archive.message_f7b999182e0e"), old || next ? __nscVueView(templates[333], [old ? __nscVueView(templates[331], [old.state === 'FAILED' ? 'fail' : '', statusCN[old.state], TB(esc(old.id), 'x-instance', '', __nscVueInterpolate(["data-id=\"", "\""], [old.id])), esc(NODE(old.node)?.name), old.exitCode]) : '', next ? __nscVueView(templates[332], [statusCN[next.state], TB(esc(next.id), 'x-instance', '', __nscVueInterpolate(["data-id=\"", "\""], [next.id])), esc(NODE(next.node)?.name), next.started]) : '']) : '', e.time.slice(11), e.category === 'fault' ? 'red' : 'amber', e.category === 'gap' ? _t("archive.message_e3d379ebf282") : _t("archive.message_99ce53030ee7"), esc(e.evidence), next ? __nscVueView(templates[334], [(e.scheduleTime || next?.started || e.time).slice(11), e.end?.slice(11) || '']) : '', eventLabel(e), esc(e.actor), esc(_serviceName(db.services, e.service) || _t("archive.message_2746d995801b")), e.release ? TB(esc(e.release), 'x-deployment', '', __nscVueInterpolate(["data-id=\"", "\""], [e.release])) : _t("archive.message_c3b2d4398602"), esc(e.detail)]),
        footer: __nscVueAdd(B(_t("console.close"), 'close'), e.service ? B(_t("archive.message_707c48b3b3ab"), 'x-service', 'right', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [e.service])) : B(_t("archive.message_b89253ab665f"), 'x-runtime-events', 'right', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [e.runtime])))
      })));
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
        swarmRaw: t.swarmRaw || '',
        catalogVariables: t.catalogVariables || [],
        runtimeEnv: '',
        name: t.name,
        key: t.key,
        project: db.project,
        env: db.env,
        runtime: '',
        singleMode: false,
        raw: source === 'template' ? t.raw || SAMPLE_COMPOSE : SAMPLE_COMPOSE,
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
        x.wizard = reactive(createWizard(source, template));
        const compatible = db.runtimes.filter((r) => r.allowedEnvs.includes(db.env));
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
        errors: [_t("archive.message_45f45af9c866")]
      };
      if (!/^services:\s*(?:#.*)?$/m.test(raw)) errors.push(_t("archive.message_42eb2f2c1386"));
      if (/\t/.test(raw)) errors.push(_t("archive.message_8fd82bfe7a00"));
      if (/[&*][A-Za-z_-]|!include|^include:|^extends:/m.test(raw)) errors.push(_t("archive.message_0fdffaa8a095"));
      if (/(?:PASSWORD|TOKEN|SECRET|API_KEY)\s*[:=]\s*[^\s$#]/i.test(raw)) errors.push(_t("archive.message_c655bfe8a67f"));
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
          if (services.some((s) => s.name === sm[1])) errors.push(__nscVueAdd(_t("archive.message_0c214935e029"), sm[1]));
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
        if (!/^(services:\s*(?:#.*)?|  [A-Za-z0-9_-]+:\s*(?:#.*)?|    image:\s*.+|    expose:\s*(?:#.*)?|      -\s*[\"']?\d+(?:\/tcp)?[\"']?\s*)$/.test(line)) errors.push(__nscVueAdd(__nscVueAdd(_t("archive.message_9552fe3bd366"), __nscVueAdd(i, 1)), _t("archive.message_101f30b84f3a")));
      }
      if (!services.length) errors.push(_t("archive.message_bb82e58116bc"));
      if (services.length > 12) errors.push(_t("archive.message_57b7fb3203d5"));
      for (const s of services) if (!s.image) errors.push(__nscVueAdd(s.name, _t("archive.message_ffc2e90310d1")));
      return {
        services,
        errors
      };
    }
    function wizardServices(w) {
      if (w.source === 'template') return NSCWorkspace?.importCompose ? NSCWorkspace.importCompose(composeSource(w), w).services : summarizeCompose(composeSource(w)).services;
      if (w.source === 'image') return [{
        name: w.key || 'main',
        image: w.image,
        port: Number(w.port) || 0,
        http: Number(w.port) > 0
      }];
      return NSCWorkspace?.importCompose ? NSCWorkspace.importCompose(w.raw, w).services : summarizeCompose(w.raw).services;
    }
    function collectWizard() {
      const w = x.wizard;
      if (!w) return;
      document.querySelectorAll('[data-wf]').forEach((el) => {
        const k = el.dataset.wf;
        if (el.type === 'checkbox') w[k] = el.checked;else if (['port', 'replicas', 'memory', 'routeService'].includes(k)) w[k] = Number(el.value);else w[k] = el.value;
      });
    }
    function chooseCompatibleRuntime(w) {
      const rs = db.runtimes.filter((r) => r.allowedEnvs.includes(w.env));
      if (!rs.some((r) => r.id === w.runtime)) w.runtime = rs[0]?.id || '';
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
      if (!w.name.trim() || w.name.length > 64) errors.push(_t("archive.message_2ccb12ccc97b"));
      if (!/^[a-z][a-z0-9-]{1,47}$/.test(w.key)) errors.push(_t("archive.message_503d76f038f5"));
      if (db.apps.some((a) => a.project === w.project && a.env === w.env && a.key === w.key)) errors.push(_t("archive.message_d02f0666f578"));
      if (!P(w.project) || E(w.env)?.project !== w.project) errors.push(_t("archive.message_e81f1948c740"));
      if (!r?.allowedEnvs.includes(w.env)) errors.push(_t("archive.message_d8327966420e"));
      const gate = runtimeGate(r);
      if (gate) errors.push(gate);
      if (w.source === 'template' && !t.modes.includes(mode)) errors.push(__nscVueAdd(__nscVueAdd(_t("archive.message_ee723a803895"), MODE[mode]), _t("archive.message_df2603023914")));
      if (w.source === 'image' && !/^[a-zA-Z0-9][a-zA-Z0-9_.\/:@-]+$/.test(w.image)) errors.push(_t("archive.message_0b804ba02a4d"));
      if (w.source === 'file') {
        const compiled = NSCWorkspace.importCompose(w.raw, w);
        errors.push(...compiled.errors);
        warnings.push(...compiled.result.warnings);
        if (/docker\.sock/.test(w.raw)) errors.push(_t("archive.message_ab1c9ac90cf8"));
      }
      if (!Number.isInteger(w.memory) || w.memory < 64 || w.memory > 65536) errors.push(_t("archive.message_05ef1f2cd558"));
      if (!Number.isInteger(w.replicas) || w.replicas < 1 || w.replicas > 20) errors.push(_t("archive.message_af0bb4dc8c39"));
      if (w.source === 'image' && (!Number.isInteger(w.port) || w.port < 0 || w.port > 65535)) errors.push(_t("archive.message_74eb09a31c7f"));
      if (mode === 'container' && ss.length > 1) errors.push(_t("archive.message_3051780acdc0"));
      if (mode === 'container' && w.replicas !== 1) errors.push(_t("archive.message_87a4dd152611"));
      if (w.source === 'template' && t.secret && !w.secretRef) errors.push(_t("archive.message_ddd5d842d129"));
      if (w.source === 'template' && t.stateful && !w.storageAck) errors.push(_t("archive.message_343232273e2d"));
      if (w.source === 'template' && t.stateful && w.replicas !== 1) errors.push(_t("archive.message_c28bbaae1f37"));
      if (w.access === 'domain') {
        if (!ss[w.routeService]?.http) errors.push(_t("archive.message_035b4e1b503c"));
        if (!validHost(w.domain.toLowerCase().trim())) errors.push(_t("archive.message_e719d2571beb"));
        if (!w.gateway || !E(w.env)?.gateways.includes(w.gateway)) errors.push(_t("archive.message_704e98735745"));
        const conflict = domainConflict(w.domain.toLowerCase().trim(), [w.gateway]);
        if (conflict) errors.push(__nscVueAdd(__nscVueAdd(_t("archive.message_70f6f172a7e2"), scopeText(conflict)), _t("archive.message_ce7ca5546346")));
        warnings.push(_t("archive.message_ee7366028bdf"));
      }
      if (ss.some((s) => !s.image.includes('@sha256:'))) warnings.push(_t("archive.message_272db5a3918d"));
      if (mode === 'compose' || mode === 'container') warnings.push(_t("archive.message_1ed12fdf2a19"));
      if (w.source === 'template' && t.stateful) warnings.push(_t("archive.message_78b0479872aa"));
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
      const titles = [_t("archive.message_46e5eb29c7d4"), _t("archive.message_6dc1c087283d"), _t("archive.message_67b0b7d14f06"), _t("archive.message_2cb211a78083")];
      let inner = '';
      if (w.step === 1) inner = __nscVueView(templates[344], [__nscVueJoin([['template', _t("archive.message_e04d8f25c4d0"), _t("archive.message_e7097778c938"), 'grid'], ['file', _t("archive.message_432866e592b3"), _t("archive.message_278cec83afd1"), 'file'], ['image', _t("archive.message_0318ec3e3a1e"), _t("archive.message_4ee0b114af3c"), 'box']].map(([k, n, d, i]) => __nscVueView(templates[339], [w.source === k ? 'active' : '', k, I(i), n, d])), ''), esc(w.name), esc(w.key), w.source === 'template' ? __nscVueView(templates[341], [__nscVueJoin(TEMPLATES.map((t) => __nscVueView(templates[340], [t.id, t.id === w.template ? 'selected' : '', t.name, t.services.length])), ''), esc(tpl(w.template).notes)]) : w.source === 'file' ? __nscVueView(templates[342], [B(_t("archive.message_a146f0457cab"), 'x-wsample', 'file', 'sm'), esc(w.raw)]) : __nscVueView(templates[343], [esc(w.image), w.port])]);
      if (w.step === 2) inner = __nscVueView(templates[351], [__nscVueJoin(db.projects.map((p) => __nscVueView(templates[345], [p.id, p.id === w.project ? 'selected' : '', esc(p.name)])), ''), __nscVueJoin(projectEnvs(w.project).map((e) => __nscVueView(templates[346], [e.id, e.id === w.env ? 'selected' : '', esc(e.name)])), ''), __nscVueJoin(db.runtimes.filter((r) => r.allowedEnvs.includes(w.env)).map((r) => __nscVueView(templates[347], [r.id, r.id === w.runtime ? 'selected' : '', esc(r.name), r.type === 'swarm' ? _t("nav.cluster") : _t("archive.message_a86b9e40562b"), runtimeGate(r) ? _t("archive.message_445cdece2cef") : ''])), ''), I(r?.type === 'swarm' ? 'layers' : 'server'), MODE[mode], mode === 'stack' ? _t("archive.message_a604ddc5fdc2") : mode === 'swarmservice' ? _t("archive.message_a7851774152e") : mode === 'container' ? _t("archive.message_101ab90f7af6") : _t("archive.message_ce8d4cb76d2b"), w.source === 'template' && r?.type === 'docker' && ss.length === 1 && tpl(w.template).modes.includes('container') ? __nscVueView(templates[348], [w.singleMode ? 'checked' : '']) : '', r ? __nscVueView(templates[350], [runtimeGate(r) || _t("archive.message_dcd3d3d96066"), r.type === 'swarm' && w.source === 'template' && !tpl(w.template).modes.includes(mode) ? __nscVueView(templates[349], []) : '']) : '']);
      if (w.step === 3) inner = __nscVueView(templates[358], [ss.length, __nscVueJoin(ss.map((s) => __nscVueView(templates[352], [esc(s.name), esc(s.image), s.port || _t("archive.message_db7681b55987")])), ''), w.replicas, mode === 'container' || w.source === 'file' ? 'disabled' : '', w.source === 'file' ? _t("archive.message_414248303926") : _t("archive.message_9478962dd2c7"), w.memory, w.source === 'file' ? 'disabled' : '', w.source === 'template' && tpl(w.template).secret ? __nscVueView(templates[353], [w.env, w.secretRef ? 'selected' : '', esc(envName(w.env))]) : '', w.source === 'template' && tpl(w.template).stateful ? __nscVueView(templates[354], [w.storageAck ? 'checked' : '']) : '', w.access === 'internal' ? 'selected' : '', w.access === 'domain' ? 'selected' : '', !ss.some((s) => s.http) ? 'disabled' : '', w.access === 'domain' ? __nscVueView(templates[357], [esc(w.domain), __nscVueJoin(ss.map((s, i) => s.http ? __nscVueView(templates[355], [i, i === w.routeService ? 'selected' : '', esc(s.name), s.port]) : ''), ''), __nscVueJoin((E(w.env)?.gateways || []).map((id) => __nscVueView(templates[356], [id, id === w.gateway ? 'selected' : '', esc(G(id)?.name)])), ''), callout(_t("archive.message_c50d190e8ada"), 'blue')]) : '']);
      if (w.step === 4) {
        const q = wizardIssues(w);
        if (w.source === 'file' && !w.reviewAck) w.parameterReview = NSCWorkspace.installVersion(w);
        inner = __nscVueView(templates[362], [esc(scopeName(w.project, w.env)), esc(w.name), badge(E(w.env)?.kind === 'production' ? _t("archive.message_3aeb52c742e9") : _t("archive.message_7a0885805267"), E(w.env)?.kind === 'production' ? 'amber' : 'blue'), I('checkcircle'), MODE[mode], esc(r?.name), ss.length, w.source === 'file' ? _t("archive.message_d419f36ef145") : __nscVueAdd(__nscVueAdd(__nscVueAdd(mode === 'container' ? 1 : w.replicas, _t("archive.message_369bcd6848cd")), w.memory), _t("archive.message_f4446890a52a")), I('checkcircle'), w.access === 'internal' ? _t("ports.noDomain") : __nscVueAdd(__nscVueAdd(_t("archive.message_fa34bf50b902"), esc(w.domain)), _t("archive.message_ab972479b82e")), __nscVueJoin(q.errors.map((m) => __nscVueView(templates[359], [I('warning'), esc(m)])), ''), __nscVueJoin(q.warnings.map((m) => __nscVueView(templates[360], [I('info'), esc(m)])), ''), w.source === 'file' ? NSCWorkspace.installReviewHTML(w) : '', esc(w.source === 'file' ? w.raw : previewWizard(w)), w.reviewAck ? 'checked' : '', q.warnings.length ? __nscVueView(templates[361], [w.warningAck ? 'checked' : '']) : '']);
      }
      const issues = w.step === 4 ? wizardIssues(w) : null;
      showModal(_modal(() => ({
        kind: 'x-wizard',
        size: 'xwide',
        title: _t("archive.message_3a4a165a145b"),
        sub: _t("archive.message_1323547b655f"),
        body: __nscVueView(templates[365], [__nscVueJoin(titles.map((n, i) => __nscVueView(templates[363], [w.step === __nscVueAdd(i, 1) ? 'active' : w.step > __nscVueAdd(i, 1) ? 'done' : '', w.step > __nscVueAdd(i, 1) ? '✓' : __nscVueAdd(i, 1), n])), ''), inner, x.wizardError ? __nscVueView(templates[364], [callout(esc(x.wizardError), 'red')]) : '', esc(w.name), esc(P(w.project)?.name), esc(envName(w.env)), esc(r?.name || _t("archive.message_04fbe1f84206")), MODE[mode], ss.length]),
        footer: __nscVueAdd(__nscVueAdd(__nscVueAdd(B(_t("console.cancel"), 'close', '', 'ghost'), B(_t("archive.message_efe13b34ae4e"), 'x-wsave', 'save')), w.step > 1 ? B(_t("archive.message_da336fdc0dbd"), 'x-wback', 'back') : ''), B(w.step === 4 ? _t("archive.message_5f221a7521e8") : _t("console.next"), w.step === 4 ? 'x-wdeploy' : 'x-wnext', w.step === 4 ? 'send' : 'right', 'primary', __nscVueInterpolate(["id=\"x-w-primary\" ", ""], [(w.step === 4 && (issues.errors.length || !w.reviewAck || issues.warnings.length && !w.warningAck || !deploymentReady(w) || !composeSession(w).review || composeSession(w).reviewPending) || w.step === 1 && w.source === 'file' && !validateComposeDocument(w.raw).schema_valid) ? 'disabled' : ''])))
      })));
    }
    function previewWizard(w) {
      if (w.source === 'image' || w.source === 'template') return composeSession(w).review?.preview?.resolved_yaml || composeSource(w);
      return __nscVueAdd(__nscVueAdd(__nscVueInterpolate(["# ", " \xB7 ", "\n# ", " \u2192 ", "\nservices:\n"], [w.name, scopeName(w.project, w.env), MODE[wizardMode(w)], RT(w.runtime)?.name]), __nscVueJoin(wizardServices(w).map((s) => __nscVueInterpolate(["  ", ":\n    image: ", "\n", "    # replicas: ", "; memory: ", " MiB\n"], [s.name, s.image, s.port ? __nscVueAdd(__nscVueAdd('    expose: ["', s.port), '"]\n') : '', w.replicas, w.memory])), '')), w.secretRef ? __nscVueAdd(__nscVueAdd('# secret reference: ', w.secretRef), '\n') : '');
    }
    function nextWizard() {
      collectWizard();
      const w = x.wizard;
      if (w.step === 1) {
        if (!w.name.trim() || !/^[a-z][a-z0-9-]{1,47}$/.test(w.key)) {
          x.wizardError = _t("archive.message_b07da6167d1a");
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
          x.wizardError = _t("archive.message_861822702af5");
          return renderWizard();
        }
        const gate = runtimeGate(RT(w.runtime));
        if (gate) {
          x.wizardError = gate;
          return renderWizard();
        }
        if (w.source === 'template' && !tpl(w.template).modes.includes(wizardMode(w))) {
          x.wizardError = _t("archive.message_e445cbecd873");
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
      return hooks.data.command("saveWizard", []);
    }
    function specText(...args) {
      return __applicationsViews.specText_66(__renderContext1, ...args);
    }
    function writeGate(runtime, app = null) {
      if (ui.externalConflict) {
        toast(_t("archive.message_f8db087a868e"), true);
        return false;
      }
      if (deploymentTimer) {
        toast(_t("archive.message_9a50c41234d8"), true);
        return false;
      }
      const reason = runtimeGate(RT(runtime));
      if (reason) {
        toast(reason, true);
        return false;
      }
      if (app && !app.managed) {
        toast(_t("archive.message_78c12d7b2ba4"), true);
        return false;
      }
      return true;
    }
    function newInstance(s, slot, release, previous = null, cause = '发布更新') {
      return hooks.data.unsupported("newInstance", formError);
    }
    function appendEvent(s, category, title, detail, extra = {}) {
      return hooks.data.unsupported("appendEvent", formError);
    }
    function applyServiceSnapshot(s, desired, op) {
      return hooks.data.unsupported("applyServiceSnapshot", formError);
    }
    function deployWizard() {
      return hooks.data.command("deployWizard", arguments);
    }
    function maybeCreateAccessDraft(a) {
      return hooks.data.unsupported("maybeCreateAccessDraft", formError);
    }
    function createOperation(a, action, kind, spec, targets, previous) {
      return hooks.data.unsupported("createOperation", formError);
    }
    function runAttempt(op, targets) {
      return hooks.data.unsupported("runAttempt", formError);
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
      showModal(_modal(() => ({
        kind: 'x-deployment',
        size: 'xwide',
        title: __nscVueAdd(__nscVueAdd(o.action, ' · '), st[0]),
        sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(o.id, ' · '), scopeName(o.project, o.env)), ' / '), a?.name || o.snapshot.name),
        locked: o.state === 'running',
        body: __nscVueView(templates[372], [o.version, esc(RT(o.runtime)?.name), badge(...st), __nscVueJoin([_t("archive.message_5fbc2d6e4ce3"), _t("archive.message_8157fc174a65"), _t("archive.message_e21097c3ee96"), _t("archive.message_dffce81a1237")].map((t, i) => __nscVueView(templates[366], [o.stages > i ? 'done' : '', __nscVueAdd(i, 1), t])), ''), __nscVueJoin(o.targets.map((sid) => __nscVueView(templates[367], [esc(_serviceName(db.services, sid)), badge({
          success: _t("archive.message_173e9809e175"),
          failed: _t("archive.message_2fb4d1e3bd8e"),
          unknown: _t("archive.message_fb7e71452c04"),
          pending: _t("archive.message_4f021f59121c")
        }[o.results[sid]] || _t("community.evidence_state.UNKNOWN"), o.results[sid] === 'success' ? 'green' : o.results[sid] === 'failed' ? 'red' : 'amber'), o.version, ST(sid)?.observedVersion ? __nscVueAdd('r', ST(sid).observedVersion) : _t("archive.message_a0ab546f6ac1"), o.state !== 'running' ? TB(_t("archive.message_3bb9cb84250b"), 'x-service', 'right', __nscVueInterpolate(["data-id=\"", "\""], [sid])) : ''])), ''), o.state === 'unknown' ? __nscVueView(templates[368], [callout(_t("archive.message_c7f75df76164"), 'amber')]) : '', !latest ? __nscVueView(templates[369], [callout(_t("archive.message_f0c3f848b951"), 'amber')]) : '', a?.access ? __nscVueView(templates[370], [callout(a.access.created ? __nscVueAdd(__nscVueAdd(_t("archive.message_b1ef9a063008"), esc(a.access.domain)), _t("archive.message_35d738f17f4b")) : a.access.error ? esc(a.access.error) : _t("archive.message_29b3442f134c"), 'blue')]) : '', o.attempts.length, __nscVueJoin(o.attempts.map((at) => __nscVueView(templates[371], [at.number === o.attempts.length ? 'open' : '', at.number, at.time, esc(at.state), esc(__nscVueJoin(at.logs, '\n'))])), ''), esc(specText(o.snapshot))]),
        footer: __nscVueAdd(__nscVueAdd(__nscVueAdd(B(o.state === 'running' ? _t("archive.message_d44996326e60") : _t("console.close"), 'close', '', '', o.state === 'running' ? 'disabled' : ''), canRetry ? B(_t("archive.message_f70018000469"), 'x-retry', 'refresh', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [o.id])) : ''), o.state === 'unknown' ? B(_t("archive.message_7a38cb0fac52"), 'x-verify', 'search', 'primary', __nscVueInterpolate(["data-id=\"", "\" ", ""], [o.id, g ? 'disabled' : ''])) : ''), o.state !== 'running' ? B(_t("archive.message_87d504f157cc"), 'x-app', 'right', '', __nscVueInterpolate(["data-id=\"", "\""], [o.app])) : '')
      })));
    }
    function retryOperation(id) {
      return hooks.data.unsupported("retryOperation", formError);
    }
    function verifyOperation(id) {
      const o = OP(id);
      if (!o || o.state !== 'unknown' || runtimeGate(RT(o.runtime))) return;
      showModal(_modal(() => ({
        kind: 'x-verify',
        size: 'small',
        title: _t("archive.message_3ac308b91b9d"),
        sub: o.id,
        body: callout(_t("archive.message_b12110cd48a5"), 'blue'),
        footer: __nscVueAdd(__nscVueAdd(B(_t("archive.message_c59f728f0ed4"), 'close'), B(_t("archive.message_468588230fa6"), 'x-verify-result', '', '', __nscVueInterpolate(["data-id=\"", "\" data-result=\"failed\""], [id]))), B(_t("archive.message_3d58333aed7b"), 'x-verify-result', 'checkcircle', 'primary', __nscVueInterpolate(["data-id=\"", "\" data-result=\"success\""], [id])))
      })));
    }
    function verifiedResult(id, result) {
      return hooks.data.unsupported("verifiedResult", formError);
    }
    function opForm(sid, kind) {
      const s = ST(sid),
        a = APP(s?.app);
      if (!s || !a || !writeGate(s.runtimeId, a)) return;
      if (s.system || a.system || !s.managed || kind !== 'remove_runtime' && s.workloadKind === 'job' || ['scale', 'stop'].includes(kind) && (s.mode === 'container' || s.workloadKind !== 'replicated')) return toast(_t("archive.message_7032eb9bb62f"), true);
      if (s.removedAt && kind === 'restart') return toast(_t('serviceOps.removedHint'), true);
      x.pendingOp = {
        service: sid,
        kind,
        base: a.version
      };
      const restartLabel = RT(s.runtimeId).type === 'swarm' ? _t("archive.message_5466c072293c") : _t("archive.message_f7d763a12cbf");
      const persistence = new View([h('fieldset', { class: 'service-persist-choice' }, [
        h('legend', _t('serviceOps.persistQuestion')),
        h('label', [h('input', { type: 'radio', name: 'x-op-persist', value: 'runtime', checked: !s.removedAt, disabled: !!s.removedAt }), _t('serviceOps.runtimeOnly')]),
        h('label', [h('input', { type: 'radio', name: 'x-op-persist', value: 'source', checked: !!s.removedAt }), _t('serviceOps.persistCompose')]),
      ])]);
      const acknowledgement = new View([h('label', { class: 'service-op-ack' }, [
        h('input', { id: 'x-op-ack', type: 'checkbox' }), _t('serviceOps.confirm'),
      ])]);
      const specialBody = kind === 'remove_runtime'
        ? __nscVueAdd(__nscVueAdd(callout(_t('serviceOps.removeWarning'), 'amber'), callout(_t('serviceOps.bindingHint'), 'blue')), acknowledgement)
        : __nscVueAdd(__nscVueAdd(callout(_t('serviceOps.stopWarning'), 'amber'), persistence), acknowledgement);
      const scaleBody = __nscVueAdd(callout(_t("archive.message_2a0d9597512e"), 'blue'), new View([
        h('label', { class: 'field mt16', for: 'x-op-value' }, [
          h('span', { class: 'field-label' }, _t('serviceOps.scale')),
          h('input', { id: 'x-op-value', type: 'number', min: '0', max: '10000', value: s.desired }),
        ]), ...nodes(persistence), ...nodes(acknowledgement),
      ]));
      showModal(_modal(() => ({
        kind: 'x-op-form',
        size: 'small',
        title: kind === 'image' ? _t("archive.message_35720eddb2e6") : kind === 'scale' ? _t("archive.message_7361d7463742") : kind === 'stop' ? _t('serviceOps.stop') : kind === 'remove_runtime' ? _t('serviceOps.remove') : restartLabel,
        sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(scopeName(s.project, s.env), ' / '), a.name), ' / '), _serviceName(db.services, s.id)),
        body: ['stop', 'remove_runtime'].includes(kind) ? specialBody : kind === 'scale' ? scaleBody : __nscVueView(templates[373], [callout(kind === 'restart' ? RT(s.runtimeId).type === 'swarm' ? _t("archive.message_936341568fd2") : _t("archive.message_8b3138b0c087") : _t("archive.message_2a0d9597512e"), 'blue'), kind === 'image' ? _t("archive.message_c1020d669207") : _t("archive.message_d30c83cc43c0"), 'text', kind === 'image' ? esc(s.image) : '', kind === 'restart' ? _t("archive.message_bc29a90cb4db") : '', s.stateful ? _t("archive.message_49f1cc37800f") : '']),
        footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(kind === 'remove_runtime' ? _t('serviceOps.remove') : _t("archive.message_5401c70a6a87"), 'x-op-confirm', 'send', 'primary'))
      })));
    }
    function confirmOp() {
      return hooks.data.command("confirmOp", arguments);
    }
    function restoreApp(id, v) {
      const a = APP(id),
        rev = a?.revisions.find((r) => r.v === Number(v));
      if (!a || !rev || !writeGate(a.runtime, a)) return;
      x.restore = {
        app: id,
        version: Number(v),
        base: a.version
      };
      showModal(_modal(() => ({
        kind: 'x-restore',
        size: 'xwide',
        title: _t("archive.message_4692d82a869c"),
        sub: __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(a.name, ' · r'), a.version), _t("archive.message_5933adc63d15")), v),
        body: __nscVueView(templates[374], [callout(_t("archive.message_d75237563765"), 'amber'), esc(specText(appSpec(a))), esc(specText(rev.spec))]),
        footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("archive.message_8cd2161dc200"), 'x-restore-save', 'edit', 'primary'))
      })));
    }
    function saveRestore() {
      return hooks.data.command("saveRestore", arguments);
    }
    function reviewRestore(id) {
      const a = APP(id);
      if (!a?.draft) return;
      showModal(_modal(() => ({
        kind: 'x-restore-review',
        size: 'xwide',
        title: _t("archive.message_3208e9a23007"),
        sub: __nscVueAdd(__nscVueAdd(scopeName(a.project, a.env), ' / '), a.name),
        body: __nscVueView(templates[375], [callout(_t("archive.message_28b352d38314"), 'amber'), esc(specText(a.draft.spec))]),
        footer: __nscVueAdd(B(_t("archive.message_4d1638c82c8e"), 'close'), B(_t("archive.message_5f221a7521e8"), 'x-restore-deploy', 'send', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [id])))
      })));
    }
    function deployRestore(id) {
      return hooks.data.command("deployRestore", arguments);
    }
    function templateInfo(...args) {
      return __applicationsViews.templateInfo_67(__renderContext1, ...args);
    }
    function runtimeInfo(...args) {
      return __applicationsViews.runtimeInfo_68(__renderContext1, ...args);
    }
    function nodeInfo(id) {
      const n = NODE(id),
        r = db.runtimes.find((r) => r.nodes.some((n) => n.id === id));
      if (!n || !r) return;
      showModal(_modal(() => ({
        kind: 'x-node',
        size: 'xwide',
        title: n.name,
        sub: __nscVueAdd(__nscVueAdd(r.name, ' · '), n.id),
        body: __nscVueView(templates[382], [r.connection === 'online' ? n.status : _t("community.evidence_state.UNKNOWN"), n.availability, n.role, n.manager || _t("archive.message_2746d995801b"), r.connection === 'online' && n.agent ? _t("archive.message_58e53743cc79") : _t("archive.message_969ce4f62ec2"), callout(_t("archive.message_c58e0466d303"), 'blue'), __nscVueJoin(db.instances.filter((t) => t.node === id && t.current).map((t) => __nscVueView(templates[380], [esc(_serviceName(db.services, t.service)), TB(_t("archive.message_3bb9cb84250b"), 'x-instance', 'right', __nscVueInterpolate(["data-id=\"", "\""], [t.id]))])), '') || __nscVueView(templates[381], [])]),
        footer: __nscVueAdd(B(_t("console.close"), 'close'), B(_t("archive.message_a00d06786441"), 'x-runtime-events', 'history', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [r.id])))
      })));
    }
    function connectForm() {
      showModal(_modal(() => ({
        kind: 'x-connect',
        size: 'small',
        title: _t("archive.message_65242153c831"),
        sub: _t("archive.message_c107a4ca3fec"),
        body: __nscVueView(templates[384], [callout(_t("archive.message_2861f3a12279"), 'blue'), __nscVueJoin(db.environments.map((e) => __nscVueView(templates[383], [e.id, !allEnvironmentView() && e.id === db.env ? 'selected' : '', esc(scopeName(e.project, e.id))])), '')]),
        footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("archive.message_94f5df25c86c"), 'x-connect-save', 'plus', 'primary'))
      })));
    }
    function saveConnection() {
      return hooks.data.command("saveConnection", arguments);
    }
    function demoSettings() {
      showModal(_modal(() => ({
        kind: 'x-demo',
        size: 'small',
        title: _t("prototype.failover_demo"),
        sub: _t("archive.message_69b65062ce2d"),
        body: __nscVueView(templates[385], [x.scenario === 'success' ? 'selected' : '', x.scenario === 'partial' ? 'selected' : '', x.scenario === 'unknown' ? 'selected' : '', B(_t("archive.message_41228fd33912"), 'x-event', 'activity', '', 'data-id="INC-018"'), B(_t("archive.message_e9cf672ee2c5"), 'x-service', 'checkcircle', '', 'data-id="db-migrate"'), B(_t("archive.message_b8b0e44a7e45"), 'x-runtime-events', 'server', '', 'data-id="swarm-prod"')]),
        footer: __nscVueAdd(B(_t("console.close"), 'close'), B(_t("archive.message_ad2a5df1a761"), 'x-demo-save', 'checkcircle', 'primary'))
      })));
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
      if (!force && modal?.kind === 'x-wizard' && x.wizard && composeSession(x.wizard).editing) { window.dispatchEvent(new Event('nsc:close-compose-workspace')); return; }
      if (!force && modal?.kind === 'x-wizard' && x.wizard) {
        collectWizard();
        showModal(_modal(() => ({
          kind: 'x-wizard-exit',
          size: 'small',
          title: _t("archive.message_00de049443b6"),
          sub: _t("archive.message_b9957b786a21"),
          body: callout(_t("archive.message_470a3e7b2195"), 'amber'),
          footer: __nscVueAdd(__nscVueAdd(B(_t("archive.message_446f8327bf57"), 'x-wresume'), B(_t("archive.message_498256ff6143"), 'x-wdiscard', '', 'danger')), B(_t("archive.message_efe13b34ae4e"), 'x-wsave', 'save', 'primary'))
        })));
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
    };
    listen(document, 'click', (event) => {
      const el = event.target.closest('[data-action]');
      if (!el || el.disabled || !el.dataset.action.startsWith('x-')) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      const a = el.dataset.action,
        id = el.dataset.id;
      switch (a) {
        case 'x-nav':
          if (el.dataset.page === 'services') {
            x.svcHealth = el.dataset.health || 'all';
            x.svcPage = 1;
          }
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
        case 'x-removed-toggle':
          x.includeRemoved = !x.includeRemoved;
          x.svcPage = 1;
          updateHash();
          render();
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
          hooks.data.unsupported("x-runtime-toggle", formError);
          break;
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
          hooks.data.command("discardWizard", []);
          break;
        case 'x-resume-wizard':
          {
            const d = db.appDrafts.filter(inListScope).at(-1);
            if (d) {
              x.wizard = reactive(clone(d));
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
            historyCoverage: db.runtimes.map((r) => ({
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
          hooks.data.unsupported("x-demo-save", formError);
          break;
      }
    }, true);
    listen(document, 'change', (event) => {
      const el = event.target;
      if (el.isConnected && el.matches('[data-wf]')) {
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
          const e = db.environments.find((e) => e.project === w.project);
          w.env = e?.id || '';
          w.secretRef = '';
          chooseCompatibleRuntime(w);
        }
        if (k === 'env') {
          w.secretRef = '';
          chooseCompatibleRuntime(w);
        }
        if (k === 'template' && w.source === 'template') {
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
          if (!ss[w.routeService]?.http) w.routeService = ss.findIndex((s) => s.http);
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
        if (file.size > 2 * 1024 * 1024) return toast(_t("composeEditor.sizeLimit"), true);
        file.text().then((text) => {
          if (!x.wizard) return;
          x.wizard.raw = text;
          x.wizard.source = 'file';
          x.wizard.reviewAck = false;
          renderWizard();
        });
      }
    }, true);
    listen(document, 'input', (event) => {
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
      if (el.isConnected && el.matches('[data-wf]')) {
        collectWizard();
        if (x.wizard) {
          x.wizard.touched = true;
          if (!['reviewAck', 'warningAck'].includes(el.dataset.wf)) {
            x.wizard.reviewAck = false;
            x.wizard.warningAck = false;
          }
          // Keep rendered form values aligned with the draft before an async
          // validation or inventory refresh reuses this modal's VNodes.
          preserveWizardFocus = true;
          try { renderWizard(); } finally { preserveWizardFocus = false; }
        }
      }
    }, true);
    listen(document, 'keydown', (event) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's' && x.wizard) {
        event.preventDefault();
        event.stopImmediatePropagation();
        saveWizard();
      }
    }, true);
    listen(window, 'beforeunload', (event) => {
      if (x.wizard?.touched || deploymentTimer) {
        event.preventDefault();
        event.returnValue = '';
      }
    });
    // Test/read-only inspection hook; no network API and no credentials.
    NSCBridge = {
      renderWizard,
      wizardServices,
      previewWizard,
      wizardMode,
      wizardIssues,
      collectWizard,
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
      appState,
      serviceState,
      clusterSummary,
      createOperation,
      writeGate,
      collectWizard,
      beginInstall,
      templateInfo,
      templates: TEMPLATES,
      showDeployment,
      instanceDetail,
      eventDetail,
      demoSettings,
      setShowDeployment: (fn) => {
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
    const typelabel = _labels({
      string: () => _t("variables.text"),
      integer: () => _t("variables.integer"),
      boolean: () => _t("variables.boolean"),
      memory: () => _t("variables.memory"),
      credential: () => _t("variables.credential")
    });
    const label = {
      project: _t("prototype.project_default"),
      environment: _t("prototype.environment_override"),
      application: _t("variables.application_overrides")
    };
    const rec = (value, type = 'string', note = '') => ({
      value: String(value),
      type,
      note
    });
    const clean = (obj) => JSON.parse(JSON.stringify(obj));
    function seed() {
      return hooks.data.state;
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
      for (const [k, source] of [[__nscVueAdd('project:', project), _t("prototype.project_default")], [__nscVueAdd('environment:', env), _t("prototype.environment_override")], [__nscVueAdd('application:', app), _t("variables.application_overrides")]]) {
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
        source: _t("prototype.project_default"),
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
    function variableState() {
      const apps = db.apps.filter((a) => a.project === db.project && a.env === db.env);
      if (!apps.some((a) => a.id === Y.app)) Y.app = apps[0]?.id || '';
      const key = scopeKey();
      return {
        layer: Y.layer,
        app: Y.app,
        apps: apps.map((a) => ({ id: a.id, name: a.name })),
        query: Y.query,
        version: scope(key).version,
        dirty: varsDirty(),
        ownCount: Object.keys(ownRows()).length,
        projectName: P()?.name || '',
        environmentName: E()?.name || '',
        rows: Object.entries(viewRows()).map(([name, value]) => ({
          name,
          value: value.value,
          type: value.type,
          note: value.note || '',
          source: value.source,
          own: value.scope === key,
          references: usage(name).length
        }))
      };
    }
    function variableEditor() {
      if (!Y.modalEdit) return null;
      return { key: Y.modalEdit.key, ...clean(Y.modalEdit.original) };
    }
    function docsDirty() {
      if (!Y.docApp) return false;
      const d = Y.serviceSourceDocument || db.parameters13.documents[Y.docApp];
      return !!d && ((Y.composeWork ?? d.source) !== (d.draft?.source ?? d.source) || (Y.runtimeWork ?? d.runtime) !== (d.draft?.runtime ?? d.runtime));
    }
    function ensureDoc(a) {
      return a ? db.parameters13.documents[a.id] : null;
    }
    function loadDoc(id) {
      const a = W.APP(id);
      const d = ensureDoc(a);
      if (!d) return;
      Y.docApp = id;
      Y.sourceServiceID = null;
      Y.serviceSourceDocument = null;
      Y.documentBaseRevision = d.draft?.base ?? a.revision;
      Y.sourceBaseRevisionID = d.draft?.sourceRevision || d.sourceRevision;
      Y.composeWork = d.draft?.source ?? d.source;
      Y.runtimeWork = d.draft?.runtime ?? d.runtime;
      Y.previewEnv = a.env;
      Y.view = 'source';
      Y.file = 'compose';
    }
    function saveVars(silent = false) {
      return hooks.data.command("saveVars", arguments);
    }
    function saveDoc(silent = false) {
      return hooks.data.command("saveDoc", arguments);
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
        errors: [_t("archive.message_bf106a8a47d7")],
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
      for (const a of db.apps.filter((a) => a.project === db.project)) {
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
      showModal(_modal(() => ({
        kind: 'y-variable',
        size: 'drawer',
        title: key ? __nscVueAdd(_t("archive.message_6d40b73c79f4"), key) : _t("delivery.add_variable"),
        sub: __nscVueAdd(__nscVueAdd(label[Y.layer], ' · '), Y.layer === 'project' ? P().name : E().name),
        body: __nscVueView(templates[399], [esc(key), key ? 'readonly' : '', __nscVueJoin(Object.entries(typelabel).map(([k, n]) => __nscVueView(templates[398], [k, value.type === k ? 'selected' : '', n])), ''), value.type === 'credential' ? esc(value.value) : esc(value.value), esc(value.note || ''), callout(_t("archive.message_6a411e07f568"), 'blue')]),
        footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("variables.stage_change"), 'y-var-apply', 'check', 'primary'))
      })));
    }
    function applyVar(form) {
      const k = String(form?.key ?? document.getElementById('y-key').value).trim(),
        type = form?.type ?? document.getElementById('y-type').value,
        value = form?.value ?? document.getElementById('y-value').value,
        note = form?.note ?? document.getElementById('y-note').value;
      let error = '';
      if (!/^[A-Z_][A-Z0-9_]{0,95}$/.test(k)) error = _t("archive.message_236eb017524b");
      if (/^(DOCKER_|COMPOSE_|NSC_)/.test(k)) error = _t("archive.message_3d8293b4b13c");
      if (type === 'integer' && !/^-?\d+$/.test(value)) error = _t("archive.message_9f3c78c716c0");
      if (type === 'boolean' && !['true', 'false'].includes(value)) error = _t("archive.message_c669c771f4d3");
      if (type === 'memory' && !/^\d+(?:\.\d+)?[bkmg]?(?:b)?$/i.test(value)) error = _t("archive.message_7a61d0cd584e");
      if (type === 'credential' && !/^secret:\/\/[A-Za-z0-9_./-]+@[A-Za-z0-9_.-]+$/.test(value)) error = _t("archive.message_dade63d03c9b");
      if (/(PASSWORD|TOKEN|SECRET|PRIVATE_KEY)$/i.test(k) && type !== 'credential') error = _t("archive.message_b3c741036d01");
      if (value.includes('$(') || value.includes('${')) error = _t("archive.message_94e54a3414cf");
      if (value.length > 4096) error = _t("archive.message_a5974a7cdc43");
      if (!Y.modalEdit?.key && Object.hasOwn(ownRows(), k)) error = _t("archive.message_1dfce7eff232");
      if (error) {
        if (!form) region(document.getElementById('y-var-error'), callout(esc(error), 'red'));
        return error;
      }
      ensureDraft().rows[k] = {
        type,
        value,
        note
      };
      Y.modalEdit = null;
      closeModal(true);
      render();
      return '';
    }
    function removeVar(key) {
      const current = ownRows()[key];
      if (!current) return;
      Y.removeKey = key;
      const refs = usage(key);
      showModal(_modal(() => ({
        kind: 'y-remove',
        size: 'small',
        title: Y.layer === 'project' ? _t("archive.message_35956d93088d") : _t("archive.message_33eb9ddc1dc5"),
        sub: key,
        body: callout(Y.layer === 'project' ? __nscVueInterpolate([_t("archive.message_a6815db95ff5"), _t("archive.message_68c9cedea807")], [refs.length]) : _t("archive.message_17ca48ca65bb"), 'amber'),
        footer: __nscVueAdd(B(_t("archive.message_670ec25af841"), 'close'), B(_t("variables.stage_change"), 'y-var-remove-confirm', 'trash', 'danger', __nscVueInterpolate(["data-key=\"", "\""], [key])))
      })));
    }
    function usageDialog(key) {
      Y.usageKey = key;
      const items = usage(key);
      showModal(_modal(() => ({
        kind: 'y-usage',
        size: 'xwide',
        title: __nscVueAdd(key, _t("archive.message_b12f661dce46")),
        sub: _t("archive.message_23b350c5bba0"),
        body: __nscVueView(templates[402], [__nscVueJoin(items.map(({
          a,
          effective: v,
          shadowed
        }) => __nscVueView(templates[400], [esc(a.name), esc(E(a.env)?.name), esc(v?.source || _t("archive.message_4598307605ea")), shadowed ? _t("archive.message_ed87c67d2fb7") : _t("archive.message_5c88da6ce4bb"), B(_t("archive.message_ac05e8899ab4"), 'y-open-compose', 'code', 'sm', __nscVueInterpolate(["data-id=\"", "\""], [a.id]))])), '') || __nscVueView(templates[401], [])]),
        footer: B(_t("console.close"), 'close')
      })));
    }
    function importEnv() {
      Y.importRows = null;
      showModal(_modal(() => ({
        kind: 'y-env-import',
        size: 'xwide',
        title: _t("archive.message_31c5c3fadb7d"),
        sub: _t("archive.message_08010f3f2a6f"),
        body: __nscVueView(templates[403], []),
        footer: __nscVueAdd(B(_t("console.cancel"), 'close'), B(_t("archive.message_ed488445de43"), 'y-import-analyze', 'search', 'primary'))
      })));
    }
    function analyzeImport(raw) {
      const parsed = C.parseEnv(raw ?? document.getElementById('y-import').value);
      for (const k of Object.keys(parsed.rows)) if (!/^[A-Z_][A-Z0-9_]*$/.test(k) || /^(DOCKER_|COMPOSE_|NSC_)/.test(k) || /(PASSWORD|TOKEN|SECRET|PRIVATE_KEY)$/i.test(k)) parsed.errors.push(_t("archive.message_12120722ca96"));
      if (!Object.keys(parsed.rows).length) parsed.errors.push(_t("archive.message_f5889f181a65"));
      if (parsed.errors.length) {
        Y.importRows = null;
        const errors = [...new Set(parsed.errors)];
        if (raw === undefined) region(document.getElementById('y-import-result'), callout(esc(__nscVueJoin(errors, '；')), 'red'));
        return { errors, names: [], overwrite: 0 };
      }
      Y.importRows = parsed.rows;
      const names = Object.keys(parsed.rows);
      const overwrite = names.filter((k) => Object.hasOwn(ownRows(), k)).length;
      if (raw === undefined) region(document.getElementById('y-import-result'), __nscVueView(templates[405], [names.length, overwrite, __nscVueJoin(names.map((k) => __nscVueView(templates[404], [esc(k)])), ''), B(_t("archive.message_0670158b4a30"), 'y-import-apply', 'check', 'primary')]));
      return { errors: [], names, overwrite };
    }
    function applyImported() {
      if (!Y.importRows) return;
      const draft = ensureDraft();
      for (const [key, value] of Object.entries(Y.importRows)) draft.rows[key] = rec(value);
      Y.importRows = null;
      closeModal(true);
      render();
    }
    function confirmRemove(key) {
      if (!Object.hasOwn(ownRows(), key)) return;
      delete ensureDraft().rows[key];
      closeModal(true);
      render();
    }
    function variableDialogState() {
      return {
        layer: Y.layer,
        removeKey: Y.removeKey || '',
        removeReferences: Y.removeKey ? usage(Y.removeKey).length : 0,
        usageKey: Y.usageKey || '',
        usage: Y.usageKey ? usage(Y.usageKey).map(({ a, effective: value, shadowed }) => ({
          id: a.id, application: a.name, environment: E(a.env)?.name || '',
          source: value?.source || _t("archive.message_4598307605ea"), shadowed
        })) : [],
        history: scope(scopeKey()).history.map((item) => ({
          version: item.version, time: item.time, note: item.note || '',
          rows: Object.entries(item.rows).map(([key, value]) => `${key} = ${value.type === 'credential' ? _t("archive.message_40392fef5601") : value.value}`).join('\n')
        }))
      };
    }
    function historyVars() {
      const s = scope(scopeKey());
      showModal(_modal(() => ({
        kind: 'y-history',
        size: 'xwide',
        title: _t("archive.message_8432896c9287"),
        sub: __nscVueAdd(__nscVueAdd(label[Y.layer], ' · '), scopeKey()),
        body: __nscVueView(templates[407], [callout(_t("archive.message_9bdde09f8294"), 'blue'), __nscVueJoin(s.history.map((h) => __nscVueView(templates[406], [h.version, h.time, esc(h.note), esc(__nscVueJoin(Object.entries(h.rows).map(([k, v]) => __nscVueAdd(__nscVueAdd(k, ' = '), v.type === 'credential' ? _t("archive.message_40392fef5601") : v.value)), '\n'))])), '') || _t("archive.message_d0a722f4dc49")]),
        footer: B(_t("console.close"), 'close')
      })));
    }
    function composePage() {
      return hooks.data.composePage();
    }
    function compileSummary(...args) {
      return __variablesViews.compileSummary_74(__renderContext2, ...args);
    }
    function reviewCompose() {
      return hooks.data.command("reviewCompose", arguments);
    }
    function deployCompose() {
      return hooks.data.command("deployCompose", arguments);
    }
    function installVersion(w) {
      return {
        project: scope(__nscVueAdd('project:', w.project)).version,
        environment: scope(__nscVueAdd('environment:', w.env)).version
      };
    }
    function checkImportStructure(raw) {
      const result = validateComposeDocument(raw);
      return { errors: result.diagnostics.filter(d => d.severity === 'error').map(d => _t('compose.' + d.code)) };
    }
    function importCompose(raw, w) {
      w = w || {
        project: db.project,
        env: db.env,
        runtime: 'swarm-prod'
      };
      const analyzed = validateComposeDocument(raw, { swarm: W.RT(w.runtime)?.type === 'swarm' });
      const result = { tree: analyzed.tree, text: raw, errors: analyzed.diagnostics.filter(d => d.severity === 'error').map(d => _t('compose.' + d.code)), warnings: [], refs: [], runtime: [] };
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
      return hooks.data.unsupported("initializeImportedApp", formError);
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
        r = db.runtimes.find((r) => r.nodes.some((n) => n.id === Y.node));
      const all = db.instances.filter((t) => t.node === Y.node);
      return {
        n,
        r,
        all,
        current: all.filter((t) => t.current),
        services: [...new Set(all.filter((t) => t.current).map((t) => t.service))].map(W.ST).filter(Boolean)
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
      variables: () => _t("prototype.variables_configuration"),
      compose: () => _t("prototype.compose_workspace"),
      node: () => _t("archive.message_396fc72d71dc"),
      instance: () => _t("cluster.running"),
      operation: () => _t("archive.message_2a1b4e0e3217"),
      notfound: () => _t("archive.message_4aefbe3428ef")
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
      if (ui.page === 'notfound') return __nscVueAdd(yHead(_t("archive.message_343a0ee5705f"), _t("archive.message_dcf6ba0dd25f"), B(_t("archive.message_bb14d7c41431"), 'nav', 'back', 'primary', 'data-page="projects"')), __nscVueView(templates[451], [esc(Y.routeError || _t("archive.message_6e5dfb4748c8"))]));
      let h = oldPage();
      if (ui.page === 'application') {
        const a = W.APP(W.x.app);
        if (a) h = insertView(h, '.hero-actions', B(_t("prototype.compose_workspace"), 'y-open-compose', 'code', 'primary', __nscVueInterpolate(["data-id=\"", "\""], [a.id])));
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
    listen(window, 'click', (e) => {
      const el = e.target.closest('[data-action]');
      if (!el || el.disabled || el.dataset.vueControl === 'true' || e.button > 0 || e.ctrlKey || e.metaKey || e.shiftKey || e.altKey) return;
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
          rev = app?.revisions.find((r) => r.v === Number(el.dataset.version || el.dataset.v));
        if (!rev?.spec.composeBundle) {
          e.preventDefault();
          e.stopImmediatePropagation();
          toast(_t("archive.message_32f58ad83c96"), true);
          return;
        }
      }
      if (a === 'x-op-form' && el.dataset.kind === 'image') {
        e.preventDefault();
        e.stopImmediatePropagation();
        openCompose(W.ST(id)?.app);
        toast(_t("archive.message_64890f056cfa"));
        return;
      }
      if (!a.startsWith('y-') || a.startsWith('y-guard-')) return;
      e.preventDefault();
      e.stopImmediatePropagation();
      switch (a) {
        case 'y-layer':
          if (id === 'application' && !Y.app) {
            toast(_t("archive.message_32a024925138"), true);
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
          confirmRemove(key);
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
          applyImported();
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
            toast(_t("archive.message_02a5471e38ae"), true);
            break;
          }
          navigator.clipboard.writeText(location.href).then(() => toast(_t("archive.message_249b8c9a41f9"))).catch(() => toast(_t("archive.message_02a5471e38ae"), true));
          break;
      }
    }, true);
    let compileTimer;
    listen(window, 'input', (e) => {
      const el = e.target;
      if (el?.dataset?.vueControl === 'true') return;
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
    listen(window, 'change', (e) => {
      const el = e.target;
      if (el?.dataset?.vueControl === 'true') return;
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
    listen(window, 'keydown', (e) => {
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
        toast(_t("archive.message_3766efb26833"), true);
        return false;
      }
      if (!saveVars(true)) return false;
      if (docsDirty() && !saveDoc(true)) return false;
      return true;
    }
    Object.assign(Y, {
      seed,
      variableState,
      variableEditor,
      variableDialogState,
      analyzeImport,
      applyImported,
      confirmRemove,
      editVar,
      applyVar,
      removeVar,
      usageDialog,
      historyVars,
      importEnv,
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
  let historyRouteRequest = 0;
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
      const r = db.runtimes.find((r) => r.nodes.some((n) => n.id === Y.node));
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
    } else if (ui.page === 'activity') { path = '/operations/activity'; q = Y.activityQuery || {}; } else if (ui.page === 'installation') { path = '/infrastructure/installation'; q = Y.installationJob ? {job:Y.installationJob} : {}; } else if (ui.page === 'ha') path = '/infrastructure/ha';else if (ui.page === 'runtimes') path = '/infrastructure/runtimes';else if (ui.page === 'market') path = '/market';else if (ui.page === 'runtimeEvents') {
      path = '/infrastructure/events';
      q.filter = W.x.eventFilter;
    } else {
      path = __nscVueAdd(__nscVueAdd(root, '/'), enc(ui.page));
      if (ui.page === 'services') q = {
        include_removed: W.x.includeRemoved ? 'true' : '',
        scope: W.x.svcScope,
        layout: W.x.layout,
        runtime: W.x.svcRuntime === 'all' ? '' : W.x.svcRuntime,
        health: W.x.svcHealth === 'all' ? '' : W.x.svcHealth,
        q: W.x.svcSearch,
        page: W.x.svcPage > 1 ? W.x.svcPage : ''
      };
      if (ui.page === 'workloadHistory') q.tab = W.x.historyTab;
    }
    if (ui.page === 'service' && W.ST(W.x.service)?.status === 'REMOVED') q.include_removed = 'true';
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
      bad(path, _t("archive.message_28e357a3b940"));
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
      if (globalScope && (a.length !== 5 || a[2] !== 'environments' || a[3] !== 'all' || !aggregatePages.includes(a[4]))) return bad(path, _t("archive.message_ecd32590d110"));
      const p = globalScope ? P() || db.projects[0] : P(a[1]);
      if (!p && !globalScope) return bad(path, _t("archive.message_e1da3f27466b"));
      const aggregate = a[2] === 'environments' && a[3] === 'all';
      if (aggregate && (a.length !== 5 || !aggregatePages.includes(a[4]))) return bad(path, _t("archive.message_e67ea01fd27a"));
      const e = !p ? null : a[2] === 'environments' && a[3] && !aggregate ? E(a[3]) : aggregate && E(db.env)?.project === p.id ? E(db.env) : db.environments.find((e) => e.project === p.id && e.kind === 'production') || db.environments.find((e) => e.project === p.id);
      const projectOnly = a.length === 2 || a.length === 3 && ['environments', 'variables'].includes(a[2]) || aggregate;
      if (e ? e.project !== p.id : !projectOnly || p && projectEnvs(p.id).length > 0) return bad(path, _t("archive.message_e81f1948c740"));
      if (p) setScope(p.id, e?.id || '');else {
        db.project = '';
        db.env = '';
      }
      ui.allEnvironments = aggregate;
      ui.allProjects = globalScope;
      if (a.length === 2) {
        ui.page = 'project';
        return;
      }
      if (a[2] === 'variables' || a[4] === 'variables') {
        ui.page = 'variables';
        Y.layer = a[2] === 'variables' ? 'project' : tab('layer', ['environment', 'application'], 'environment');
        Y.app = q.get('app') || db.apps.find((a) => a.project === p.id && a.env === e?.id)?.id || '';
        if (Y.layer === 'application' && (!W.APP(Y.app) || W.APP(Y.app).project !== p.id || W.APP(Y.app).env !== e.id)) return bad(path, _t("archive.message_6ef03ee15c56"));
        Y.query = q.get('q') || '';
        return;
      }
      if (a.length === 3 && a[2] === 'environments') {
        ui.page = 'environments';
        return;
      }
      if (a[2] !== 'environments') return bad(path, _t("archive.message_235b3943b6fd"));
      if (a[4] === 'applications' && a[5]) {
        const app = W.APP(a[5]);
        if (!app || app.project !== p.id || app.env !== e.id) return bad(path, _t("archive.message_912115e2df19"));
        W.x.app = app.id;
        if (a[6] === 'compose') {
          if (Y.docApp !== app.id) Y.loadDoc(app.id);
          ui.page = 'compose';
          Y.view = tab('view', ['source', 'resolved', 'runtime', 'diff'], 'source');
          Y.file = q.get('file') === 'runtime' ? 'runtime' : 'compose';
          Y.previewEnv = q.get('previewEnv') || app.env;
          if (E(Y.previewEnv)?.project !== p.id) return bad(path, _t("archive.message_e71f59334cc1"));
        } else if (!a[6]) {
          ui.page = 'application';
          W.x.appTab = tab('tab', ['overview', 'config', 'history', 'events'], 'overview');
        } else return bad(path, _t("archive.message_08e88245957f"));
        return;
      }
      if (a[4] === 'services' && a[5]) {
        const s = W.ST(a[5]);
        if (!s || s.project !== p.id || s.env !== e.id) return bad(path, _t("archive.message_955a072364c5"));
        ui.page = 'service';
        W.x.service = s.id;
        W.x.serviceTab = tab('tab', ['instances', 'events', 'logs', 'config', 'routes'], 'instances');
        W.x.includeHistory = q.get('history') === '1';
        return;
      }
      if (a[4] === 'domains' && a[5]) {
        const d = db.domains.find((d) => d.id === a[5] && d.project === p.id && d.env === e.id);
        if (!d) return bad(path, _t("archive.message_cfcc8739dfc0"));
        setScope(p.id, e.id, d.id);
        ui.page = 'domain';
        ui.tab = tab('tab', ['overview', 'routes', 'config', 'tls', 'history'], 'overview');
        ui.configView = tab('editor', ['semantic', 'code'], 'semantic');
        ui.file = tab('file', ['generated', 'native', 'server', 'gateway', 'route'], 'generated');
        return;
      }
      const allowed = ['overview', 'applications', 'domains', 'routes', 'services', 'configs', 'certificates', 'workloadHistory', 'gateways', 'deployments', 'events', 'help'];
      if (!allowed.includes(a[4]) || a[5]) return bad(path, _t("archive.message_907606f90c04"));
      ui.page = a[4];
      if (ui.page === 'services') {
        W.x.svcScope = globalScope ? 'all' : aggregate ? 'project' : tab('scope', ['environment', 'project', 'all'], 'environment');
        ui.allProjects = W.x.svcScope === 'all';
        ui.allEnvironments = ui.allProjects || W.x.svcScope === 'project';
        W.x.layout = tab('layout', ['grouped', 'flat'], 'grouped');
        W.x.svcRuntime = q.get('runtime') || 'all';
        if (W.x.svcRuntime !== 'all' && !W.RT(W.x.svcRuntime)) return bad(path, _t("archive.message_704b2432f9af"));
        W.x.svcHealth = tab('health', ['all', 'running', 'issue', 'external'], 'all');
        W.x.svcSearch = q.get('q') || '';
        W.x.includeRemoved = q.get('include_removed') === 'true';
        W.x.svcPage = Math.max(1, Math.min(999, parseInt(q.get('page')) || 1));
      }
      if (ui.page === 'workloadHistory') W.x.historyTab = tab('tab', ['application', 'gateway'], 'application');
      return;
    }
    if (a[0] === 'services' && a.length === 2) {
      const s = W.ST(a[1]);
      if (!s || s.project) return bad(path, _t("archive.message_cd2cc6ae1789"));
      ui.page = 'service';
      W.x.service = s.id;
      W.x.serviceTab = tab('tab', ['instances', 'events', 'logs', 'config', 'routes'], 'instances');
      W.x.includeHistory = q.get('history') === '1';
      return;
    }
    if (a[0] === 'market' && a.length === 1) {
      ui.page = 'market';
      return;
    }
    if (a[0] === 'operations' && a[1] === 'activity' && a.length === 2) { ui.page = 'activity'; Y.activityQuery = Object.fromEntries(['resource_type', 'resource_id', 'project_id', 'environment_id', 'state'].map(key => [key, q.get(key) || ''])); return; }
    if (a[0] === 'operations' && a.length === 2) {
      const o = W.OP(a[1]);
      if (!o) return bad(path, _t("archive.message_3ef123a77f47"));
      setScope(o.project, o.env);
      Y.operation = o.id;
      ui.page = 'operation';
      return;
    }
    if (a[0] === 'infrastructure') {
      if (a[1] === 'installation' && a.length === 2) { ui.page = 'installation'; Y.installationJob = q.get('job') || ''; return; }
      if (a[1] === 'ha' && a.length === 2) { ui.page = 'ha'; return; }
      if (a[1] === 'events' && a.length === 2) {
        ui.page = 'runtimeEvents';
        W.x.eventFilter = q.get('filter') || 'all';
        return;
      }
      if (a[1] !== 'runtimes') return bad(path, _t("archive.message_9ef0a99ffa56"));
      if (a.length === 2) {
        ui.page = 'runtimes';
        return;
      }
      const r = W.RT(a[2]);
      if (!r) return bad(path, _t("archive.message_9c752946cacb"));
      W.x.runtime = r.id;
      if (a.length === 3) {
        ui.page = 'runtime';
		W.x.runtimeTab = tab('tab', ['overview', 'nodes', 'services', 'events', 'resources'], 'overview');
        return;
      }
      if (a[3] === 'nodes' && a.length === 5) {
        const n = r.nodes.find((n) => n.id === a[4]);
        if (!n) return bad(path, _t("archive.message_b8ed8dc01ea0"));
        Y.node = n.id;
        Y.nodeTab = tab('tab', r.type === 'swarm' ? ['services', 'tasks', 'containers', 'events'] : ['services', 'containers', 'events'], 'services');
        Y.nodeHistory = q.get('history') === '1';
        Y.nodeSearch = q.get('q') || '';
        ui.page = 'node';
        return;
      }
      if (a[3] === 'instances' && a.length === 5) {
        const t = W.IS(a[4]);
        if (!t || t.runtime !== r.id) return bad(path, _t("archive.message_a042e9615aab"));
        Y.instance = t.id;
        Y.instanceKind = t.kind === 'container' ? 'container' : tab('kind', ['task', 'container'], 'task');
        Y.instanceTab = tab('tab', ['overview', 'logs'], 'overview');
        ui.page = 'instance';
        return;
      }
    }
    bad(path, _t("archive.message_7d5a187da78d"));
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
    return [...document.querySelectorAll('#overlay input[id],#overlay textarea[id],#overlay select[id]')].map((el) => ({
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
      const r = db.runtimes.find((r) => r.nodes.some((n) => n.id === id));
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
      const d = db.domains.find((d) => d.id === id);
      return d ? __nscVueInterpolate(["/projects/", "/environments/", "/domains/", "?tab=", ""], [enc(d.project), enc(d.env), enc(id), el.dataset.tab || 'overview']) : null;
    }
    if (a === 'x-runtime') return __nscVueAdd('/infrastructure/runtimes/', enc(id));
    if (a === 'x-deployment') return __nscVueAdd('/operations/', enc(id));
    if (a === 'nav') {
      const page = el.dataset.page;
      if (!allProjectView() && P() && !projectEnvs().length) {
        if (page === 'variables') return '/projects/' + enc(db.project) + '/variables';
        if (aggregatePages.includes(page)) return projectHref(db.project, page);
      }
      if (page === 'projects') return '/projects';
      if (page === 'project') return __nscVueAdd('/projects/', enc(db.project));
      if (page === 'environments') return __nscVueAdd(__nscVueAdd('/projects/', enc(db.project)), '/environments');
      if (page === 'market') return '/market';
      if (page === 'installation') return '/infrastructure/installation';
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
    showModal(_modal(() => ({
      kind: 'y-navigation-guard',
      size: 'small',
      title: _t("archive.message_fa3084b385bb"),
      sub: _t("archive.message_bd4c29e03679"),
      body: callout(_t("archive.message_3bbbc4ab324c"), 'blue'),
      footer: __nscVueAdd(__nscVueAdd(B(_t("dirty.continue"), 'y-guard-stay'), B(_t("dirty.discard"), 'y-guard-discard', '', 'danger')), B(_t("dirty.save_leave"), 'y-guard-save', 'save', 'primary'))
    })));
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
  listen(window, 'click', (e) => {
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
          toast(_t("archive.message_e09145ad9f5e"), true);
          guardState = null;
          return;
        }
        if (modal?.kind === 'route' && !equal(routeForm, routeDraftSnapshot)) {
          toast(_t("archive.message_abfdaa5e7761"), true);
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
  listen(window, 'beforeunload', (e) => {
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
    if (resource.status === 'REMOVED') return false;
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
      q: W.x.svcSearch,
      include_removed: W.x.includeRemoved ? 'true' : ''
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
        hooks.navigate(concreteScopeHref(target, intent)).then((failure) => {
          if (failure) return;
          if (intent === 'domain-add') addDomainModal();else if (intent === 'p-service-add') serviceModal();else if (intent !== 'variables') envModal(target.id);
        });
      });
      return;
    }
    if (!allEnvironmentView() || !['domain-add', 'p-service-add', 'p-env-settings', 'variables'].includes(action)) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    ensureNavigate(() => showModal(_modal(() => ({
      kind: 'scope-environment',
      scopeIntent: action,
      size: 'small',
      title: allProjectView() ? _t("archive.message_4a88e394e119") : _t("archive.message_00a771470af7"),
      sub: _t("archive.message_f0547a18d180"),
      body: __nscVueView(templates[455], [__nscVueJoin(listEnvironments().map((e) => __nscVueView(templates[454], [e.id, I('layers'), esc(resourceScopeName({
        project: e.project,
        env: e.id
      })), esc(e.slug), I('right', 'sm')])), '')]),
      footer: B(_t("console.cancel"), 'close')
    }))));
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
    const name = E(resource.env)?.name || resource.env || _t("archive.message_96181a00e026");
    return __nscVueAdd(allProjectView() ? __nscVueAdd(P(resource.project)?.name || resource.project || _t("archive.message_96181a00e026"), ' / ') : '', name);
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
      q: W.x.svcSearch,
      include_removed: W.x.includeRemoved ? 'true' : ''
    } : {};
    if (page === 'workloadHistory') q.tab = W.x.historyTab;
    if (db.view !== 'cloud') q.perspective = db.view;
    return __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('/projects/', enc(project)), '/environments/all/'), page), query(q));
  }
  function concreteScopeHref(target, intent) {
    return __nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd(__nscVueAdd('/projects/', enc(target.project)), '/environments/'), enc(target.id)), '/'), intent === 'variables' ? 'variables?layer=environment' : scopeListPage(ui.page)), db.view === 'ops' ? __nscVueAdd(intent === 'variables' ? '&' : '?', 'perspective=ops') : '');
  }
  function globalScopeContext() {
    return __nscVueView(templates[456], [I('folder'), badge(__nscVueAdd(db.projects.length, _t("archive.message_d441d42da141"))), I('layers', 'sm'), db.environments.length]);
  }
  const scopedLinkFor = linkFor;
  linkFor = function (el) {
    const href = scopedLinkFor(el);
    if (!href || db.view !== 'ops') return href;
    const url = new URL(href, 'https://nsc.invalid');
    url.searchParams.set('perspective', 'ops');
    return __nscVueAdd(__nscVueAdd(url.pathname, url.search), url.hash);
  };
  hooks.data.attach({
    get db() {
      return db;
    },
    get ui() {
      return ui;
    },
    get workspace() {
      return NSCWorkspace;
    },
    get workload() {
      return NSCBridge;
    },
    get modal() {
      return modal;
    },
    get work() {
      return work;
    },
    get guardState() {
      return guardState;
    },
    get ENV() {
      return ENV;
    },
    render: () => render(),
    // Resource refresh repaints the admitted route without synchronizing a URL.
    renderData: () => renderView(),
    refreshDomainEditor: () => { work = clone(workingSaved()); },
    showModal,
    closeModal,
    formError,
    toast,
    selectScope,
    updateHash,
    redrawDiag,
    restoreFields,
    baseClose,
    renderModal
  });
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
    deploymentWizard: {
      render() {
        preserveWizardFocus = true;
        try { W.renderWizard(); } finally { preserveWizardFocus = false; }
      },
      collect: W.collectWizard,
      issues: W.wizardIssues
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
    link: (attrs) => linkFor({
      dataset: Object.fromEntries(Object.entries(attrs).filter(([k]) => k.startsWith('data-')).map(([k, v]) => [k.slice(5).replace(/-([a-z])/g, (_, c) => c.toUpperCase()), v]))
    }),
    sameEditor: (a, b) => {
      const x = editorKey(a),
        y = editorKey(b);
      return !!x && x === y;
    },
    acceptRoute(path) {
      const request = ++historyRouteRequest;
      if (new URL(path, 'https://nsc.invalid').searchParams.get('include_removed') === 'true' && hooks.data?.loadServiceHistory && !hooks.data.historyLoaded) {
        hooks.data.loadServiceHistory().then(() => {
          if (request === historyRouteRequest) this.acceptRoute(path);
        }).catch((error) => toast(error.message, true));
        return;
      }
      // Local controls have already updated this route's state. URL
      // synchronization must retain a modal opened while Router was resolving.
      if (path === routePath()) { renderView(); return; }
      routeBusy = true;
      const keep = this.sameEditor(routePath(), path);
      if (!(keep && ['y-variable', 'y-usage', 'y-remove', 'y-history', 'y-env-import'].includes(modal?.kind))) baseClose(true);
      if (!keep) Y.varDraft = null;
      parse(path);
      renderView();
      document.title = __nscVueAdd(PAGE_NAMES[ui.page] || 'NSC', ' · Network Service Composer');
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
    toggleMenu() {
      ui.menu = !ui.menu;
      render();
    },
    openPalette: palette,
    paletteItems(query) {
      const search = String(query || '').trim().toLowerCase();
      const domains = db.domains.filter(item => (allProjectView() || item.project === db.project) && item.name.toLowerCase().includes(search));
      const services = db.services.filter(item => item.status !== 'REMOVED' && (allProjectView() || item.project === db.project) && _serviceSearch(db.services, item.id).includes(search));
      return {
        scope: allProjectView() ? _t("archive.message_273ec15f9553") : __nscVueAdd(__nscVueAdd(_t("archive.message_2b54fee2ad35"), P().name), _t("archive.message_08d8662812cb")),
        domains: domains.map(item => ({ id: item.id, name: item.name, scope: __nscVueAdd(__nscVueAdd(P(item.project).name, ' / '), E(item.env).name) })),
        services: services.map(item => ({ id: item.id, name: _serviceName(db.services, item.id), scope: resourceScopeName(item) }))
      };
    },
    selectPaletteItem(kind, id) {
      if (kind === 'domain') { closeModal(true); navigate('domain', 'overview', id); return; }
      if (kind === 'service') { serviceDetail(id); return; }
      if (kind === 'config') { closeModal(true); navigate('domain', 'config'); return; }
      if (kind === 'ops') { closeModal(true); db.view = 'ops'; navigate('domain', 'overview', ui.domain); return; }
      if (kind === 'scenarios') { scenarioModal(); return; }
      if (kind === 'help') helpModal();
    },
    openHelp: helpModal,
    openAbout: aboutModal,
    navigatePage(page) {
      closeModal(true);
      const href = linkFor({ dataset: { action: 'nav', page } });
      if (href) void hooks.navigate(href);
      else navigate(page);
    },
    navigateServices(health = '') {
      W.x.svcHealth = health || 'all';
      this.navigatePage('services');
    },
    variableState() { return Y.variableState(); },
    variableEditor() { return Y.variableEditor(); },
    selectVariableLayer(layer) {
      if (!['project', 'environment', 'application'].includes(layer)) return;
      if (layer === 'application' && !Y.variableState().app) {
        toast(_t("archive.message_32a024925138"), true);
        return;
      }
      ensureNavigate(() => { Y.layer = layer; Y.varDraft = null; render(); });
    },
    selectVariableApplication(id) {
      if (!Y.variableState().apps.some((app) => app.id === id)) return;
      ensureNavigate(() => { Y.app = id; Y.varDraft = null; render(); });
    },
    updateVariableSearch(value) { Y.query = value; render(); },
    saveVariables() { return Y.saveVars(); },
    openVariableEditor(key = '') { Y.editVar(key); },
    stageVariable(form) { return Y.applyVar(form); },
    requestVariableRemoval(key) { Y.removeVar(key); },
    openVariableUsage(key) { Y.usageDialog(key); },
    openVariableHistory() { Y.historyVars(); },
    openVariableImport() { Y.importEnv(); },
    variableDialogState() { return Y.variableDialogState(); },
    analyzeVariableImport(raw) { return Y.analyzeImport(raw); },
    invalidateVariableImport() { Y.importRows = null; },
    applyVariableImport() { Y.applyImported(); },
    confirmVariableRemoval(key) { Y.confirmRemove(key); },
    openComposeDocument(id) { Y.openCompose(id); },
    updateServiceSearch(value) { W.x.svcSearch = value; W.x.svcPage = 1; render(); },
    updateServiceFilter(field, value) {
      const filters = { scope: 'svcScope', runtime: 'svcRuntime', health: 'svcHealth' };
      if (!filters[field]) return;
      W.x[filters[field]] = value;
      if (field === 'scope') {
        ui.allProjects = value === 'all';
        ui.allEnvironments = value !== 'environment';
      }
      W.x.svcPage = 1;
      render();
    },
    setServiceLayout(value) {
      if (!['grouped', 'flat'].includes(value)) return;
      W.x.layout = value;
      db.serviceLayout = value;
      persist();
      render();
    },
    stepServicePage(step) { W.x.svcPage = Math.max(1, W.x.svcPage + step); render(); },
    toggleRemovedServices() { W.x.includeRemoved = !W.x.includeRemoved; W.x.svcPage = 1; updateHash(); render(); },
    showAllServices() { ui.allProjects = true; ui.allEnvironments = true; W.x.svcScope = 'all'; W.x.svcPage = 1; W.jump('services'); },
    openService(id, tab = 'instances') { W.jump('service', id, tab); },
    selectProject(id) {
      if (id === 'all' || allProjectView()) hooks.navigate(projectHref(id));
      else switchScope(id);
    },
    selectEnvironment(id) { hooks.navigate(environmentHref(id)); },
    selectContextEnvironment(id) {
      const href = linkFor({ dataset: { action: 'p-env', id } });
      if (href) void hooks.navigate(href);
      else switchScope(db.project, id);
    },
    createEnvironment() { ensureNavigate(() => envModal()); },
    editEnvironment(id) { ensureNavigate(() => envModal(id)); },
    addDomainFromEnvironments() { ensureNavigate(chooseEnvForDomain); },
    copyProjectConfiguration() { ensureNavigate(beginCopy); },
    openDomain(id) {
      const href = linkFor({ dataset: { action: 'open-domain', id } });
      if (href) void hooks.navigate(href);
    },
    domainStatusView(id) { return domainStatus(db.domains.find(domain => domain.id === id)); },
    routeEditor() {
      if (!routeForm) return null;
      return {
        route: clone(routeForm), existing: work.routes.some(item => item.id === routeForm.id),
        targetMode: routeTargetMode, supportsCustomTarget: true, error: routeEditorError,
        domainName: D().name,
        matches: Object.entries(MATCH).map(([value, label]) => ({ value, label })),
        kinds: ['proxy', 'return', 'redirect'].map(value => ({ value, label: KINDS[value] })),
        services: db.services.filter(item => item.status !== 'REMOVED' && item.env === D().env && item.project === D().project).map(item => ({ id: item.id, name: _serviceName(db.services, item.id), port: item.port, ready: ready(item.id).length }))
      };
    },
    updateRouteForm(patch) { if (routeForm) Object.assign(routeForm, patch); },
    updateRouteTargetMode(value) { routeTargetMode = value === 'custom' ? 'custom' : 'service'; if (routeTargetMode === 'service' && routeForm) routeForm.externalUrl = ''; },
    applyRouteForm() { applyRoute(); },
    requestRouteDelete,
    domainCoverage(id) { return syncInfo(db.domains.find(domain => domain.id === id)); },
    domainHasIssue(id) {
      const domain = db.domains.find(item => item.id === id);
      return !!domain && (!!domain.drift || syncInfo(domain).known !== domain.gateways.length);
    },
    updateDomainListSearch(value) { ui.search = value; render(); },
    updateDomainListFilter(value) { ui.filter = value; render(); },
    addDomain() { ensureNavigate(addDomainModal); },
    addService() { ensureNavigate(serviceModal); },
    openApplication(id) { W.jump('application', id); },
    beginApplicationInstall(source = 'template', template = 'portal') { W.beginInstall(source, template); },
    marketTemplates() { return W.templates; },
    updateMarketSearch(value) { W.x.marketSearch = value; render(); },
    updateMarketCategory(value) { W.x.marketCategory = value; render(); },
    openMarketTemplate(id) { W.templateInfo(id); },
    installMarketTemplate(id) { closeModal(true); W.beginInstall('template', id); },
    resumeApplicationDraft() {
      const d = db.appDrafts.filter(inListScope).at(-1);
      if (!d) return;
      W.x.wizard = reactive(clone(d));
      W.x.wizard.reviewAck = false;
      W.x.wizard.warningAck = false;
      W.x.wizardError = '';
      W.renderWizard();
    },
    applicationState(id) {
      const app = W.APP(id);
      if (!app) return { name: '', tone: '' };
      return app.system && app.status === 'UNKNOWN'
        ? { name: _t('community.evidence_state.UNKNOWN'), tone: 'amber' }
        : W.appState(app);
    },
    openProjectForm() {
      if (ui.externalConflict) return toast(_t("archive.message_d86dfab74539"), true);
      if (deploymentTimer) return toast(_t("archive.message_7cbb758e89de"), true);
      ensureNavigate(projectModal);
    },
    enterProject(id) {
      if (ui.externalConflict) return toast(_t("archive.message_d86dfab74539"), true);
      if (deploymentTimer) return toast(_t("archive.message_7cbb758e89de"), true);
      switchScope(id, null, 'project');
    },
    browseAllProjects() { void hooks.navigate(projectHref('all')); },
    createProject,
    saveEnvironment,
    deleteEnvironment,
    get formErrorMessage() { return formErrorMessage; },
    openScenarios: scenarioModal,
    openDemoSettings: W.demoSettings,
    iconView: I,
    closeModal() { closeModal(); },
    setPerspective,
    refresh: render,
    compile: () => Y.compileApp(),
    effective: (p, e, a) => Y.effective(p, e, a),
    serviceState: (id) => W.serviceState(W.ST(id)),
    clusterSummary: (id) => W.clusterSummary(W.RT(id)),
    dispose() {
      disposers.forEach((fn) => fn());
      if (deploymentTimer) clearInterval(deploymentTimer);
      document.body.style.overflow = '';
    }
  };
}
