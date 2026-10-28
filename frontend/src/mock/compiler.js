import { translate as _t } from '../i18n';

/* NSC prototype compiler. Deliberately bounded YAML subset, never executes code.
 * Production must use a pinned official Compose/Stack validator, not this parser.
 * This file is standalone so its rules can be tested in Node as well as browsers.
 */

'use strict';
const own = (o, k) => Object.prototype.hasOwnProperty.call(o, k);
const blocked = new Set(['__proto__', 'constructor', 'prototype']);
function stripComment(s) {
  let q = '',esc = false;
  for (let i = 0; i < s.length; i++) {const c = s[i];if (esc) {esc = false;continue;}if (q === '"' && c === '\\') {esc = true;continue;}if (q) {if (c === q) {if (q === "'" && s[i + 1] === "'") {i++;continue;}q = '';}} else if (c === '"' || c === "'") {q = c;} else if (c === '#' && (i === 0 || /\s/.test(s[i - 1]))) {return s.slice(0, i).trimEnd();}}
  return s.trimEnd();
}
function colon(s) {let q = '',depth = 0;for (let i = 0; i < s.length; i++) {let c = s[i];if (q) {if (c === '\\' && q === '"') {i++;continue;}if (c === q) q = '';continue;}if (c === '"' || c === "'") {q = c;continue;}if (c === '[' || c === '{') depth++;if (c === ']' || c === '}') depth--;if (c === ':' && depth === 0 && (i === s.length - 1 || /\s/.test(s[i + 1]))) return i;}return -1;}
function scalar(s, line) {s = s.trim();if (!s) return null;if (/^[&*!>|]/.test(s) || s === '---' || s === '...') throw Error(_t("archive.message_2ae2516c45b8", { v0: line }));if (s.startsWith('"')) {try {return JSON.parse(s);} catch (_) {throw Error(_t("archive.message_7c6bacfece60", { v0: line }));}}if (s.startsWith("'")) {if (!s.endsWith("'")) throw Error(_t("archive.message_21715241519f", { v0: line }));return s.slice(1, -1).replace(/''/g, "'");}if (s.startsWith('[') || s.startsWith('{')) {try {return JSON.parse(s);} catch (_) {throw Error(_t("archive.message_9731fed76ddb", { v0: line }));}}if (s === 'null' || s === '~') return null;if (s === 'true') return true;if (s === 'false') return false;if (/^-?(?:0|[1-9]\d*)(?:\.\d+)?$/.test(s)) return Number(s);return s;}
function parseYAML(text) {
  if (typeof text !== 'string' || text.length > 65536) throw Error(_t("archive.message_0677a7c6bc5b"));
  const ls = text.split(/\r?\n/).map((s, i) => {if (/^\s*\t/.test(s)) throw Error(_t("archive.message_e0f799b76d34", { v0: i + 1 }));s = stripComment(s);return { text: s.trim(), indent: s.length - s.trimStart().length, line: i + 1 };}).filter((l) => l.text);
  if (!ls.length) throw Error(_t("archive.message_8d3225a3d1ab"));if (ls[0].indent) throw Error(_t("archive.message_bee4489d08bd"));let at = 0;
  function block(indent, depth) {if (depth > 24) throw Error(_t("archive.message_f6e5e96b40b0"));const arr = ls[at].text.startsWith('- '),out = arr ? [] : Object.create(null);
    while (at < ls.length && ls[at].indent === indent) {const row = ls[at];if (row.text.startsWith('- ') !== arr) throw Error(_t("archive.message_0b818b1a5919", { v0: row.line }));if (arr) {const v = row.text.slice(2);if (colon(v) >= 0) throw Error(_t("archive.message_245050ba4902", { v0: row.line }));out.push(scalar(v, row.line));at++;if (at < ls.length && ls[at].indent > indent) throw Error(_t("archive.message_bce0ef0d6b7f", { v0: ls[at].line }));} else {const ci = colon(row.text);if (ci < 0) throw Error(_t("archive.message_22761326ccce", { v0: row.line }));const key = String(scalar(row.text.slice(0, ci), row.line));if (blocked.has(key) || key === '<<' || key.includes('$')) throw Error(_t("archive.message_4789243a35f4", { v0: row.line }));if (own(out, key)) throw Error(_t("archive.message_9cd32694443a", { v0: row.line, v1: key }));const tail = row.text.slice(ci + 1).trim();at++;if (tail) {out[key] = scalar(tail, row.line);if (at < ls.length && ls[at].indent > indent) throw Error(_t("archive.message_03025233b6a2", { v0: ls[at].line }));} else if (at < ls.length && ls[at].indent > indent) out[key] = block(ls[at].indent, depth + 1);else out[key] = null;}}
    if (at < ls.length && ls[at].indent > indent) throw Error(_t("archive.message_571f0b31d5c4", { v0: ls[at].line }));return out;
  }
  const obj = block(0, 0);if (at < ls.length) throw Error(_t("archive.message_571f0b31d5c4", { v0: ls[at].line }));return obj;
}
function parseEnv(text) {const rows = Object.create(null);const errors = [];for (const [i, line] of text.split(/\r?\n/).entries()) {const s = stripComment(line).trim();if (!s) continue;const m = s.match(/^([A-Za-z_][A-Za-z0-9_]*)\s*=\s*(.*)$/);if (!m) {errors.push(_t("archive.message_a379339359a8", { v0: i + 1 }));continue;}const k = m[1];if (blocked.has(k) || own(rows, k)) {errors.push(_t("archive.message_87e866af0d3c", { v0: i + 1 }));continue;}try {let v = m[2];if (v.startsWith('"') || v.startsWith("'")) v = scalar(v, i + 1);if (String(v).includes('${') || String(v).includes('$(')) throw Error(_t("archive.message_a5c7e8e65bfa"));rows[k] = String(v);} catch (e) {errors.push(_t("archive.message_640f0bc5148e", { v0: i + 1, v1: e.message }));}}return { rows, errors };}
function interpolate(text, vars, path = '', refs = [], depth = 0) {if (depth > 16) throw Error(_t("archive.message_e1af47b96869"));let out = '';
  for (let i = 0; i < text.length;) {if (text[i] !== '$') {out += text[i++];continue;}if (text[i + 1] === '$') {out += '$';i += 2;continue;}let expr = '',end = i + 1;
    if (text[i + 1] === '{') {let balance = 1,j = i + 2;for (; j < text.length && balance; j++) {if (text[j] === '{') balance++;else if (text[j] === '}') balance--;}if (balance) throw Error(_t("archive.message_87a764394a12", { v0: path }));expr = text.slice(i + 2, j - 1);end = j;} else {const m = text.slice(i + 1).match(/^[_A-Za-z][_A-Za-z0-9]*/);if (!m) {out += '$';i++;continue;}expr = m[0];end = i + 1 + expr.length;}
    const m = expr.match(/^([_A-Za-z][_A-Za-z0-9]*)(?:(:-|-|:\?|\?|:\+|\+)([\s\S]*))?$/);if (!m) throw Error(_t("archive.message_f37ac800eb27", { v0: path }));
    const [, key, op, arg = ''] = m;const set = own(vars, key),v = set ? vars[key] : null,secret = v?.type === 'credential';const value = set ? String(v?.value ?? v ?? '') : '';const nonempty = set && value !== '';let chosen = false,result = '';
    if (!op) {if (!set) throw Error(_t("archive.message_30f2fb57f526", { v0: path, v1: key }));chosen = true;result = value;} else
    if (op === ':-' || op === '-') {chosen = op === ':-' ? nonempty : set;result = chosen ? value : interpolate(arg, vars, path, refs, depth + 1);} else
    if (op === ':?' || op === '?') {if (!(op === ':?' ? nonempty : set)) throw Error(_t("archive.message_8e4766399dae", { v0: path, v1: key, v2: op === ':?' ? _t("archive.message_f40f20d36dff") : _t("archive.message_df3d58c7d84b") }));chosen = true;result = value;} else
    {const use = op === ':+' ? nonempty : set;result = use ? interpolate(arg, vars, path, refs, depth + 1) : '';}
    if (secret) throw Error(_t("archive.message_916c7f6b19f1", { v0: path, v1: key }));
    refs.push({ key, path, source: chosen ? v?.source || _t("archive.message_a772fa4ebe36") : _t("archive.message_a0087080eda3"), value: result, expression: '$' + '{' + expr + '}' });out += result;i = end;
  }return out;}
