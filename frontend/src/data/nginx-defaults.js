export const NGINX_MANAGED_DEFAULTS = Object.freeze({
  bodySize: '1m',
  connectTimeoutSeconds: 60,
  readTimeoutSeconds: 60,
})

// Invalid persisted values must never be interpolated into an NGINX directive.
// These values match the corresponding NGINX defaults so the generated file
// remains explicit while preserving the behavior of an omitted directive.
export function effectiveManagedNginxConfig(config) {
  const validSize = typeof config.body === 'string' && /^[0-9]+[kKmMgG]?$/.test(config.body)
  return {
    body: validSize ? config.body : NGINX_MANAGED_DEFAULTS.bodySize,
    bodyFallback: !validSize,
    route(route) {
      const connectValid = Number.isInteger(route.connect) && route.connect >= 1 && route.connect <= 300
      const readValid = Number.isInteger(route.timeout) && route.timeout >= 1 && route.timeout <= 3600
      return {
        connect: connectValid ? route.connect : NGINX_MANAGED_DEFAULTS.connectTimeoutSeconds,
        read: readValid ? route.timeout : NGINX_MANAGED_DEFAULTS.readTimeoutSeconds,
        connectFallback: !connectValid,
        readFallback: !readValid,
      }
    },
  }
}
