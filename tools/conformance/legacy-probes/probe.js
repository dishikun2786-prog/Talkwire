// Probe the oracle im-go in "no host" mode. Verifies a handful of behaviours
// the framework plan depends on. Usage: node probe.js <port> <jwtSecret> (needs the ws package)
const crypto = require('crypto');
const WebSocket = require('ws'); // npm install ws

const [port, secret] = process.argv.slice(2);
const b64u = (b) => Buffer.from(b).toString('base64url');
function jwt(claims) {
  const h = b64u(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
  const p = b64u(JSON.stringify({ iat: Math.floor(Date.now() / 1000), exp: Math.floor(Date.now() / 1000) + 600, ...claims }));
  const s = crypto.createHmac('sha256', secret).update(h + '.' + p).digest('base64url');
  return `${h}.${p}.${s}`;
}
const imUid = (id) => 'uid_' + crypto.createHash('sha256').update(id).digest().subarray(0, 8).toString('base64url');

function connect() {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(`ws://127.0.0.1:${port}/ws/im`);
    const inbox = [];
    const waiters = [];
    ws.on('message', (d) => {
      const m = JSON.parse(d.toString());
      const i = waiters.findIndex((w) => w.pred(m));
      if (i >= 0) waiters.splice(i, 1)[0].resolve(m); else inbox.push(m);
    });
    ws.on('open', () => resolve({
      ws, inbox,
      send(msg) { ws.send(JSON.stringify(msg)); },
      wait(pred, ms = 4000) {
        const i = inbox.findIndex(pred);
        if (i >= 0) return Promise.resolve(inbox.splice(i, 1)[0]);
        return new Promise((res) => {
          const w = { pred, resolve: res };
          waiters.push(w);
          setTimeout(() => { const k = waiters.indexOf(w); if (k >= 0) { waiters.splice(k, 1); res({ timeout: true }); } }, ms);
        });
      },
      async rpc(msg) { this.send(msg); return this.wait((m) => m.id === msg.id); },
    }));
    ws.on('error', reject);
  });
}
const short = (m) => JSON.stringify(m).slice(0, 220);
let n = 0; const id = () => 'p' + (++n);

(async () => {
  const A = 'probe-user-a-' + Date.now(), B = 'probe-user-b-' + Date.now();
  const a = await connect();
  const b = await connect();

  const la = await a.rpc({ id: id(), action: 'login', token: jwt({ sub: A, role: 'app_user', type: 'access' }) });
  console.log('1 login access          ->', short(la), '| expected uid', imUid(A), 'match=', la.uid === imUid(A));
  const lb = await b.rpc({ id: id(), action: 'login', token: jwt({ sub: B, role: 'app_user', type: 'access' }) });
  console.log('1b login B              ->', short(lb));
  console.log('2 relogin same conn     ->', short(await a.rpc({ id: id(), action: 'login', token: jwt({ sub: A, type: 'access' }) })));

  console.log('3 sub p2p(no host)      ->', short(await a.rpc({ id: id(), action: 'sub', topic: imUid(B) })));

  const cg = await a.rpc({ id: id(), action: 'create_grp', name: 'probe', members: [] });
  console.log('4 create_grp no members ->', short(cg));
  const topic = cg.topic;
  console.log('4b sub grp              ->', short(await a.rpc({ id: id(), action: 'sub', topic })));
  const p1 = await a.rpc({ id: id(), action: 'pub', topic, content: 'hello', contentType: 'text/plain', clientMsgId: 'cm-1' });
  const p2 = await a.rpc({ id: id(), action: 'pub', topic, content: 'hello', contentType: 'text/plain', clientMsgId: 'cm-1' });
  console.log('5 pub                   ->', short(p1));
  console.log('5b pub dup clientMsgId  ->', short(p2), '| same seq=', p1.seq === p2.seq);
  const g = await a.rpc({ id: id(), action: 'get', topic, limit: 20 });
  console.log('6 get                   ->', short(g));
  console.log('7 B sub grp(not member) ->', short(await b.rpc({ id: id(), action: 'sub', topic })));
  console.log('8 pres *                ->', short(await a.rpc({ id: id(), action: 'pres', topic: '*' })));
  const dt = await a.rpc({ id: id(), action: 'del_topic', topic });
  console.log('9 del_topic(owner)      ->', short(dt));
  const g2 = await a.rpc({ id: id(), action: 'get', topic, limit: 20 });
  console.log('9b get after clear      ->', short(g2));

  const c = await connect();
  console.log('10 login refresh-type   ->', short(await c.rpc({ id: id(), action: 'login', token: jwt({ sub: A, role: 'app_user', type: 'refresh' }) })));
  const d = await connect();
  console.log('11 login agent-type     ->', short(await d.rpc({ id: id(), action: 'login', token: jwt({ sub: A, role: 'app_user', type: 'agent', deviceId: 'x' }) })));
  const e = await connect();
  console.log('12 unknown action       ->', short(await e.rpc({ id: id(), action: 'login', token: jwt({ sub: B + '-e', type: 'access' }) }) && await e.rpc({ id: id(), action: 'no_such_action' })));
  console.log('13 frames seen on A not matched by id:', a.inbox.map((m) => m.action || m.type).join(','));
  for (const x of [a, b, c, d, e]) x.ws.close();
  setTimeout(() => process.exit(0), 300);
})().catch((err) => { console.error('PROBE_ERROR', err); process.exit(1); });
