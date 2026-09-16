# Markdown Capture API

一个只做一件事的服务：接收 Markdown，并按日期和时间追加到服务器上的同一个文件。

客户端不能指定文件路径、日期或时间。目标文件和时区全部由服务端配置。

## 写入格式

假设目标文件是 `Inbox.md`，同一天发送两次后，文件内容类似：

```markdown
## 2026-08-31

<!-- floral-capture-date:v1 2026-08-31 -->

### 21:35:42

今天想到的一段 **Markdown**。

<!-- floral-capture-entry:v1 id=550e8400-e29b-41d4-a716-446655440000 sha256=... at=2026-08-31T21:35:42+08:00 -->

### 22:10:03

- [ ] 明天处理这件事

<!-- floral-capture-entry:v1 id=0f832de1-8f5f-4abc-9540-4ea0c22987b9 sha256=... at=2026-08-31T22:10:03+08:00 -->
```

HTML 注释用于跨重启幂等和可靠识别日期，在 Markdown 渲染结果中不可见。正文不会被转义，标题、列表、任务、代码块等 Markdown 均会原样保存；仅在末尾补足用于分隔下一条记录的空行。

到第二天时，服务会先追加新的 `## YYYY-MM-DD`，随后再写时间和正文。

## API

### `POST /v1/capture`

推荐直接发送 Markdown：

```bash
curl -X POST http://127.0.0.1:8787/v1/capture \
  -H "Authorization: Bearer $CAPTURE_TOKEN" \
  -H "Content-Type: text/markdown; charset=utf-8" \
  -H "Idempotency-Key: 550e8400-e29b-41d4-a716-446655440000" \
  --data-binary '今天想到的一段 **Markdown**。'
```

也支持 JSON，方便某些手机客户端或快捷指令调用：

```bash
curl -X POST http://127.0.0.1:8787/v1/capture \
  -H "Authorization: Bearer $CAPTURE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"id":"550e8400-e29b-41d4-a716-446655440000","content":"- [ ] 一条任务"}'
```

首次写入返回 `201 Created`：

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "createdAt": "2026-08-31T21:35:42+08:00",
  "date": "2026-08-31",
  "time": "21:35:42",
  "duplicate": false
}
```

相同 `Idempotency-Key` 和相同正文重试时不重复写入，返回 `200 OK` 且 `duplicate` 为 `true`。相同 key 携带不同正文返回 `409 Conflict`。

未提供 key 时服务会生成一个，但客户端在响应丢失后无法可靠去重，因此正式客户端应始终生成并保存 key。

正文 Content-Type 可以是：

- `text/markdown`
- `text/plain`
- `application/json`

常见错误状态：

| 状态码 | 含义 |
| --- | --- |
| `400` | 请求、编码或幂等 key 无效 |
| `401` | Bearer Token 缺失或错误 |
| `409` | 同一个幂等 key 被用于不同正文 |
| `413` | 正文超过配置的大小限制 |
| `415` | Content-Type 不受支持 |
| `422` | 正文为空或包含服务保留标记 |
| `503` | 文件锁超时，可稍后安全重试 |

### `GET /healthz`

匿名存活检查，只返回：

```json
{"status":"ok"}
```

## 直接运行

需要 Go 1.24 或更新版本：

```bash
cd capture-server

export CAPTURE_FILE=/srv/markdown/Inbox.md
export CAPTURE_TIMEZONE=Asia/Shanghai
export CAPTURE_TOKEN=请替换为至少32个字符的随机令牌

go run .
```

默认仅监听 `127.0.0.1:8787`。

## Docker Compose

先复制示例并生成 Token：

```bash
cd capture-server
cp compose.example.yaml compose.yaml
mkdir -p secrets
openssl rand -hex 32 > secrets/capture_token
chmod 600 secrets/capture_token
```

然后修改 `compose.yaml` 中的：

- `/srv/markdown`：服务器上 Markdown 文件夹的真实路径。
- `CAPTURE_FILE`：容器内固定目标文件，默认 `/notes/Inbox.md`。
- `CAPTURE_TIMEZONE`：例如 `Asia/Shanghai`、`Asia/Hong_Kong` 或 `UTC`。
- `user`：改成拥有 Markdown 目录写权限的宿主机 `UID:GID`。

启动：

```bash
docker compose up -d --build
```

示例只把端口发布到服务器的 `127.0.0.1`。请通过 Caddy、Nginx 等反向代理提供 HTTPS，或者仅通过可信 VPN 访问；不要把未加密的 HTTP 端口直接暴露到公网。

## 配置

| 环境变量 | 必填 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `CAPTURE_FILE` | 是 | — | 固定目标文件的绝对路径，只接受 `.md`/`.markdown` |
| `CAPTURE_TIMEZONE` | 是 | — | IANA 时区，日期和时间以此为准 |
| `CAPTURE_TOKEN` | 二选一 | — | 至少 32 个字符的 Bearer Token |
| `CAPTURE_TOKEN_FILE` | 二选一 | — | 从 Docker Secret 等文件读取 Token |
| `CAPTURE_BIND` | 否 | `127.0.0.1:8787` | 监听地址 |
| `CAPTURE_MAX_BODY_BYTES` | 否 | `262144` | 请求体上限，允许范围 1 字节至 10 MiB |
| `CAPTURE_LOCK_TIMEOUT` | 否 | `3s` | 等待文件锁的最长时间 |

服务不会自动创建目标文件的父目录。部署者必须先准备目录并设置正确权限；目标文件不存在时会以 `0600` 权限创建。

## 并发和编辑约束

- 同一个服务进程内的请求会串行写入。
- Linux/macOS 上还会使用与目标文件同目录的 `Inbox.md.lock`，防止误启动的第二个服务进程交叉写入。
- 每个追加块在持有锁期间完整写入，并在成功响应前执行 `fsync`。
- Docker 服务应保持单副本运行，目标目录应位于服务器本地文件系统。

Files.md、SilverBullet 或其他编辑器通常不会遵守该锁。如果编辑器长时间打开并修改同一个 `Inbox.md`，它可能用旧内容覆盖 API 刚追加的数据。因此建议把目标文件视为只追加的收件箱：API 负责写入，其他工具主要读取，并在整理前先刷新到最新版本。

## 安全边界

- 客户端不能传文件路径，API 只写入 `CAPTURE_FILE`。
- Token 使用常量时间比较，日志不记录 Token 或正文。
- 目标文件拒绝符号链接。
- Docker 默认非 root、根文件系统只读，并移除 Linux capabilities。
- 服务自身不终止 TLS，公网部署必须放在 HTTPS 反向代理后。

本服务沿用项目根目录的 MIT License。
