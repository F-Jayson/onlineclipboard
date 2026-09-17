# Go 服务端

需要 PostgreSQL。从本目录执行：

```powershell
$env:CLIP_DATABASE_URL = "postgres://USER:PASS@127.0.0.1:5432/onlineclipboard?sslmode=disable"
$env:CLIP_HTTP_ADDR = "127.0.0.1:8080"
$env:CLIP_REGISTRATION_MODE = "invite"
go run ./cmd/clipd
```

- `/healthz`：进程存活。
- `/readyz`：数据库可连接时 200。
- `/api/v1/server-info`：`sync_available` / `e2ee_available` 为 true，`stage=ready`。
- 业务接口：注册（邀请/开放/邮箱）、外部账号登录、刷新、保险库与密码包装信封、密文 CRUD、增量/快照、设备、WebSocket 提示。

管理命令：

```powershell
go run ./cmd/clipd migrate
go run ./cmd/clipd admin invite
go run ./cmd/clipd admin reset-password <username>
go run ./cmd/clipd admin rotate-sync-epoch
```

验证：

```powershell
$env:CLIP_TEST_DATABASE_URL = $env:CLIP_DATABASE_URL
go test ./...
go vet ./...
go build ./...
```

`clipd -healthcheck` 探测容器内 `127.0.0.1:8080/healthz`。
