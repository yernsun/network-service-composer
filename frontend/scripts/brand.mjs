// Explicit branding adaptation of the preserved frontend archive.
export function adaptBrand(source) {
  return source
    .replaceAll("title: 'Nginx Service Composer'", "title: 'Network Service Composer for Nginx'")
    .replaceAll('Nginx Service Composer', 'Network Service Composer')
    .replaceAll('nginx-service-composer-vue-v1.3', 'network-service-composer-vue-v1.3')
}
