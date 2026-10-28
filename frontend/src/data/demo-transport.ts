export const transport: typeof fetch = async () => {
  throw new Error('Business API requests are unavailable in the isolated demo')
}
