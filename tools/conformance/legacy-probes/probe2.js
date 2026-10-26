const crypto = require('crypto');
const WebSocket = require('ws'); // npm install ws
const [port, secret] = process.argv.slice(2);
const b64u = (b) => Buffer.from(b).toString('base64url');
const jwt = (c) => { const h = b64u(JSON.stringify({alg:'HS256',typ:'JWT'})); const p = b64u(JSON.stringify({iat:Math.floor(Date.now()/1000),exp:Math.floor(Date.now()/1000)+600,...c})); return `${h}.${p}.${crypto.createHmac('sha256',secret).update(h+'.'+p).digest('base64url')}`; };
function once(token, extra) { return new Promise((res) => { const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/im`); const got = []; ws.on('message', (d) => got.push(JSON.parse(d.toString()))); ws.on('open', () => { ws.send(JSON.stringify({ id: 'L1', action: 'login', token })); if (extra) setTimeout(() => ws.send(JSON.stringify(extra)), 400); setTimeout(() => { ws.close(); res(got); }, 1200); }); }); }
(async () => {
  const A = 'probe2-a-' + Date.now();
  for (const [label, claims, extra] of [
    ['access', { sub: A, role: 'app_user', type: 'access' }, { id: 'P1', action: 'pres', topic: '*' }],
    ['refresh', { sub: A, role: 'app_user', type: 'refresh' }],
    ['agent', { sub: A, role: 'app_user', type: 'agent', deviceId: 'x' }],
    ['admin', { sub: 'admin-' + Date.now(), role: 'superadmin', type: 'access' }],
    ['expired', { sub: A, type: 'access', exp: Math.floor(Date.now()/1000) - 60 }],
  ]) console.log(label.padEnd(8), JSON.stringify(await once(jwt(claims), extra)).slice(0, 300));
  process.exit(0);
})();