function expandTree(v, vars, refs, path = '') {if (typeof v === 'string') return interpolate(v, vars, path, refs);if (Array.isArray(v)) return v.map((s, i) => expandTree(s, vars, refs, `${path}[${i}]`));if (v && typeof v === 'object') {const out = Object.create(null);for (const [k, s] of Object.entries(v)) out[k] = expandTree(s, vars, refs, path ? `${path}.${k}` : k);return out;}return v;}
function dump(v, indent = 0) {const p = ' '.repeat(indent);if (Array.isArray(v)) return v.map((x) => p + '- ' + JSON.stringify(x)).join('\n');if (v && typeof v === 'object') return Object.entries(v).map(([k, val]) => p + k + ':' + (val && typeof val === 'object' ? '\n' + dump(val, indent + 2) : ' ' + JSON.stringify(val))).join('\n');return p + JSON.stringify(v);}
const ALLOWED_TOP = new Set(['version', 'name', 'services', 'networks', 'volumes', 'secrets', 'configs']);
const ALLOWED_SVC = new Set(['image', 'environment', 'env_file', 'ports', 'expose', 'volumes', 'networks', 'command', 'entrypoint', 'working_dir', 'user', 'read_only', 'restart', 'mem_limit', 'cpus', 'deploy', 'healthcheck', 'labels', 'secrets', 'configs']);
function compile(text, vars, { target = 'stack', envFiles = {} } = {}) {const errors = [],warnings = [],refs = [];let original, tree;try {original = parseYAML(text);tree = expandTree(original, vars, refs);} catch (e) {return { errors: [e.message], warnings, refs, tree: null, text: '', runtime: [] };}
  for (const k of Object.keys(tree)) if (!ALLOWED_TOP.has(k)) errors.push(_t("archive.message_645ca9919027", { v0: k }));
  if (!tree.services || Array.isArray(tree.services) || typeof tree.services !== 'object') errors.push(_t("archive.message_e2a004216be1"));const runtime = [];
  for (const [name, s] of Object.entries(tree.services || {})) {if (!s || typeof s !== 'object' || Array.isArray(s)) {errors.push(_t("archive.message_fe9ae18c8b84", { v0: name }));continue;}const at = 'services.' + name;
    const invalid = [];for (const key of ['deploy', 'healthcheck']) if (s[key] !== undefined && (!s[key] || typeof s[key] !== 'object' || Array.isArray(s[key]))) invalid.push(key + _t("archive.message_35062052eb03"));
    for (const key of ['ports', 'expose', 'volumes', 'secrets', 'configs']) if (s[key] !== undefined && !Array.isArray(s[key])) invalid.push(key + _t("archive.message_3d780ad3bc4e"));
    if (s.env_file !== undefined && typeof s.env_file !== 'string' && !Array.isArray(s.env_file)) invalid.push(_t("archive.message_a9a65f0f36a1"));
    if (s.environment !== undefined && (!s.environment || typeof s.environment !== 'object')) invalid.push(_t("archive.message_acf6ba98d2e1"));
    if (invalid.length) {errors.push(...invalid.map((m) => at + '：' + m));continue;}
    if (s.deploy?.mode && !['replicated', 'replicated-job'].includes(s.deploy.mode)) errors.push(at + _t("archive.message_f3d4736dba45"));

    for (const k of Object.keys(s)) if (!ALLOWED_SVC.has(k)) errors.push(_t("archive.message_b7d217108e7b", { v0: at, v1: k }));
    if (!s.image || !/^[a-zA-Z0-9][a-zA-Z0-9._/@:-]*$/.test(String(s.image))) errors.push(_t("archive.message_26ca59372bf6", { v0: at }));
    if (s.deploy) {for (const k of Object.keys(s.deploy)) if (!['mode', 'replicas', 'resources', 'restart_policy', 'placement', 'update_config', 'rollback_config', 'labels'].includes(k)) errors.push(_t("archive.message_5b7eca4459e9", { v0: at, v1: k }));if (s.deploy.replicas !== undefined && (!/^\d+$/.test(String(s.deploy.replicas)) || Number(s.deploy.replicas) > 50)) errors.push(_t("archive.message_a17bb9693439", { v0: at }));if (s.deploy.replicas !== undefined) s.deploy.replicas = Number(s.deploy.replicas);}
    const cpu = s.deploy?.resources?.limits?.cpus ?? s.cpus;if (cpu !== undefined && (!(Number(cpu) > 0) || Number(cpu) > 128)) errors.push(_t("archive.message_da4ae920c3ab", { v0: at }));
    const mem = s.deploy?.resources?.limits?.memory ?? s.mem_limit;if (mem !== undefined && !/^\d+(?:\.\d+)?[bkmg]?(?:b)?$/i.test(String(mem))) errors.push(_t("archive.message_3a3159749669", { v0: at }));
    for (const p of s.expose || []) if (!/^\d+(?:\/tcp|\/udp)?$/.test(String(p)) || parseInt(p) < 1 || parseInt(p) > 65535) errors.push(_t("archive.message_31ba3320c4be", { v0: at }));
    if (s.ports?.length) {for (const p of s.ports) {if (typeof p !== 'string' || p.split(':').slice(-2).some((n) => !/^\d+(?:\/tcp|\/udp)?$/.test(n) || parseInt(n) < 1 || parseInt(n) > 65535)) errors.push(_t("archive.message_777a0fa650c0", { v0: at }));}warnings.push(_t("archive.message_0367348248af", { v0: name }));}
    if (target === 'stack' && (s.restart !== undefined || s.mem_limit !== undefined || s.cpus !== undefined)) errors.push(_t("archive.message_00fdaffa3b2e", { v0: name }));
    if (target === 'compose' && s.deploy?.mode === 'replicated-job') errors.push(_t("archive.message_33808b9650b5", { v0: name }));
    if (target === 'compose' && s.deploy?.placement) errors.push(_t("archive.message_d7cdcb5a35ea", { v0: name }));
    if (s.volumes?.some((v) => String(v).includes('/var/run/docker.sock') || String(v).startsWith('/'))) warnings.push(_t("archive.message_7cf02751016c", { v0: name }));
    if (s.secrets || s.configs) warnings.push(_t("archive.message_79e7859f111c", { v0: name }));
    let env = Object.create(null);const files = Array.isArray(s.env_file) ? s.env_file : s.env_file ? [s.env_file] : [];
    for (const f of files) {if (!own(envFiles, f)) {errors.push(_t("archive.message_09c538a9bb27", { v0: at, v1: String(f) }));continue;}const parsed = parseEnv(envFiles[f]);errors.push(...parsed.errors.map((e) => `${f}：${e}`));for (const [k, v] of Object.entries(parsed.rows)) {if (/(PASSWORD|TOKEN|SECRET|PRIVATE_KEY)$/i.test(k)) errors.push(_t("archive.message_d240107b0755", { v0: f, v1: k }));env[k] = { value: v, source: 'env_file · ' + f };}}
    const explicit = Array.isArray(s.environment) ? Object.fromEntries(s.environment.map((row) => {const i = String(row).indexOf('=');if (i < 0) {errors.push(_t("archive.message_a4d62d5ddccd", { v0: at }));return [String(row), ''];}return [row.slice(0, i), row.slice(i + 1)];})) : s.environment || {};
    for (const [k, v] of Object.entries(explicit)) {if (v === null) {errors.push(_t("archive.message_aedcf320c983", { v0: at, v1: k }));continue;}if (/(PASSWORD|TOKEN|SECRET|PRIVATE_KEY)$/i.test(k)) errors.push(_t("archive.message_d06c84622771", { v0: at, v1: k }));env[k] = { value: String(v), source: env[k] ? _t("archive.message_68b94bfa9924") : 'environment' };}
    runtime.push({ service: name, env, unknownImageDefaults: true });
  }
  if (!Object.keys(tree.services || {}).length) errors.push(_t("archive.message_efce8fe6dbfc"));
  if (target === 'stack') warnings.push(_t("archive.message_f2ad7537dcd0"));
  warnings.push(_t("archive.message_f51bb9e05289"));
  return { errors: [...new Set(errors)], warnings: [...new Set(warnings)], refs, tree, text: dump(tree) + '\n', runtime };
}
export default { parseYAML, parseEnv, interpolate, expandTree, dump, compile };
