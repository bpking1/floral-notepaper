# 远程 Markdown 笔记 API

配合花笺桌面端，把便笺发送到服务器笔记库的 Inbox 文件（替代 memos 式快速记录），之后在 nvim 中整理。服务直接读写 Markdown 文件，不需要数据库。

## 部署（Linux）

需要 Go 1.26，或使用下面的 Docker Compose。

```bash
cd notes-server
export NOTES_ROOT=/srv/markdown
export NOTES_TOKEN="$(openssl rand -hex 32)"
# 请妥善保存此 Token，桌面端连接时需要填写。
go run .
```

默认监听 `127.0.0.1:8789`。`NOTES_ROOT` 必须已存在，服务用户需要读写笔记目录的权限。

Docker Compose：

```bash
cd notes-server
cp compose.example.yaml compose.yaml
mkdir -p secrets
openssl rand -hex 32 > secrets/notes_token
chmod 600 secrets/notes_token
# 修改 compose.yaml 的 /srv/markdown 和 user（笔记目录所有者 UID:GID）。
# 确保容器中的该用户也有权读取 secrets/notes_token。
docker compose up -d --build
```

通过现有反向代理提供 HTTPS，例如 Caddy：

```caddyfile
notes.example.com {
    reverse_proxy 127.0.0.1:8789
}
```

私有网络可设置 `NOTES_BIND` 为对应监听地址。HTTP 会明文传输 Token 和笔记，仅用于本机或可信私有网络；公网使用 HTTPS。容器默认只发布到服务器回环地址。

| 配置                  | 说明                                                              |
| --------------------- | ----------------------------------------------------------------- |
| `NOTES_ROOT`          | 现有 Markdown 笔记库目录，必填                                    |
| `NOTES_TOKEN`         | 至少 6 字符的访问令牌                                             |
| `NOTES_TOKEN_FILE`    | 从文件读取令牌，与 `NOTES_TOKEN` 二选一                           |
| `NOTES_BIND`          | 默认 `127.0.0.1:8789`                                             |
| `NOTES_INBOX`         | 追加目标，默认 `Chat.md`                                          |
| `NOTES_NEW_DIR`       | 有标题的便笺默认放到的目录，如 `notes`；默认空，即笔记库根目录    |
| `NOTES_ATTACHMENTS`   | 图片目录，默认 `media`                                            |
| `NOTES_PUBLIC_IMAGES` | `true` 时读取图片无需 Token，磁贴才能显示远程图片；仅限可信局域网 |
| `TZ`                  | 追加记录使用的时区，如 `Asia/Shanghai`                            |

## 桌面端使用

1. 主窗口 → 应用设置 → **远程 Markdown 笔记库**。
2. 填写 API 地址与 Token，勾选“启用远程笔记库”，点击“保存并测试连接”。
3. 按快捷键（默认 `Ctrl+Space`）打开的仍是普通本地便笺：自动保存、钉成磁贴都照旧。
4. 写完按 `Ctrl+Enter` 或点击“发送”：先保存到本地，再追加到服务器 Inbox；成功后本地便笺移入「已发送」分类并关闭窗口。发送失败时便笺原样保留，之后可再次发送。
   - 不填标题：追加到 Inbox（`Chat.md`）。
   - 填了标题：发送到以标题命名的笔记。标题相对于服务端 `NOTES_NEW_DIR`，以 `/` 开头则从笔记库根目录算起：设 `NOTES_NEW_DIR=notes` 时，`读书笔记` → `notes/读书笔记.md`，`/diary/today` → `diary/today.md`。文件不存在就新建，缺少的目录自动创建；已存在则空一行后追加到末尾。标题下方会显示目标路径，以及是新建还是追加。
   - 标题以 `/` 开头时会列出远程目录和笔记供选择：↑↓ 选择，Tab 填入，Esc 收起。标题不能包含 `\ : * ? " < > |`，也不能指向 `.` 开头的隐藏目录。
