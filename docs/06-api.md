# 06 · HTTP 与 WebSocket 接口设计

机器可读契约：[contracts/openapi.yaml](../contracts/openapi.yaml)。实现状态见 [implementation-status.md](implementation-status.md)。P0 业务路径已实现；未实现能力不得再依赖 501 作为占位。

## 1. 通用约定

- 基础地址为用户输入的 HTTPS origin；业务前缀 `/api/v1`。首版不支持子路径、URL 用户名/密码、查询参数或 fragment。
- JSON UTF-8；日期 RFC 3339 UTC；UUID 小写标准格式；seq 十进制字符串；二进制 base64url 无填充。
- 原生客户端 `Authorization: Bearer <access_token>`；除注册、登录、刷新、公开能力与健康检查外均认证。server-info 不暴露内部连接串或管理员配置。
- 请求体大小上限 128 KiB；分页默认 100、最大 200；配额和实际限制从 server-info 获取。
- 生命周期操作必须携带 `Idempotency-Key: <UUID>` 和 `If-Match: "<version>"`。保险库首次 PUT 使用 `If-None-Match: *`。
- HTTP 重定向：客户端不能向另一 origin 自动转发凭据；地址变更由用户显式修改配置，证书校验不跳过。
- 所有状态变更只允许 POST/PUT/DELETE 等相应方法；GET 不产生采集、恢复或永久删除副作用。

## 2. 接口清单

| 方法与路径 | 说明 | 成功结果 |
| --- | --- | --- |
| GET /healthz | 进程存活 | 200 |
| GET /readyz | 数据库可达且迁移完成后就绪 | 200 或 503 |
| GET /api/v1/server-info | 服务版本、协议、能力、限制 | 200 |
| POST /auth/register | 按 server-info 的注册模式创建账号：邀请、开放、或邮箱验证码 | 201 AuthResult |
| POST /auth/email-code | 邮箱注册模式下发送 6 位验证码 | 204 |
| POST /auth/login | 用户名或邮箱 + 密码；external 模式向配置的站点校验凭据 | 200 AuthResult |
| POST /auth/refresh | 刷新令牌轮换 | 200 TokenPair |
| POST /auth/logout | 撤销当前会话 | 204 |
| POST /auth/password | 原密码验证后设置新密码，撤销所有会话含当前 | 204 |
| GET /devices | 当前账号设备列表 | 200 |
| DELETE /devices/{id} | 撤销该设备会话及长连接 | 204 |
| GET /vault | 下载当前账号保险库信封（可含 password_wrap） | 200 或 404 VAULT_NOT_INITIALIZED |
| PUT /vault | 只允许首次初始化；不覆盖已有保险库 | 201 或 412 |
| PUT /vault/password-wrap | 登录后补写或替换密码包装信封 | 204 |
| POST /clips | 创建密文条目；条目 ID 是幂等键 | 201，新请求；200，已存在同一请求 |
| GET /clips | 按 active/trash 列出可见密文，设备/日期过滤 | 200 分页列表；没有 q 正文搜索参数 |
| GET /clips/{id} | 当前条目，ETag 为 version | 200；404 不属于该用户；410 已清理/过期 |
| DELETE /clips/{id} | 移入回收站 | 200 MutationReceipt |
| POST /clips/{id}/restore | 恢复，必须未到期 | 200 MutationReceipt |
| DELETE /clips/{id}/permanent | 永久删除回收站条目 | 200 MutationReceipt |
| GET /sync/changes | 同账号有序变化流 | 200 ChangePage |
| POST /sync/snapshots | 开始全量收敛扫描 | 201 Snapshot |
| GET /sync/snapshots/{token} | 分页扫描当前可见条目 | 200 SnapshotPage |
| GET /sync/ws | WebSocket 升级；只发变化提示 | 101 |

以上业务路径均相对 `/api/v1`。管理员创建邀请码/密码重置使用未来的本机 CLI，不提供未鉴权的公网管理接口。

设备 ID 由客户端首次安装生成并安全保存；登录提交的已存在 ID 必须属于当前账号，已撤销 ID 不能自动恢复，用户需按新设备重新登记。设备名在登记时设置，首版不提供独立在线改名接口。用户名重复注册返回 409；邀请消费、账号、同步行和初始设备/会话创建在同一事务内。

## 3. 代表性请求

创建请求（下列 `<...>` 为占位符，不是可发送的密文）：

```json
{
  "id": "44444444-4444-4444-8444-444444444444",
  "vault_id": "22222222-2222-4222-8222-222222222222",
  "source_device_id": "55555555-5555-4555-8555-555555555555",
  "format_version": 1,
  "key_epoch": 1,
  "content_type": "text/plain",
  "nonce": "<12 bytes base64url>",
  "ciphertext": "<ciphertext followed by 16-byte tag, base64url>",
  "delivery_intent": "live"
}
```

