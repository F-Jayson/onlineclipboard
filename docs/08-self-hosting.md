# 08 · 自托管部署与运维

## 1. 当前能部署什么

可用 Compose 启动 PostgreSQL 与 API（自动迁移），或本机 `go run ./cmd/clipd` 连接已有数据库。账号、密文同步和回收站已实现。模板仍使用本地源码构建，没有预推送的发布镜像或默认管理员密码。

开发机曾使用 `postgres:16-alpine` 完成集成测试；Compose 默认写 `postgres:17-alpine`。若镜像拉取失败，可将 `image` 改为 `postgres:16-alpine`，迁移兼容。

## 2. 本地 Compose 启动

前提：Docker Engine / Docker Desktop 的 Linux 容器环境，以及 Compose v2。以下命令在 `deploy` 目录执行。

```powershell
Copy-Item .env.example .env
```

把 `POSTGRES_PASSWORD` 改为随机长密码，把 `CLIP_DOMAIN` 改为自有域名。`.env` 不提交仓库。

```powershell
docker compose --env-file .env config --quiet
docker compose --env-file .env up -d --build
Invoke-RestMethod http://127.0.0.1:8080/healthz
Invoke-RestMethod http://127.0.0.1:8080/readyz
Invoke-RestMethod http://127.0.0.1:8080/api/v1/server-info
```

生成邀请码（邀请注册模式）：

```powershell
docker compose --env-file .env exec api clipd admin invite
```

查看状态/停止：

```powershell
docker compose ps
docker compose logs --tail 100 api
docker compose stop
```

不要把带变量展开的 `docker compose config` 完整输出贴到工单，可能包含密码；校验时使用 `--quiet`。

## 3. 域名 HTTPS

1. 给主机配置固定地址，域名 A/AAAA 记录指向可访问的地址；公网场景开放 TCP 80/443 到 Caddy，检查防火墙与 NAT。
2. `.env` 的 CLIP_DOMAIN 设置为实际域名，如 `clip.example.com`，不要带路径。
3. 执行以下命令；Caddy 为可验证域名申请证书，实际可用性取决于 DNS、网络与 CA 验证。

```powershell
docker compose --env-file .env --profile https up -d --build
```

客户端最终填 `https://clip.example.com`。模板只把 API 8080 绑定到主机 loopback，数据库不发布公网端口。Caddy 与 API 通过容器服务名互访；WSS 由 Caddy reverse_proxy 升级。`/readyz` 在数据库就绪后应为 200。

## 4. IP、局域网、NAS 与私有证书

客户端允许 IP，不要求购买域名；但 IP 访问也必须解决 HTTPS 信任和证书中的 IP SAN（Subject Alternative Name）匹配。

| 场景 | 方案 |
| --- | --- |
| 自有域名 + 公网服务器 | Caddy 域名自动证书，最少客户端配置 |
| 局域网 IP / NAS | 使用私有 CA 签发包含该 IP SAN 的证书；客户端按受控流程信任此 CA |
| 公网 IP | 使用可签发对应 IP SAN 的证书提供方并验证其续期支持，或使用私有 CA；不要假定所有 CA/当前代理都自动支持 |
| VPN/内网穿透 | 服务证书仍需匹配客户端填写地址；传输工具不代替账号认证与证书校验 |
| 开发机本地 | curl/PowerShell 使用 loopback HTTP 验证骨架；正式客户端不因此启用任意明文地址 |

LAN 私有 CA 模板为 [Caddyfile.ip.example](../deploy/Caddyfile.ip.example)：复制其内容到实际 Caddyfile 并替换 IP，再重启代理。使用 `tls internal` 时，Caddy CA 公共根证书通常在其 `/data/caddy/pki/authorities/local/root.crt`；可以用容器复制命令导出**公共证书**。不要导出或分享 root.key。

Windows 由用户/管理员按范围导入信任根；Android 现代应用不默认信任全部用户安装 CA，正式客户端应实现应用专属 CA 导入或明确的 Network Security Config 策略，展示指纹并让用户通过可信渠道核对。Android `ApiClient` 支持可选自定义 CA PEM，连接界面尚未提供导入入口，因此 IP 私有 CA 场景仍须手工配置或后续补 UI 后才能作为完整验收项。

首版 P0 需要完成此受控信任流程才能满足 IP 自托管。不得用 `TrustAllCertificates`、忽略 hostname 验证或 release 全局允许 HTTP 解决证书问题。Caddy 官方自动 HTTPS 与私有 CA 行为见 [资料](11-references.md)。

## 5. 配置

`clipd` 启动时读取下列环境变量。Compose 已传入数据库 URL 与注册模式。

