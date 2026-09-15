# Windows 客户端骨架

环境：Windows 11、.NET 10 SDK（仅安装 runtime 不够）。入口为 WPF 项目，无需 `.sln`。

在本目录执行：

```powershell
dotnet build ./OnlineClipboard.Windows/OnlineClipboard.Windows.csproj
dotnet run --project ./OnlineClipboard.Windows/OnlineClipboard.Windows.csproj
```

当前只展示骨架说明页；Core 提供剪贴板、同步、保险库接口。没有系统剪贴板读写、联网、凭据存储、托盘或实际加密实现。

后续先实现账号/密钥解锁与平台适配，再接同步状态机；详细步骤见 [客户端设计](../../docs/07-clients.md) 和 [开发计划](../../docs/09-development-plan.md)。正式安装包和签名需要在发布阶段完成。
