# 协议契约

- [openapi.yaml](openapi.yaml)：22 个 HTTP 操作的契约。`x-implementation: implemented` 表示服务端已提供对应路由。
- [crypto-v1-vectors.json](crypto-v1-vectors.json)：用于后续 Windows/Android 独立实现互通的固定参考输入和输出。
- [加密规范](../docs/03-security.md)、[同步规范](../docs/04-sync-protocol.md) 约束跨字段语义；不能只按 JSON 字段名实现。

## 向量使用

向量中的密钥、恢复码、nonce、UUID 和测试正文均是**公开的固定测试值**，不能用于任何真实账号。固定 nonce 仅用于测试确定性；生产中必须 CSPRNG 生成，重试复用原信封而非重新加密。

向量由 Python cryptography 标准实现计算，提供 CMK 包装、条目 HKDF 输出、AAD、ciphertext+tag、UTF-8 字节；空白、CRLF、中文和 emoji 都保留。它验证文档所定义字节布局，不代表密码学审计或已完成跨语言互通。

后续两端必须各自用实现生成相同输出，并执行：篡改密文、tag、nonce、账号 ID、保险库 ID、来源设备 ID全部失败；格式版本或 key_epoch 不支持时明确拒绝；不得放宽 AAD 校验以匹配错误样例。

## 本地静态校验

从仓库根目录执行（依赖 Python 3、PyYAML 和 cryptography；不是服务端运行依赖）：

```powershell
python -X utf8 scripts/validate_contracts.py
```

脚本检查文档链接、UTF-8、JSON/YAML/XML、OpenAPI 内部引用与必填路径参数，以及固定密码学向量的输出/篡改拒绝。不代替完整 OpenAPI 标准校验、SQL 执行、客户端编译或系统集成测试。