5. 编辑服务器上已有的文件：便笺“打开”页切到“远程”，列表按修改时间倒序；搜索框按路径匹配，空格分隔多个关键词（如 `日记 09`），回车打开第一项。打开后照常编辑、自动保存，也可钉成磁贴。标题即文件名，不可修改。
   - 悬停条目时的图标或右键菜单“在新窗口中打开”：在单独的便笺窗口编辑，当前便笺不受影响。
   - 右键“删除”（再点一次确认）：文件移到服务器笔记库的 `.trash/` 目录（保留原目录结构，同名时加时间后缀），需要时可在 nvim 中移回。

远程文件的保存带版本校验：若文件已在 nvim 等其他地方被修改，不会覆盖，而是把你的修改另存为本地便笺「xxx（冲突副本）」并载入服务器最新版本。窗口重新获得焦点时，没有未保存修改的远程文件会自动刷新。粘贴或拖入图片会上传到服务器并插入链接；暂不支持在便笺中新建远程文件。

API 地址可以带反向代理前缀，例如 `https://example.com/notes/`；代理需移除该前缀后转发。Token 按 API 地址保存在操作系统凭据库，JSON 配置不保存 Token。便笺中粘贴的图片会在发送时一并上传。

## Inbox 格式

条目按 Files.md 的 `Chat.md` 格式追加到 `NOTES_INBOX`（默认 `Chat.md`，也支持 `{YYYY}`、`{MM}`、`{DD}`，如 `inbox/{YYYY}-{MM}.md`；目录不存在时自动创建）。时间按服务端 `TZ` 计算：

```text
#### 29 September, Tuesday
- [ ] `14:32` 第一行（有标题时为标题）
其余行原样跟在后面
- [ ] `16:05` 另一条
```

- 日期标题为英文日、月、星期（不含年份），与文件中最后一个 `####` 标题不同时才新增，新日期前空一行。
- 同一天的条目直接相连；上一条是多行内容时先空一行，避免新条目并入上一段。
- 可在 nvim 或 Files.md 中随意整理、删除已处理内容，下次追加基于文件当前内容。正文不写入任何元数据。

## 图片

所有图片统一存放在 `NOTES_ATTACHMENTS`（默认 `media/`，与 Files.md 保存图片的目录相同）目录，Markdown 中写相对于所在笔记的链接，例如根目录的 `Chat.md` 里是 `![](media/20260928-143205.png)`，`日记/今天.md` 里是 `![](../media/…)`。在 nvim 中把条目移到其他目录的笔记时，按新位置调整 `../` 层级即可，图片本身不用移动。

- 文件名：拖入的文件保留原名（空格、括号等 Markdown 敏感字符替换为 `-`）；粘贴的截图按上传时间命名，如 `20260928-143205.png`，时间按服务端 `TZ`。重名时加 `-2`、`-3` 后缀，不会覆盖已有文件。
- 去重：上传前按内容比对目录中已有图片，完全相同则复用，因此请求重试或重复粘贴不会产生副本。上传在目录锁内完成，多个请求并发也不会重复。
- 磁贴开启 Markdown 渲染时显示远程图片，需要服务端设置 `NOTES_PUBLIC_IMAGES=true`（只放开 `GET /v1/image`，其余接口仍需 Token）。在磁贴中右键图片即复制到剪贴板（转为 PNG），本地便笺的图片同样适用。
- 可以在 nvim 中随意重命名图片，只需同步修改链接；服务不会自动删除或改名图片。
- 支持 PNG、JPEG、GIF、静态 WebP、BMP，每张最大 20 MiB、4000 万像素；服务根据实际内容判断格式和扩展名，不信任客户端的扩展名或 `Content-Type`。

## 协议

除 `GET /healthz` 外均需要 `Authorization: Bearer <token>`。