| 配置 | 建议/说明 |
| --- | --- |
| CLIP_HTTP_ADDR | 默认 `127.0.0.1:8080`；容器内 `0.0.0.0:8080` |
| CLIP_DATABASE_URL | PostgreSQL DSN，必填（也可用 CLIP_DATABASE_URL_FILE） |
| CLIP_DATABASE_URL_FILE | 容器 secret 文件，包含 PostgreSQL DSN；日志隐藏 |
| CLIP_REGISTRATION_MODE | invite 默认；disabled 关闭新注册；open 必须显式开启 |
| CLIP_PUBLIC_ORIGIN | 可选对外 origin 记录 |
| CLIP_MAX_TEXT_BYTES | 默认 65536 |
| CLIP_MAX_ITEMS / CLIP_MAX_CIPHERTEXT_BYTES | 默认 10000 / 104857600 |
| CLIP_TRASH_RETENTION_SECONDS | v1 固定 604800 |
| CLIP_EVENT_RETENTION_DAYS | 默认 30 |
| CLIP_LOG_LEVEL | 默认 info，任何级别都不打印秘密 |

管理命令：

```powershell
go run ./cmd/clipd migrate
go run ./cmd/clipd admin invite
go run ./cmd/clipd admin reset-password <username>
go run ./cmd/clipd admin rotate-sync-epoch
```

重置密码会撤销该账号全部会话。轮换 sync_epoch 将迫使所有客户端全量校准。

## 6. 上线前部署闭环

1. 启动时自动迁移，创建随机 server_id/sync_epoch；生产环境应为数据库角色只授予所需表读写权限。
2. `/readyz` 检查数据库连接；清理作业在进程内每分钟运行。
3. 通过 `clipd admin invite` 生成一次性邀请码，客户端注册并生成恢复密钥，邀请不能出现在公共日志中。
4. 将 Dockerfile/Compose 中浮动系列镜像锁定到验证过的补丁版本或 digest；发布可追溯 Go/.NET/Android 工具链和依赖锁。
5. 用两台独立设备完成 HTTPS、加密、同步、删除恢复、断线补偿、配额与恢复演练。
6. 提供签名 Windows 安装包/APK。客户端的服务器 URL 为运行时配置；更换服务地址无需重新打包。

## 7. 备份、恢复和清理

备份对象：PostgreSQL 全库（含保险库、设备、事件、墓碑、server_state）、迁移版本、部署配置、必要服务器密钥、TLS 配置。CMK/RK 属于用户，服务端备份不能代替用户恢复密钥。

可用容器内 pg_dump 导出到临时文件，再 docker compose cp 复制，避免 PowerShell 将二进制 stdout 重定向成文本：

```powershell
docker compose exec -T postgres pg_dump -U onlineclipboard -d onlineclipboard -Fc -f /tmp/onlineclipboard.dump
docker compose cp postgres:/tmp/onlineclipboard.dump ./onlineclipboard.dump
```

上例只是导出路径示例；备份文件包含敏感元数据/认证摘要，需移至加密备份存储并移除临时副本。不要把备份提交仓库。正式计划建议每日备份、保留 14 天，恢复点目标 RPO ≤24h、恢复时间目标 RTO ≤2h；必须通过实际演练确认。

恢复顺序：隔离新环境 → 校验备份与版本 → 使用 pg_restore 恢复 → 执行兼容迁移 → **更换 sync_epoch、撤销所有会话/刷新族、删除扫描会话** → 检查备份回滚导致的设备撤销/删除回退 → 通知用户重新登录与重新校准 → 验证后切换入口。更换世代发生在开放客户端连接前。

备份恢复可能恢复用户已删除的密文、丢失备份之后的写入，需明确告知，必要时用独立审计/删除记录重新清理。不能把“新世代全量同步”描述为解决了备份以来的数据损失。

回收站在线清理 7 天，备份保留 14 天，这两个周期必须一起告知用户；旧备份应自动过期，不承诺介质安全擦除。永久删除操作不自动删除已离线的其他设备副本。

## 8. 升级、监控与排障

- 升级前备份和读取变更说明；先兼容性扩展迁移，再部署代码，最后在后续版本删除旧字段。破坏性数据库变更不依赖盲目 down migration 回滚，走已演练的备份恢复。
- 监控：请求成功率/P95、数据库连接、活跃连接数、清理积压条数、最旧逾期时长、事件保留边界、磁盘空间、失败登录、配额拒绝。指标禁止以正文或高基数条目 ID 作 label。
- 清理延迟 >5 分钟、备份失败、证书即将到期、磁盘逼近容量时告警。无正文健康探测不应读取某个真实用户剪贴板。
- 手机无法同步先检查运行模式/是否前台/会话/解锁/证书/网络；不要把所有问题归为省电策略。`/readyz` 503 表示数据库未就绪。
- 服务器迁移到新地址时先备份保留用户 ID/保险库，客户端用显式迁移连接流程并重新验证身份；直接换 IP 不自动信任新证书或转发旧凭据。
