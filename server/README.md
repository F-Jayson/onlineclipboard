# Go 服务端骨架

从本目录执行 `go run ./cmd/clipd`，默认 `127.0.0.1:8080`。唯一当前生效的环境变量是 `CLIP_HTTP_ADDR`，例如容器中设 `0.0.0.0:8080`。

- `/healthz`：200，进程存在。
- `/readyz`：503，业务未实现。
- `/api/v1/server-info`：200，明确返回 skeleton 和不可用能力。
- 其余 `/api/v1/*`：501，不读取或保存请求正文，不进行任何剪贴板同步。

验证命令：

```powershell
go fmt ./...
go vet ./...
go test ./...
go build ./...
```

当前没有业务测试；`go test` 主要确认包可构建。业务模块、驱动、迁移工具、认证与 WebSocket 依赖在后续里程碑加入并提交 go.sum。`migrations/000001_initial.sql` 是待集成模型，不由当前进程自动执行。

若受限工具会话提示找不到 GOCACHE/LocalAppData，可在当前 PowerShell 会话显式指定项目缓存后重试：

```powershell
$env:GOCACHE = Join-Path (Resolve-Path ..) 'artifacts/go-cache'
```

正常安装环境无需此配置；不修改系统全局环境变量。

`clipd -healthcheck` 仅检查容器内固定 `127.0.0.1:8080/healthz`，供附带 Dockerfile 使用。开发者修改容器监听端口时也要同步修改健康检查；业务发布后另设就绪检查。