| 请求                                            | 响应                                                                                                                                                                                  |
| ----------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET /v1/config`                                | `{ "inbox": "Chat.md", "newDir": "notes" }`，客户端据此显示发送目标                                                                                                                   |
| `POST /v1/append`                               | 正文 `{ "id": "…", "title": "…", "text": "…", "path": "可选，如 notes/read/test.md" }`；无 `path` 追加到 Inbox，有则新建或追加到该笔记，返回 `{ "path": "Chat.md", "revision": "…" }` |
| `GET /v1/files`                                 | `{ "files": [{ "path": "日记/今天.md", "modified": 1790000000 }], "truncated": false }`                                                                                               |
| `GET /v1/file?path=Inbox.md`                    | `{ "content": "…", "revision": "\"sha256…\"" }`，同时返回 `ETag`                                                                                                                      |
| `PUT /v1/file?path=Inbox.md`                    | 正文为原始 UTF-8 Markdown，返回 `{ "revision": "\"sha256…\"" }`                                                                                                                       |
| `DELETE /v1/file?path=日记/今天.md`             | 移到 `.trash/日记/今天.md`，返回 `{ "trashedTo": "…" }`；可带 `If-Match`，文件已变化则 `412`                                                                                          |
| `POST /v1/images?note=日记/今天.md&name=图.png` | 正文为原始图片字节，返回 `{ "path": "media/图.png", "markdownPath": "../media/图.png" }`；`name` 可省略                                                                               |
| `POST /v1/images?inbox`                         | 同上，链接相对于下一次追加写入的 inbox 文件                                                                                                                                           |
| `GET /v1/image?path=media/图.png`               | 原始图片字节，`Content-Type` 根据实际内容返回                                                                                                                                         |

`/v1/files` 最多列出 20000 个文件，超出时只返回最近修改的部分并设 `truncated: true`；客户端带 `Accept-Encoding: gzip` 时压缩返回。追加按 `id` 去重：响应丢失后用同一 `id` 重试不会重复写入（去重记录只在内存中，服务重启后失效）。保存已有文件必须携带读取时返回的 `If-Match: "sha256…"`；新建使用 `If-None-Match: *`。版本冲突返回 `412`，缺少条件头返回 `428`，文件不存在返回 `404`。错误体为 `{ "code": "…", "message": "…" }`。

图片上传无需版本条件头，正文不是 multipart；图片上传与读取均要求同样的 Bearer Token。图片路径使用笔记库内的相对路径，查询参数需 URL 编码。内容无效或格式不支持返回 `415`，超过大小或像素限制返回 `413`，非法路径返回 `400`。上传响应和安全重试均返回 `200`。图片地址不包含 Token，客户端通过已认证请求获取图片后显示。

## 保存与并发边界

服务在笔记库根目录上使用 `flock` 协调 API 写入；内容版本用 SHA-256 表示。保存先写入同目录临时文件，执行 `fsync` 后原子替换，保留原文件权限位；新文件为 `0600`。不会向 Markdown 正文添加元数据，也不会创建索引或数据库。

图片使用同目录临时文件、文件 `fsync`、原子硬链接创建和目录 `fsync`；不会覆盖已有文件，重复内容可安全重试。新图片权限为 `0600`，按需创建的 `images/` 目录为 `0700`。图片解码在每个服务进程中串行执行，限制大图并发解码所需内存。

其他编辑器不遵守 API 锁：保存前会检查版本，并在替换前再次检查，但检查与替换之间仍有极短竞争窗口。nvim / 网页编辑器也可能在之后保存自己的旧副本。因此避免在多个编辑器中同时修改同一文件；切换编辑器前保存并重新读取。服务应使用服务器本地文件系统；文件所有者是运行服务的用户，替换不保留额外 ACL/xattr。

路径访问使用 Go `os.Root` 限制在笔记库内，拒绝路径穿越、符号链接和非普通文件；文本接口仅允许 Markdown 文件，图片接口仅允许上述图片格式。服务不提供跨域访问许可、不记录笔记正文或 Token。目录不可同时用于存放不可信的设备文件或挂载点。

## 验证

```bash
go test -race ./...
go vet ./...
go build -o notes-api .
```

服务沿用仓库根目录的 MIT License。
