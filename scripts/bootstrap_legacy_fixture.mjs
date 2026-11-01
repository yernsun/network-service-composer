// Transform only an explicitly owned acceptance fixture into the historical
// four-component HTTP layout. This is not an installation/migration command.
import assert from 'node:assert/strict'
import { parse, stringify } from '../frontend/node_modules/yaml/dist/index.js'
let raw = ''; for await (const chunk of process.stdin) raw += chunk
const input = JSON.parse(raw)
const value = parse(input.configuration)
assert.equal(value.storage.driver, 'postgres')
assert.equal(value.controller.local_gateway, false)
assert(value.controller.system_project.services.some(service => service.object_id === input.executor))
value.controller.installation.metadata_directory = ''
value.server.console_https_listen = ''
value.server.console_https_cert_file = ''
value.server.console_https_key_file = ''
value.controller.system_project.services = value.controller.system_project.services.filter(service => service.object_id !== input.executor)
for (const application of value.controller.system_project.applications) application.object_ids = application.object_ids.filter(id => id !== input.executor)
process.stdout.write(stringify(value))
