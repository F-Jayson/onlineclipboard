# 当前交付验证记录

日期：2026-09-15；环境：Windows，工作目录 `C:\Users\Jayson\Desktop\onlineclipboard`。

## 已执行

| 检查 | 结果与范围 |
| --- | --- |
| Go 1.26.5：fmt / vet / test / build | 通过；尚无业务测试，go test 主要确认包构建 |
| 真实 loopback HTTP 冒烟 | `/healthz` 200、`/readyz` 503、`/api/v1/server-info` 200、POST `/api/v1/clips` 501、未知路径 404，响应形状与契约匹配 |
| 冒烟进程清理 | 临时服务已停止，没有留下后台服务 |
| Compose 配置 | 使用本机发现的 Compose v5.3.1 插件，加载 infrastructure/https profiles，以 `config --quiet` 校验通过；没有启动容器 |
| 文档与基础格式 | `python -X utf8 scripts/validate_contracts.py` 通过：UTF-8、所有文档本地链接、YAML/JSON/XML（含 XAML/csproj/Manifest） |
| OpenAPI | 22 个操作的本地引用/路径参数检查通过；另使用 openapi-spec-validator 完成 OpenAPI 3.0 规范校验 |
| SQL | pglast 对初始迁移的 21 条语句语法解析通过；没有在 PostgreSQL 实际执行 |
| 加密参考向量 | 3 组固定公开测试向量的 HKDF、AES-GCM 包装/解包、中文/emoji/空白字节保持，以及 nonce/tag/AAD 篡改拒绝通过 |

OpenAPI/SQL 额外校验库只安装在工作区忽略目录 `artifacts/qa-libs`，没有变更系统 Python 包。校验过程中旧有 requests 与新增 urllib3 产生兼容性警告；本次本地规范/语法校验已成功，不涉及它们的 HTTP 网络请求。固定向量只由 Python 参考实现验证，双端独立互通仍待开发。

## 未执行及环境限制

- Windows 客户端编译/运行：系统有 dotnet 主机，但没有 .NET SDK 目录，`dotnet --list-sdks` 无输出。
- Android 构建/真机：没有已配置的 Android SDK、Gradle/Wrapper 或连接的真机测试环境；只交付工程入口，不声称 APK 构建成功。
- PostgreSQL 迁移执行、Docker 镜像构建和 Compose 运行：本轮未启动实际数据库或容器。
- 业务认证、加密实现、同步、清理、性能和双端互通：尚未实现，不能验收为通过。

初始 Go 检查因本工具会话缺少 LocalAppData/GOCACHE 失败；设置仅用于当前命令的工作区缓存后通过。HTTP 冒烟为子进程补充缺失的 Windows SystemRoot，未改变用户系统配置。Docker 主命令未找到 Compose 插件，随后使用已有插件的绝对路径完成配置校验。