source_device_id 必须匹配认证会话，vault_id 必须归属于账号；服务端不接受客户端提供 owner。request_hash 由服务端对严格解析后的字段以固定有序、长度分隔的二进制格式计算 SHA-256，包含 intent；不能对未经规范化的原始 JSON 直接散列。原创建收据包括 id、version=1、seq、created_at。重放返回原收据只是确认创建被接受过，不代表现在仍 active；随后查询当前状态。

移入回收站：

```http
DELETE /api/v1/clips/44444444-4444-4444-8444-444444444444
Authorization: Bearer <access_token>
If-Match: "1"
Idempotency-Key: 66666666-6666-4666-8666-666666666666
```

```json
{
  "id":"44444444-4444-4444-8444-444444444444",
  "status":"trash",
  "version":2,
  "seq":"43",
  "deleted_at":"2026-09-15T03:00:00Z",
  "expires_at":"2026-09-22T03:00:00Z"
}
```

## 4. 错误与重试

统一错误体：

```json
{"error":{"code":"VERSION_CONFLICT","message":"条目状态已改变，请刷新后重试。","request_id":"..."}}
```

错误可附 `details`，仅含当前对象的必要元数据。`CLIP_PURGED` 的 details 必须给出 `id/status=purged/version/seq`，version/seq 来自永久 ID 登记中的 final_version/final_seq；客户端才能从旧 create 事件安全推进到最新墓碑。`TRASH_EXPIRED` 给出当前 trash 的 version/seq/expires_at，客户端隐藏正文并禁用恢复，不能自行伪造 purge 版本。`VERSION_CONFLICT` 可给当前版本；游标错误可给当前 sync_epoch 与 min_available_seq。跨账号 404 不返回这些细节。

| HTTP | 业务码 | 客户端处理 |
| --- | --- | --- |
| 400 | INVALID_REQUEST / UNSUPPORTED_FORMAT | 展示字段错误，禁止原样死循环重试 |
| 401 | UNAUTHENTICATED / TOKEN_EXPIRED | 协调刷新一次，失败回登录 |
| 403 | DEVICE_REVOKED / REGISTRATION_DISABLED | 停止受限操作，展示原因 |
| 404 | NOT_FOUND / VAULT_NOT_INITIALIZED | 仅本人保险库未初始化可进入初始化；跨账号资源统一 NOT_FOUND |
| 409 | IDEMPOTENCY_CONFLICT / SYNC_RESET_REQUIRED / QUOTA_EXCEEDED | 冲突人工处理、重建缓存或管理容量 |
| 410 | CLIP_PURGED / TRASH_EXPIRED / CURSOR_EXPIRED / SNAPSHOT_EXPIRED | 更新墓碑、禁用恢复，或开始全量扫描 |
| 412 | VERSION_CONFLICT / VAULT_EXISTS | 刷新当前对象；不能覆盖保险库 |
| 413 | PAYLOAD_TOO_LARGE | 明确跳过，不能静默截断 |
| 428 | PRECONDITION_REQUIRED | 修复客户端，补 If-Match / If-None-Match |
| 429 | RATE_LIMITED | 按 Retry-After 退避 |
| 501 | NOT_IMPLEMENTED | 骨架能力，不显示“已同步” |
| 503 | NOT_READY / TEMPORARILY_UNAVAILABLE | 保留队列并退避 |

## 5. 分页与过滤

普通列表使用不透明 page_token，绑定账号、status、来源设备与日期过滤、最后 `(created_seq,id)`。列表按创建序号倒序，不以客户端时间排序。翻页期间状态可能变化，UI 根据 ID 合并；可靠同步使用 sync 接口，不能用历史列表分页代替。

`GET /clips?status=trash` 默认只返回未到期回收站密文。到期条目可以从另一个纯元数据展示路径在后续扩展；首版无需展示“清理中”行。返回条目包含服务器计算的 deleted_at/expires_at。

## 6. 版本与能力发现

server-info 返回 `protocol_version=1`、`stage`、`sync_available`、`e2ee_available`、`registration_mode`（`invite` / `open` / `email` / `external` / `disabled`）、`email_verification`、`min_password_chars`、`password_wrap`、可选 `external_register_url`、`max_text_bytes`、`max_request_bytes`、`trash_retention_seconds`。当前 skeleton 中可用性均为 false，注册 disabled。`external` 模式下客户端隐藏本应用注册，引导到 `external_register_url`。

正式客户端连接到 skeleton 时显示“服务器尚未完成业务功能”，不进入登录/采集。服务端版本不兼容时返回清晰升级提示，禁止把未知加密版本当明文显示。REST 规范不完整表达 WebSocket 帧，帧语义以 [同步文档](04-sync-protocol.md) 为准。
