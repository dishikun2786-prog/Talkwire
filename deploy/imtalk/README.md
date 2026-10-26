# 云端测试服务（imtalk.openedskill.com）的部署

公开的 Talkwire 测试服务：IM 服务端、沙箱宿主与 Web 示例，给第三方开发者试用框架。使用说明在网站的[云端测试服务](https://talkwire.openedskill.com/guide/sandbox)一页；本目录是部署它的脚本与配置。

目前与网站（`deploy/site/`）共用一台服务器：`43.103.5.167`，阿里云 Ubuntu 24.04，2 核 1.6 GB。

## 组成

| 部分 | 来源 | 在服务器上 |
| --- | --- | --- |
| IM 服务端 | `server/`，交叉编译成 Linux 可执行文件 | `talkwire-im.service`，`/opt/talkwire/current/talkwire-server`，端口 6062 |
| 沙箱宿主 | `examples/sample-host/src/sandbox-main.ts`，esbuild 打成一个文件 | `talkwire-sandbox.service`，`/usr/local/bin/node /opt/talkwire/current/sandbox.mjs`，127.0.0.1:6001 |
| Web 示例 | `web/sample`，Vite 构建 | `/var/www/imtalk/current`，Caddy 直接出文件 |
| 数据库 | PostgreSQL 16（Ubuntu 自带的包） | 库与角色都叫 `talkwire_sandbox` |
| 每日清空 | `talkwire-sandbox-reset.sh` | `talkwire-sandbox-reset.timer`，每天北京时间 04:00 |
| 入口 | `imtalk.caddy` | `/etc/caddy/sites/imtalk.caddy`，由主 Caddyfile 导入 |

沙箱宿主在开发宿主的基础上加了公网需要的东西（`examples/sample-host/src/sandbox.ts`）：按空间隔离、没有管理员登录与管理接口、按网络地址限制建空间和登录的频率、每个空间的人数上限、上传的大小、次数与总量上限，以及 `/sandbox/info`。

## 安全设计

- **TLS 由 Caddy 自己完成**（Let's Encrypt 证书），所以 DNS 记录设为“仅 DNS”（浏览器直连）或经 Cloudflare 代理都能用。Cloudflare 那一侧的问题见“排查”里的 Chrome WebSocket 一条。
- **来源地址不能伪造**。宿主按 `CF-Connecting-IP` 限频。经 Cloudflare 时这个头由 Cloudflare 填；直连时 Caddy 用连接本身的地址覆盖它，客户端填什么都没用（`request_header @not_cloudflare`）。地址段写在 `imtalk.caddy` 里，见下面的“Cloudflare 地址段”。直连时用 http 访问会被 308 跳到 https。
- **两个服务只碰本机**。systemd 的 `IPAddressAllow=localhost` 与 `IPAddressDeny=any` 让它们只能和本机通信：IM 服务端虽然监听 `0.0.0.0:6062`，外部的包到不了它；数据库、宿主与 Caddy 都在本机。阿里云安全组也只开放 22、80、443。
- **内部接口不对外**。IM 服务端回调宿主走 `127.0.0.1:6001/api/v1/internal/*`；经 Caddy 进来的这类路径一律 404。
- **上传的文件不能当网页执行**。`/media/*` 加了 `Content-Security-Policy: default-src 'none'; sandbox`；宿主本来就只收白名单里的类型，并带 `nosniff`。
- **密钥只在服务器上**。`setup.sh` 用 `openssl rand` 生成数据库口令与 `JWT_SECRET`，写进 `/etc/talkwire/im.env` 与 `sandbox.env`（只有 root 能读，systemd 以 root 读取后再降权启动服务），从不打印；建数据库角色时口令经标准输入传给 psql，不出现在任何进程的参数里。
- 两个服务都用 `DynamicUser`，没有固定的系统账号，文件系统只读（`ProtectSystem=strict`），宿主只能写自己的 `StateDirectory`（`/var/lib/talkwire-sandbox`，放上传的文件）。

## 第一次部署

1. DNS：`imtalk.openedskill.com` 的 A 记录指向服务器。目前用“仅 DNS”（灰色云），浏览器直连 Caddy；原因见“排查”里的 Chrome WebSocket 一条。若改回经 Cloudflare 代理，SSL 模式与网站相同，见 `deploy/site/README.md`。
2. 把本目录除 `release.sh` 外的文件传到服务器的同一个目录，以 root 运行 `setup.sh`。它装 PostgreSQL 16、Node 24（官方构建，校验 SHA-256）、没有交换分区时建一个 1 GB 的交换文件、生成密钥、建库、装 systemd 单元与定时器、装 Caddy 站点并重新加载 Caddy。再运行一次不会改动已有的东西（密钥保留）。

   ```bash
   scp setup.sh imtalk.caddy talkwire-*.service talkwire-sandbox-reset.* root@43.103.5.167:/root/imtalk-setup/
   ssh root@43.103.5.167 'cd /root/imtalk-setup && sed -i "s/\r$//" * && bash setup.sh'
   ```

   在 Windows 上取出的文件可能带 CRLF，所以先 `sed` 一遍。

3. Caddy 重新加载后自己向 Let's Encrypt 申请证书（HTTP-01；经 Cloudflare 代理时同样能通过），通常几秒钟。
4. 在一台装好了依赖（`web/`、`host-sdk-node/`、`examples/sample-host/` 下各 `npm ci` 过）的开发机上发布第一个版本：

   ```bash
   bash deploy/imtalk/release.sh            # 默认目标 root@43.103.5.167
   ```

## 发布新版本

`release.sh` 构建三样东西（IM 服务端、打包后的宿主、Web 示例），传到服务器，放进 `/opt/talkwire/releases/<时间>-<提交>` 与 `/var/www/imtalk/releases/<时间>-<提交>`，用“新建软链再改名覆盖”的方式原子地切换两个 `current`，重启两个服务，等两者的健康检查都通过。失败时打印两个服务最近的日志并以非零退出。每样各保留最近五个版本。

回滚：把 `current` 指回上一个版本再重启。

```bash
ssh root@43.103.5.167
ls /opt/talkwire/releases/
ln -sfn releases/<上一个版本> /opt/talkwire/current.new && mv -Tf /opt/talkwire/current.new /opt/talkwire/current
systemctl restart talkwire-im talkwire-sandbox
```

数据库结构由 IM 服务端在启动时迁移；回滚到更旧的服务端前，先跑一次清空（下一节），免得旧版本遇到新结构。

## 每日清空

`talkwire-sandbox-reset.timer` 每天北京时间 04:00 运行 `/usr/local/sbin/talkwire-sandbox-reset`：停两个服务，删库重建，删掉上传的文件，再启动。宿主把空间、用户与联系人都放在内存里，重启就是另一半清空。机器在 04:00 关着时，开机后补跑一次。整个过程不到一秒。

随时可以手动清空：

```bash
systemctl start talkwire-sandbox-reset.service
journalctl -u talkwire-sandbox-reset -n 5
systemctl list-timers talkwire-sandbox-reset.timer   # 下一次的时间
```

## 验证

在开发机上从公网跑端到端检查（17 步，见网站的说明页）：

```bash
cd web && npm run build && cd ..
node tools/sandbox-check/check.mjs
```

直连源站时，证书应当能通过校验，握手里不应当有客户端证书请求，伪造的 `CF-Connecting-IP` 不起作用：

```bash
curl -s -o /dev/null -w "%{http_code} verify=%{ssl_verify_result}\n" --resolve imtalk.openedskill.com:443:43.103.5.167 https://imtalk.openedskill.com/sandbox/info   # 200 verify=0
echo | openssl s_client -connect imtalk.openedskill.com:443 -servername imtalk.openedskill.com 2>/dev/null | grep -c "Acceptable client certificate"   # 0
```

浏览器里的 WebSocket 可以用临时诊断页测：把 `deploy/imtalk/ws-diag.html` 传到 `/var/www/imtalk/current/` 下打开，它在页面、Worker、SharedWorker 里各直连一次 `/ws/im`，再连一个第三方服务器作对照。下次发布会把它连同旧版本一起换掉。

## Cloudflare 地址段

`imtalk.caddy` 里的地址段取自 <https://www.cloudflare.com/ips-v4> 与 <https://www.cloudflare.com/ips-v6>（文件里写了取的日期），用来判断一个连接是不是 Cloudflare 转来的：是就信它填的 `CF-Connecting-IP`，不是就覆盖。Cloudflare 很少改动它们；改了以后（且 DNS 经 Cloudflare 代理时），新地址段的边缘节点会被当成直连，所有经它来的用户共用一个限频额度。更新：取最新列表替换 `@not_cloudflare` 与 `@direct` 两行，然后

```bash
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile && systemctl reload caddy
```

## 排查

| 现象 | 看哪里 |
| --- | --- |
| 网页打得开，登录后连不上 | `journalctl -u talkwire-im -n 50`；`curl http://127.0.0.1:6062/api/health` |
| 登录返回 5xx | `journalctl -u talkwire-sandbox -n 50`；`curl http://127.0.0.1:6001/sandbox/info` |
| 521、522 | Caddy 没在运行，或者 Cloudflare 的地址段过时了（见上一节） |
| 525、526 | 证书：`journalctl -u caddy | grep imtalk` |
| Chrome 里一直“网络已断开”，服务器上收不到 `/ws/im`，诊断页显示 1006 | Cloudflare 在 TLS 握手里请求客户端证书（区域开了 SSL/TLS → Client Certificates，主机列表含这个域名），Chrome 的 WebSocket 一遇到证书请求就放弃（`ERR_SSL_CLIENT_AUTH_CERT_NEEDED`）。Chrome 经 DNS HTTPS 记录用 HTTP/3 加载页面后，WebSocket 要新建一次 TLS 握手，于是必然撞上；走 HTTP/2 时复用页面的会话，常常侥幸不出问题，Node 写的客户端也不受影响，所以端到端检查发现不了。确认：`openssl s_client` 输出里有 `Acceptable client certificate`。解决：DNS 记录改“仅 DNS”，或在 Cloudflare 的客户端证书主机列表里去掉这个域名 |
| 改了 `imtalk.caddy` 后重新加载失败，提示 `imtalk.log: permission denied` | 用 root 跑 `caddy validate` 时它先把日志文件建成了 root 的；删掉 `/var/log/caddy/imtalk.log` 再 `systemctl reload caddy`。重新加载失败期间 Caddy 继续用旧配置 |
| 上传返回 507 | 上传总量到了 2 GB，等下一次清空，或手动清空 |
| 内存紧张 | 两个服务各限 400 MB（`MemoryMax`）；`free -m`、`systemctl status talkwire-im` |

`ss -ltnp` 里对外监听的是 22、80、443 和 IM 服务端的 6062；6062 的包被 systemd 丢掉，从外部（包括从服务器自己的公网地址）连接都会超时，可以 `curl --connect-timeout 5 http://<服务器地址>:6062/api/health` 确认。其余（PostgreSQL 5432、宿主 6001、Caddy 管理接口 2019）只监听 127.0.0.1。
