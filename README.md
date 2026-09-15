# 云剪贴板 · OnlineClipboard

一个支持用户自托管的文字剪贴板同步项目：Windows 与 Android 使用同一服务器、同一账号，在客户端加密文字，服务端保存密文；客户端提供历史搜索、删除、恢复及 7 天回收站。

**当前交付是需求/设计文档与工程骨架，不是已经完成的同步软件。** 服务端可以启动并提供健康检查和能力信息；业务接口明确返回 `501 NOT_IMPLEMENTED`；两端仅有界面入口和接口定义。尚未实现账号、加密、数据库访问、剪贴板监听、同步与清理任务。

## 从这里开始

| 内容 | 文档 |
| --- | --- |
| 文档目录、阅读顺序、决策摘要 | [文档导航](docs/README.md) |
| 产品目标、范围、用户故事与验收 | [项目需求](docs/01-requirements.md) |
| 三端架构、模块与技术选型 | [架构设计](docs/02-architecture.md) |
| 端到端加密、账号、设备和恢复密钥 | [安全设计](docs/03-security.md) |
| 同步游标、断线补偿、防循环和冲突 | [同步协议](docs/04-sync-protocol.md) |
| 数据表、索引和回收站生命周期 | [数据设计](docs/05-data-model.md) |
| HTTP / WebSocket 接口与错误处理 | [接口设计](docs/06-api.md)、[OpenAPI](contracts/openapi.yaml) |
| Windows / Android 开发指导及页面 | [客户端设计](docs/07-clients.md) |
| 自托管、HTTPS、IP 访问、备份和升级 | [部署运维](docs/08-self-hosting.md) |
| 开发顺序、任务拆解和交付标准 | [开发计划](docs/09-development-plan.md) |
| 测试矩阵与当前验证记录 | [测试验收](docs/10-testing.md) |
| 平台官方资料与核实日期 | [参考资料](docs/11-references.md) |

## 关键边界

- Windows 登录用户会话中可监听文字复制；服务端不接触剪贴板明文或内容主密钥。
- Android 10+ 普通应用在后台读取其他应用剪贴板受系统限制；后台网络还受省电机制影响。普通模式提供前台同步、分享上传与点击复制。可选默认输入法增强模式必须先做真机验证，不能把“安装后任何状态都可直接粘贴”写成已支持能力。
- 同账号首次接入新设备，还需输入独立的恢复密钥解锁密文。登录密码与恢复密钥用途不同。
- 查询正文在客户端解密后完成。回收站从服务端确认删除时起保留 168 小时，到期禁止恢复，清理任务目标在 5 分钟内删除在线密文。

## 技术栈

服务端：Go 1.26 / PostgreSQL 17 / REST + WebSocket；Windows：.NET 10 + WPF；Android：Kotlin + 原生 Android（后续采用 Compose）；部署：Docker Compose + Caddy。一个仓库管理协议和三端，首版无需 Redis、对象存储或外部推送账号。

## 运行服务端骨架

安装 Go 1.26 或兼容的更新版本。在仓库根目录运行：

```powershell
cd server
go run ./cmd/clipd
```

另开终端：

```powershell
Invoke-RestMethod http://127.0.0.1:8080/healthz
Invoke-RestMethod http://127.0.0.1:8080/api/v1/server-info
```

默认仅监听 `127.0.0.1:8080`。`/readyz` 返回 503，表示业务尚未就绪；健康检查成功不表示同步服务已完成。该启动方式不需要数据库。运行 `go test ./...`、`go vet ./...`、`go build ./...` 可验证骨架。

## 项目结构

```text
docs/                       中文需求、设计、开发与运维文档
contracts/                  OpenAPI、密码学互通向量及协议说明
server/cmd/clipd/            Go 服务启动入口
server/internal/            配置、HTTP 和领域接口
server/migrations/          PostgreSQL 初始模型草案
clients/windows/            WPF 工程及平台接口
clients/android/            Kotlin Android 工程及平台接口
deploy/                     Compose / Caddy 自托管模板
scripts/                    静态校验工具
```

客户端构建前提与命令见各自 README；部署模板的当前能力见 [部署文档](docs/08-self-hosting.md)。尚无正式安装包、发布镜像或生产可用版本。
