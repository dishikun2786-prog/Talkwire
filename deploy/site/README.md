# 网站部署

网站与开发手册（`site/`，VitePress）由 GitHub Actions 构建，经 SSH 传到服务器上的一个新目录，再把 `current` 指向它。切换是一次改名，访问者看不到半截的站点；出了问题把 `current` 指回上一个目录就是回滚。

| 文件 | 作用 |
| --- | --- |
| `.github/workflows/site.yml` | 在 `main` 上每次推送后构建并部署；也可以在 Actions 页面手动运行 |
| `deploy/site/Caddyfile` | 服务器上的站点配置：静态文件、干净 URL、缓存头、404 页 |

服务器准备好之前，工作流照常构建，跳过部署这一步并在运行摘要里说明原因，不会报错。

## 服务器上做一次

以下命令在服务器上以 root 执行。系统按 Debian/Ubuntu 写，别的发行版换成对应的包管理器。

1. 安装 Caddy（官方仓库，见 caddyserver.com/docs/install）与 rsync（`apt install rsync`），确认 `systemctl status caddy` 在运行。
2. 建一个只用来放网站的账号，它没有 sudo：

   ```bash
   useradd --system --create-home --home-dir /var/www/talkwire --shell /bin/bash talkwire
   install -d -o talkwire -g talkwire -m 755 /var/www/talkwire/releases
   install -d -o talkwire -g talkwire -m 700 /var/www/talkwire/.ssh
   ```

3. 在自己的电脑上生成部署用的密钥对，私钥只放进 GitHub：

   ```bash
   ssh-keygen -t ed25519 -N "" -C talkwire-site-deploy -f talkwire-site-deploy
   ```

   把公钥 `talkwire-site-deploy.pub` 的内容写进服务器的 `/var/www/talkwire/.ssh/authorized_keys`，然后：

   ```bash
   chown talkwire:talkwire /var/www/talkwire/.ssh/authorized_keys
   chmod 600 /var/www/talkwire/.ssh/authorized_keys
   ```

4. 把 `Caddyfile` 放到 `/etc/caddy/Caddyfile`（已有其他站点时，把站点块并进去），检查后重新加载：

   ```bash
   caddy validate --config /etc/caddy/Caddyfile
   systemctl reload caddy
   ```

5. 云服务器的安全组放行入站 TCP 80 与 443。
6. DNS：`talkwire.openedskill.com` 的 A 记录指向服务器。Caddy 首次访问时自动申请证书。

   域名在 Cloudflare 上且开着代理（橙色云）时，有两种做法：先设为“仅 DNS”，等 Caddy 拿到证书后再打开代理；或者用 Cloudflare 的源站证书，在站点块里加 `tls /etc/caddy/origin.pem /etc/caddy/origin.key`，并把 SSL 模式设为“完全（严格）”。

## GitHub 上做一次

仓库的 Settings → Secrets and variables → Actions，新建三个 secret：

| 名称 | 内容 |
| --- | --- |
| `SITE_SSH_KEY` | 第 3 步生成的私钥文件 `talkwire-site-deploy` 的全部内容 |
| `SITE_HOST` | 服务器的地址 |
| `SITE_KNOWN_HOSTS` | 服务器的主机公钥，在自己的电脑上用 `ssh-keyscan -t ed25519 <服务器地址>` 取得，核对指纹与服务器上 `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` 的输出一致后再填 |

工作流只用这把密钥登录 `talkwire` 账号，写 `/var/www/talkwire` 下的文件，不需要 root。

## 回滚

```bash
sudo -u talkwire ls -1t /var/www/talkwire/releases        # 新的在前
sudo -u talkwire ln -sfn releases/<上一个目录> /var/www/talkwire/current.next
sudo -u talkwire mv -Tf /var/www/talkwire/current.next /var/www/talkwire/current
```

工作流每次部署后保留最近 5 个目录，更早的删掉。
