import { execFileSync } from 'node:child_process'
import { resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const git = (root, ...args) => execFileSync('git', ['--no-replace-objects', ...args], { cwd: root, encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] }).trim()
export const zeroBase = value => /^0{40}$|^0{64}$/.test(value)

export function genuineRoot(root) {
  // Raw commit headers preserve parents hidden by shallow history or grafts.
  if (git(root, 'rev-parse', '--is-shallow-repository') !== 'false') return false
  const headers = git(root, 'cat-file', '-p', 'HEAD').split('\n\n')[0]
  return !/^parent /m.test(headers)
}

export function initialBaseline(root, base) {
  if (!zeroBase(base)) return false
  if (!genuineRoot(root)) throw new Error('Missing baseline: only a genuine initial commit can establish coverage')
  if (git(root, 'status', '--porcelain', '--untracked-files=all'))
    throw new Error('Initial coverage baseline requires an unchanged committed source tree')
  return true
}

export function resolveBaseline(root, requested, refType) {
  if (requested && !zeroBase(requested)) {
    let base
    try { base = git(root, 'rev-parse', '--verify', `${requested}^{commit}`) }
    catch { throw new Error('Requested coverage baseline is missing; fetch the comparison history') }
    try { git(root, 'merge-base', base, 'HEAD') }
    catch { throw new Error('Coverage baseline has no available common history') }
    return base
  }
  if (genuineRoot(root)) {
    const base = '0'.repeat(git(root, 'rev-parse', 'HEAD').length)
    initialBaseline(root, base)
    return base
  }
  if (refType === 'tag' && (!requested || zeroBase(requested))) {
    try { return git(root, 'rev-parse', '--verify', 'HEAD^') }
    catch { throw new Error('Tag coverage baseline is missing; fetch the parent commit') }
  }
  throw new Error('Ordinary commits require an available comparison baseline')
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try { console.log(resolveBaseline(process.cwd(), process.env.NSC_COVERAGE_BASE, process.env.GITHUB_REF_TYPE)) }
  catch (error) { console.error(error.message); process.exitCode = 1 }
}
