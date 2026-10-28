import { spawn } from 'node:child_process'
import process from 'node:process'

const faker = spawn(process.execPath, ['faker/server.mjs'], { stdio: 'inherit', env: { ...process.env, NSC_FAKER_PORT: '4175' } })
const vite = spawn('npm', ['run', 'dev', '--', '--port', '5174', '--strictPort'], { stdio: 'inherit', env: { ...process.env, VITE_API_PROXY_TARGET: 'http://127.0.0.1:4175' } })
const stop = () => { faker.kill(); vite.kill() }
process.on('SIGINT', stop)
process.on('SIGTERM', stop)
faker.on('exit', () => vite.kill())
vite.on('exit', () => faker.kill())
