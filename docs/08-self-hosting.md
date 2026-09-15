# 08 · 自托管部署与运维

## 1. 当前能部署什么

现在可以本机启动 Go 外壳，或用 Compose 构建外壳并接入 Caddy；可选启动 PostgreSQL 作为未来开发环境。**当前没有可用账号或同步业务，数据库不会被 API 使用，迁移不会自动执行，不能向用户提供生产同步服务。**

模板没有真实发布镜像、默认管理员密码或真实域名。使用本地源码构建，不执行任何第三方一键脚本。

## 2. 本地骨架启动

前提：Docker Engine / Docker Desktop 的 Linux 容器环境，以及支持本模板 Compose Specification 的现代 Compose（v2 或兼容后续版本），或按根目录 README 使用 Go 本地启动。以下 Compose 命令在 `deploy` 目录执行。

```powershell
Copy-Item .env.example .env
```

用编辑器把 POSTGRES_PASSWORD 改为随机长密码，把 CLIP_DOMAIN 改为自有域名。即使只启动 API，Compose 插值也可能检查整个文件，因此先配置密码。`.env` 不提交仓库。

```powershell
docker compose --env-file .env config --quiet
docker compose --env-file .env up -d --build api
Invoke-RestMethod http://127.0.0.1:8080/api/v1/server-info
```

可选同时启动数据库基础设施：

```powershell
docker compose --env-file .env --profile infrastructure up -d --build
```

查看状态/停止：

```powershell
docker compose ps
docker compose logs --tail 100 api
docker compose stop
```

不要把带变量展开的 `docker compose config` 完整输出贴到工单，可能包含密码；校验时使用 `--quiet`。本模板不包含数据库初始化挂载，避免误以为有完整迁移系统。

## 3. 域名 HTTPS

1. 给主机配置固定地址，域名 A/AAAA 记录指向可访问的地址；公网场景开放 TCP 80/443 到 Caddy，检查防火墙与 NAT。
2. `.env` 的 CLIP_DOMAIN 设置为实际域名，如 `clip.example.com`，不要带路径。
3. 执行以下命令；Caddy 为可验证域名申请证书，实际可用性取决于 DNS、网络与 CA 验证。

```powershell
docker compose --env-file .env --profile infrastructure --profile https up -d --build
```

客户端最终填 `https://clip.example.com`。模板只把 API 8080 绑定到主机 loopback，数据库不发布公网端口。Caddy 与 API 通过容器服务名互访；需要 WSS 的代理升级由 Caddy reverse_proxy 处理。

当前 healthcheck 为进程探测，所以 skeleton 可启动代理，但 `/readyz` 仍为 503。正式上线前改为真实就绪门槛，并完成认证、数据库迁移、备份和清理作业验收。

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

Windows 由用户/管理员按范围导入信任根；Android 现代应用不默认信任全部用户安装 CA，正式客户端应实现应用专属 CA 导入或明确的 Network Security Config 策略，展示指纹并让用户通过可信渠道核对。**当前 Android 骨架未实现私有 CA 导入，因此 IP 私有 CA 场景尚不能作为已支持功能验收。**

首版 P0 需要完成此受控信任流程才能满足 IP 自托管。不得用 `TrustAllCertificates`、忽略 hostname 验证或 release 全局允许 HTTP 解决证书问题。Caddy 官方自动 HTTPS 与私有 CA 行为见 [资料](11-references.md)。

## 5. 正式版配置设计（当前未消费）

当前 Go 配置只有 CLIP_HTTP_ADDR；下表是 M1–M5 要实现的配置，不能现在写入 `.env` 就假定生效。

| 配置 | 建议/说明 |
| --- | --- |
| CLIP_PUBLIC_ORIGIN | 对外 HTTPS 根地址；启动时校验 |
| CLIP_DATABASE_URL_FILE | 容器 secret 文件，包含 PostgreSQL DSN；日志隐藏 |
| CLIP_REGISTRATION_MODE | invite 默认；disabled 关闭新注册；open 必须显式开启 |
| CLIP_MAX_TEXT_BYTES | 默认 65536；服务器限制解码密文长度，客户端限制正文 |
| CLIP_MAX_ITEMS / CLIP_MAX_CIPHERTEXT_BYTES | 默认 10000 / 104857600 |
| CLIP_TRASH_RETENTION_SECONDS | v1 固定 604800，不提供任意改变产品语义的开关 |
| CLIP_EVENT_RETENTION_DAYS | 默认 30，变更要保证客户端游标过期重建 |
| CLIP_LOG_LEVEL | 默认 info，任何级别都不打印秘密 |
| CLIP_TRUSTED_PROXY_CIDRS | 仅信任实际代理来源；不用任意 X-Forwarded-For 做限流身份 |

计划提供 `clipd migrate`、`clipd admin invite`、`clipd admin reset-password`、`clipd admin rotate-sync-epoch` 管理命令，但当前不存在，**不要直接执行这些示例命令**。实现后更新为精确的可复制命令、帮助文本和错误处理。

## 6. 上线前部署闭环

1. 实现并运行迁移工具，创建随机 server_id/sync_epoch；运行数据库角色只授予所需表读写权限。
2. 添加数据库配置与就绪检查；启动完成必须检查 schema 版本、连接、作业和认证配置。
3. 通过本机 CLI 生成一次性邀请码，客户端注册并生成恢复密钥，邀请不能出现在公共日志中。
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
- 手机无法同步先检查运行模式/是否前台/会话/解锁/证书/网络；不要把所有问题归为省电策略。出现 501 表示仍在骨架版本，503 ready 表示未完成就绪。
- 服务器迁移到新地址时先备份保留用户 ID/保险库，客户端用显式迁移连接流程并重新验证身份；直接换 IP 不自动信任新证书或转发旧凭据。
