export const transport: typeof fetch = (input, init) => {
  if (input instanceof Request) {
    const url = new URL(input.url)
    // Chi retains the escaped path parameter. A standalone node's canonical
    // ID contains a colon, which is legal inside this path segment.
    const pathname = url.pathname.replace(/(\/cluster\/nodes\/)engine%3A([^/]+)/i, '$1engine:$2')
    if (pathname !== url.pathname) {
      url.pathname = pathname
      input = new Request(url, input)
    }
  }
  return fetch(input, init)
}
