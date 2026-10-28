import { execFileSync } from 'node:child_process'
import { existsSync } from 'node:fs'
import { dirname, join } from 'node:path'

const go = process.env.GO || 'go'
const goRoot = execFileSync(go, ['env', 'GOROOT'], { encoding: 'utf8' }).trim()
const files = [...new Set(execFileSync('git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard'], { encoding: 'utf8' }).split('\0'))].filter(file => file.endsWith('.go') && existsSync(file))
const output = execFileSync(join(goRoot, 'bin', 'gofmt'), ['-l', ...files], { encoding: 'utf8', cwd: dirname(new URL('../../go.mod', import.meta.url).pathname) })
if (output.trim()) { console.error(output); process.exitCode = 1 }
