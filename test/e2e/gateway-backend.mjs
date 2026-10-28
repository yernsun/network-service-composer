// Bounded streams make graceful drain observable without pretending that an
// established connection can migrate to another Gateway process.
import { createServer } from 'node:http'
import { createHash } from 'node:crypto'

const server = createServer((request, response) => {
  if (request.url === '/events') {
    response.writeHead(200, {
      'Content-Type': 'text/event-stream',
      'Cache-Control': 'no-cache',
      'X-Accel-Buffering': 'no',
    })
    let sequence = 0
    response.write('data: 0\n\n')
    const timer = setInterval(() => {
      sequence += 1
      response.write(`data: ${sequence}\n\n`)
      if (sequence === 20) {
        clearInterval(timer)
        response.end()
      }
    }, 200)
    response.on('close', () => clearInterval(timer))
    return
  }
  response.writeHead(200, { 'Content-Type': 'text/plain' })
  response.end('gateway-acceptance\n')
})

server.on('upgrade', (request, socket) => {
  if (request.url !== '/socket' || !request.headers['sec-websocket-key']) {
    socket.destroy()
    return
  }
  const accept = createHash('sha1')
    .update(`${request.headers['sec-websocket-key']}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
    .digest('base64')
  socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`)
  let sequence = 0
  const send = () => {
    const payload = Buffer.from(`tick-${sequence}`)
    socket.write(Buffer.concat([Buffer.from([0x81, payload.length]), payload]))
  }
  send()
  const timer = setInterval(() => {
    sequence += 1
    send()
    if (sequence === 20) {
      clearInterval(timer)
      socket.end(Buffer.from([0x88, 0x00]))
    }
  }, 200)
  socket.on('error', () => socket.destroy())
  socket.on('close', () => clearInterval(timer))
})

server.listen(8080, '0.0.0.0')
