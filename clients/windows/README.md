# Windows 客户端

环境：Windows 11、.NET 10 SDK。入口为 WPF 项目。

```powershell
dotnet test ./OnlineClipboard.Windows.Tests/OnlineClipboard.Windows.Tests.csproj
dotnet build ./OnlineClipboard.Windows/OnlineClipboard.Windows.csproj -c Release
dotnet run --project ./OnlineClipboard.Windows/OnlineClipboard.Windows.csproj
```

开发机 `dotnet run` 会使用已安装的 SDK。若直接双击 `bin\Release\net10.0-windows\OnlineClipboard.Windows.exe`，该文件**不包含** .NET 桌面运行时，也必须与同目录的 dll / `runtimeconfig.json` 一起使用，否则会提示 “You must install .NET Desktop Runtime”。

给本机或没有安装 .NET 的电脑使用时，发布自包含单文件（体积较大，无需另装运行时）：

```powershell
dotnet publish ./OnlineClipboard.Windows/OnlineClipboard.Windows.csproj -c Release -p:PublishProfile=WinX64SelfContained -o ../../dist/windows
```

然后运行 `dist\windows\OnlineClipboard.Windows.exe`。未做安装包/签名。

使用：

1. 填写服务器 origin。本机开发可用 `http://127.0.0.1:8080`；非本机必须 HTTPS。
2. 注册或登录。邀请模式需粘贴管理员邀请码。
3. 首次初始化保险库并离线保存 `oc1_` 恢复密钥；新设备用同一密钥解锁。
4. 勾选「采集复制」后，用户会话内复制的纯文字会加密上传；「自动写入」会在补偿完成后把符合条件的 live 条目写回本机剪贴板（带指纹抑制，避免回传循环）。
5. 历史支持本地正文搜索、再次复制（不上传）、删除到回收站；回收站可在 168 小时内恢复或永久删除。

登录密码不能解密历史。关闭窗口即退出（尚无托盘）。
