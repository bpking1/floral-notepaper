use super::notes::{default_config_dir, AppError};
use crate::json_io::write_json_atomic;
use keyring::{Entry, Error as KeyringError};
use reqwest::{
    blocking::{Client, Response},
    header::CONTENT_TYPE,
    redirect::Policy,
    StatusCode, Url,
};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use sha2::{Digest, Sha256};
use std::{fs, io::Read, time::Duration};

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct RemoteConfig {
    pub enabled: bool,
    pub base_url: String,
}

fn error(code: &str, message: impl Into<String>) -> AppError {
    AppError {
        code: code.into(),
        message: message.into(),
        details: Default::default(),
    }
}

fn normalize_url(value: &str) -> Result<String, AppError> {
    let mut url =
        Url::parse(value.trim()).map_err(|_| error("remoteConfig", "请输入有效的 API 地址。"))?;
    if !matches!(url.scheme(), "http" | "https")
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(error(
            "remoteConfig",
            "API 地址应为 HTTP/HTTPS 地址，不包含密码、查询参数或片段。",
        ));
    }
    url.set_path(&format!("{}/", url.path().trim_end_matches('/')));
    Ok(url.to_string())
}

fn credential(base_url: &str) -> Result<Entry, AppError> {
    let account = format!("notes-api-{:x}", Sha256::digest(base_url.as_bytes()));
    Entry::new("floral-notepaper", &account).map_err(|e| error("secureStore", e.to_string()))
}

#[tauri::command]
pub fn remote_config_get() -> Result<RemoteConfig, AppError> {
    match fs::read(default_config_dir()?.join("remote.json")) {
        Ok(data) => Ok(serde_json::from_slice(&data)?),
        Err(e) if e.kind() == std::io::ErrorKind::NotFound => Ok(RemoteConfig::default()),
        Err(e) => Err(e.into()),
    }
}

#[tauri::command]
pub fn remote_config_save(
    mut config: RemoteConfig,
    token: Option<String>,
) -> Result<RemoteConfig, AppError> {
    if !config.base_url.trim().is_empty() || config.enabled {
        config.base_url = normalize_url(&config.base_url)?;
    } else {
        config.base_url.clear();
    }
    if let Some(token) = token.filter(|t| !t.trim().is_empty()) {
        if config.base_url.is_empty() {
            return Err(error("remoteConfig", "请先填写 API 地址。"));
        }
        if token.trim().len() < 6 {
            return Err(error("remoteConfig", "Token 至少需要 6 个字符。"));
        }
        credential(&config.base_url)?
            .set_password(token.trim())
            .map_err(|e| error("secureStore", format!("无法保存到系统凭据库：{e}")))?;
    }
    write_json_atomic(&default_config_dir()?.join("remote.json"), &config)?;
    Ok(config)
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum RemoteAction {
    List,
    Read,
    Write,
    Config,
    /// Moves the file to the server's .trash/.
    Delete,
}

#[tauri::command]
pub async fn remote_request(
    base_url: String,
    action: RemoteAction,
    path: Option<String>,
    content: Option<String>,
    revision: Option<String>,
) -> Result<Value, AppError> {
    // Bind every operation to the connection displayed when the document was opened.
    let base_url = bound_connection(&base_url)?;
    tauri::async_runtime::spawn_blocking(move || {
        request(&base_url, action, path, content, revision)
    })
    .await
    .map_err(|e| error("remoteTask", e.to_string()))?
}

fn enabled_base_url() -> Result<String, AppError> {
    let config = remote_config_get()?;
    if !config.enabled || config.base_url.is_empty() {
        return Err(error("remoteDisabled", "请先在设置中启用远程笔记库。"));
    }
    Ok(config.base_url)
}

/// Appends a capture to the server inbox, or with `path` creates or extends
/// that note. `id` makes retries after a lost response idempotent on the server.
#[tauri::command]
pub async fn remote_append(
    id: String,
    title: String,
    text: String,
    path: Option<String>,
) -> Result<Value, AppError> {
    let base_url = enabled_base_url()?;
    tauri::async_runtime::spawn_blocking(move || {
        append(&base_url, &id, &title, &text, path.as_deref())
    })
    .await
    .map_err(|e| error("remoteTask", e.to_string()))?
}

fn append(
    base_url: &str,
    id: &str,
    title: &str,
    text: &str,
    path: Option<&str>,
) -> Result<Value, AppError> {
    let (client, token, base) = connection(base_url)?;
    let url = base
        .join("v1/append")
        .map_err(|e| error("remoteConfig", e.to_string()))?;
    let body =
        serde_json::json!({ "id": id, "title": title, "text": text, "path": path.unwrap_or("") });
    let response = client
        .post(url)
        .header(CONTENT_TYPE, "application/json; charset=utf-8")
        .body(body.to_string())
        .bearer_auth(token)
        .send()
        .map_err(|_| {
            error(
                "remoteNetwork",
                "无法连接服务器或请求超时，便笺已保留在本地。",
            )
        })?;
    let status = response.status();
    let body = bounded_body(response, 64 * 1024)?;
    json_response(status, &body)
}

const MAX_IMAGE_BYTES: usize = 20 * 1024 * 1024;

#[derive(Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RemoteImageUpload {
    path: String,
    markdown_path: String,
}

/// Which file the uploaded image is linked from.
enum ImageTarget {
    Note(String),
    /// The inbox file the next append writes to; the server resolves it.
    Inbox,
}

/// Uploads a pasted or dropped image for a remote note. Raw IPC keeps the
/// bytes out of JSON number arrays; metadata is URI-encoded in headers so
/// Unicode paths work on Windows.
#[tauri::command]
pub async fn remote_image_upload(
    request: tauri::ipc::Request<'_>,
) -> Result<RemoteImageUpload, AppError> {
    let tauri::ipc::InvokeBody::Raw(data) = request.body() else {
        return Err(error("invalidPayload", "图片上传需要二进制数据。"));
    };
    let header = |name: &str| {
        request
            .headers()
            .get(name)
            .and_then(|value| value.to_str().ok())
            .map(decode_header)
            .transpose()
    };
    let note =
        header("x-note-path")?.ok_or_else(|| error("invalidPayload", "缺少远程笔记路径。"))?;
    let name = header("x-image-name")?.unwrap_or_default();
    let mime = image_mime(data)?;
    let base_url = enabled_base_url()?;
    let data = data.clone();
    tauri::async_runtime::spawn_blocking(move || {
        upload_image(&base_url, &ImageTarget::Note(note), &name, data, mime)
    })
    .await
    .map_err(|e| error("remoteTask", e.to_string()))?
}

/// Uploads one of a local note's pasted images (`images/<noteId>/<file>`) so
/// it can be linked from where the note is sent: `note_path`, or the inbox.
#[tauri::command]
pub async fn remote_inbox_image_upload(
    image_path: String,
    note_path: Option<String>,
) -> Result<RemoteImageUpload, AppError> {
    let relative = local_image_path(&image_path)
        .ok_or_else(|| error("invalidPath", format!("无效的本地图片路径：{image_path}")))?;
    let base_url = enabled_base_url()?;
    let file = crate::services::notes::default_store()?
        .data_dir()
        .join(relative);
    tauri::async_runtime::spawn_blocking(move || {
        if fs::metadata(&file)?.len() > MAX_IMAGE_BYTES as u64 {
            return Err(error("tooLarge", "图片大小不能超过 20 MiB。"));
        }
        let data = fs::read(&file)?;
        let mime = image_mime(&data)?;
        let target = note_path.map_or(ImageTarget::Inbox, ImageTarget::Note);
        upload_image(&base_url, &target, "", data, mime)
    })
    .await
    .map_err(|e| error("remoteTask", e.to_string()))?
}

fn upload_image(
    base_url: &str,
    target: &ImageTarget,
    name: &str,
    data: Vec<u8>,
    mime: &str,
) -> Result<RemoteImageUpload, AppError> {
    let (client, token, base) = connection(base_url)?;
    let mut url = base
        .join("v1/images")
        .map_err(|e| error("remoteConfig", e.to_string()))?;
    {
        let mut query = url.query_pairs_mut();
        match target {
            ImageTarget::Note(note) => query.append_pair("note", note),
            ImageTarget::Inbox => query.append_key_only("inbox"),
        };
        if !name.is_empty() {
            query.append_pair("name", name);
        }
    }
    let response = client
        .post(url)
        .bearer_auth(token)
        .header(CONTENT_TYPE, mime)
        .body(data)
        .send()
        .map_err(|_| error("remoteNetwork", "图片上传失败或请求超时，请重试。"))?;
    let status = response.status();
    let body = bounded_body(response, 16 * 1024)?;
    let result: RemoteImageUpload = serde_json::from_value(json_response(status, &body)?)
        .map_err(|_| error("remoteProtocol", "服务器返回了无效的图片路径。"))?;
    if !safe_markdown_link(&result.markdown_path) {
        return Err(error("remoteProtocol", "服务器返回了无效的图片路径。"));
    }
    Ok(result)
}

/// The link is inserted into Markdown as-is: keep it a plain relative path.
fn safe_markdown_link(link: &str) -> bool {
    !link.is_empty()
        && !link.starts_with('/')
        && !link.contains(':')
        && !link
            .chars()
            .any(|c| c.is_whitespace() || c.is_control() || "()<>[]\\".contains(c))
}

/// Maps `images/<noteId>/<file>` to a data-dir relative path, rejecting anything else.
fn local_image_path(path: &str) -> Option<std::path::PathBuf> {
    let parts: Vec<&str> = path.split('/').collect();
    let valid = parts.len() == 3
        && parts[0] == "images"
        && parts[1..]
            .iter()
            .all(|part| !matches!(*part, "" | "." | "..") && !part.contains(['\\', ':']));
    valid.then(|| parts.iter().collect())
}

fn image_mime(data: &[u8]) -> Result<&'static str, AppError> {
    if data.len() > MAX_IMAGE_BYTES {
        return Err(error("tooLarge", "图片大小不能超过 20 MiB。"));
    }
    if data.starts_with(b"\x89PNG\r\n\x1a\n") {
        Ok("image/png")
    } else if data.starts_with(b"\xff\xd8\xff") {
        Ok("image/jpeg")
    } else if data.starts_with(b"GIF87a") || data.starts_with(b"GIF89a") {
        Ok("image/gif")
    } else if data.starts_with(b"RIFF") && data.get(8..12) == Some(b"WEBP") {
        Ok("image/webp")
    } else if data.starts_with(b"BM") {
        Ok("image/bmp")
    } else {
        Err(error(
            "unsupportedImage",
            "仅支持 PNG、JPEG、GIF、WebP 和 BMP 图片。",
        ))
    }
}

fn decode_header(value: &str) -> Result<String, AppError> {
    let invalid = || error("invalidPayload", "图片请求头编码无效。");
    let mut decoded = Vec::with_capacity(value.len());
    let mut bytes = value.bytes();
    while let Some(byte) = bytes.next() {
        if byte == b'%' {
            let hi = bytes.next().and_then(|b| (b as char).to_digit(16));
            let lo = bytes.next().and_then(|b| (b as char).to_digit(16));
            decoded.push(((hi.ok_or_else(invalid)? << 4) | lo.ok_or_else(invalid)?) as u8);
        } else if byte.is_ascii() {
            decoded.push(byte);
        } else {
            return Err(invalid());
        }
    }
    String::from_utf8(decoded).map_err(|_| invalid())
}

fn bound_connection(base_url: &str) -> Result<String, AppError> {
    let base_url = normalize_url(base_url)?;
    if base_url != remote_config_get()?.base_url {
        return Err(error(
            "connectionChanged",
            "连接设置已变化。请先复制未保存内容，再重新打开远程笔记库。",
        ));
    }
    Ok(base_url)
}

fn connection(base_url: &str) -> Result<(Client, String, Url), AppError> {
    let token = match credential(base_url)?.get_password() {
        Ok(token) => token,
        Err(KeyringError::NoEntry) => {
            return Err(error("missingToken", "请在设置中填写 API Token。"))
        }
        Err(e) => return Err(error("secureStore", e.to_string())),
    };
    let client = Client::builder()
        .connect_timeout(Duration::from_secs(5))
        .timeout(Duration::from_secs(20))
        .redirect(Policy::none())
        .build()
        .map_err(|e| error("remoteNetwork", e.to_string()))?;
    let base = Url::parse(base_url).map_err(|e| error("remoteConfig", e.to_string()))?;
    Ok((client, token, base))
}

fn request(
    base_url: &str,
    action: RemoteAction,
    path: Option<String>,
    content: Option<String>,
    revision: Option<String>,
) -> Result<Value, AppError> {
    let (client, token, base) = connection(base_url)?;
    let route = match action {
        RemoteAction::List => "v1/files",
        RemoteAction::Config => "v1/config",
        RemoteAction::Read | RemoteAction::Write | RemoteAction::Delete => "v1/file",
    };
    let mut url = base
        .join(route)
        .map_err(|e| error("remoteConfig", e.to_string()))?;
    if let Some(path) = path {
        url.query_pairs_mut().append_pair("path", &path);
    }
    let builder = match action {
        RemoteAction::Write => {
            let content = content.ok_or_else(|| error("remoteRequest", "缺少笔记内容。"))?;
            if content.len() > 2 * 1024 * 1024 {
                return Err(error("tooLarge", "内容超过 2 MiB。"));
            }
            let builder = client
                .put(url)
                .header("Content-Type", "text/markdown; charset=utf-8")
                .body(content);
            if let Some(revision) = revision {
                builder.header("If-Match", revision)
            } else {
                builder.header("If-None-Match", "*")
            }
        }
        RemoteAction::Delete => {
            let builder = client.delete(url);
            match revision {
                Some(revision) => builder.header("If-Match", revision),
                None => builder,
            }
        }
        _ => client.get(url),
    };
    let response = builder.bearer_auth(token).send().map_err(|_| {
        error(
            "remoteNetwork",
            "无法连接服务器或请求超时。草稿仍保留；若保存响应丢失，可重试保存。",
        )
    })?;
    let status = response.status();
    let body = bounded_body(response, 16 * 1024 * 1024)?;
    json_response(status, &body)
}

fn bounded_body(response: Response, limit: usize) -> Result<Vec<u8>, AppError> {
    if response
        .content_length()
        .is_some_and(|len| len > limit as u64)
    {
        return Err(error("tooLarge", "服务器响应过大。"));
    }
    let mut body = Vec::new();
    response
        .take(limit as u64 + 1)
        .read_to_end(&mut body)
        .map_err(|_| error("remoteNetwork", "服务器响应中断，请重试。"))?;
    if body.len() > limit {
        return Err(error("tooLarge", "服务器响应过大。"));
    }
    Ok(body)
}

fn json_response(status: StatusCode, body: &[u8]) -> Result<Value, AppError> {
    let value: Value = serde_json::from_slice(body).map_err(|_| {
        error(
            "remoteProtocol",
            format!(
                "服务器返回格式异常（HTTP {}），请检查 API 地址。",
                status.as_u16()
            ),
        )
    })?;
    if !status.is_success() {
        let code = value["code"].as_str().unwrap_or("remoteHttp");
        let message = match code {
            "conflict" => "服务器上的文件已被其他编辑器修改。",
            "notFound" => "文件或父目录不存在。可以新建文件，但请先在服务器创建目录。",
            "unauthorized" => "API Token 不正确，请在设置中更新。",
            "permission" => "服务没有读写笔记目录的权限。",
            _ => value["message"].as_str().unwrap_or("远程操作失败。"),
        };
        return Err(error(code, message));
    }
    Ok(value)
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::{io::Write, net::TcpListener, thread};

    fn http_response(headers: &str, body: &[u8]) -> Response {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap();
        let response = [headers.as_bytes(), b"\r\nConnection: close\r\n\r\n", body].concat();
        thread::spawn(move || {
            let (mut stream, _) = listener.accept().unwrap();
            let mut request = [0; 4096];
            let _ = stream.read(&mut request);
            let _ = stream.write_all(&response);
        });
        Client::builder()
            .no_proxy()
            .timeout(Duration::from_secs(2))
            .build()
            .unwrap()
            .get(format!("http://{address}"))
            .send()
            .unwrap()
    }

    #[test]
    fn validates_and_normalizes_api_urls() {
        assert_eq!(
            normalize_url("https://notes.example/api").unwrap(),
            "https://notes.example/api/"
        );
        for value in [
            "file:///tmp",
            "https://user:pass@host",
            "https://host/?token=secret",
            "https://host/#x",
        ] {
            assert!(normalize_url(value).is_err());
        }
    }

    #[test]
    fn image_headers_decode_unicode_and_literal_symbols() {
        assert_eq!(decode_header("a+b.md").unwrap(), "a+b.md");
        assert_eq!(
            decode_header("%E7%AC%94%E8%AE%B0%2Fa%2Bb%20%25.md").unwrap(),
            "笔记/a+b %.md"
        );
        for value in ["%", "%2", "%GG", "%FF", "笔记.md"] {
            assert!(decode_header(value).is_err(), "{value}");
        }
    }

    #[test]
    fn only_bounded_raster_payloads_are_accepted() {
        for (data, mime) in [
            (b"\x89PNG\r\n\x1a\n".as_slice(), "image/png"),
            (b"\xff\xd8\xff", "image/jpeg"),
            (b"GIF89a", "image/gif"),
            (b"RIFF1234WEBP", "image/webp"),
            (b"BM", "image/bmp"),
        ] {
            assert_eq!(image_mime(data).unwrap(), mime);
        }
        for data in [b"<svg></svg>".as_slice(), b"RIFF1234WAVE", b""] {
            assert!(image_mime(data).is_err());
        }
        assert_eq!(
            image_mime(&vec![0; MAX_IMAGE_BYTES + 1]).unwrap_err().code,
            "tooLarge"
        );
    }

    #[test]
    fn local_image_paths_stay_in_the_images_directory() {
        assert_eq!(
            local_image_path("images/abc/1.png"),
            Some(["images", "abc", "1.png"].iter().collect())
        );
        for path in [
            "images/abc",
            "images/../x.png",
            "images/abc/../../x.png",
            "images//x.png",
            "notes/abc/1.png",
            "images/abc/C:x.png",
            "images/abc\\..\\x/1.png",
            "/images/abc/1.png",
        ] {
            assert!(local_image_path(path).is_none(), "{path}");
        }
    }

    #[test]
    fn markdown_links_from_the_server_are_plain_relative_paths() {
        for link in ["../attachments/a.png", "attachments/架构-图.png"] {
            assert!(safe_markdown_link(link), "{link}");
        }
        for link in [
            "",
            "/etc/a.png",
            "javascript:alert(1)",
            "https://x/a.png",
            "a b.png",
            "a).png",
            "a\nb.png",
        ] {
            assert!(!safe_markdown_link(link), "{link}");
        }
    }

    #[test]
    fn bounded_responses_reject_declared_and_streamed_excess() {
        for headers in ["HTTP/1.1 200 OK", "HTTP/1.1 200 OK\r\nContent-Length: 5"] {
            let response = http_response(headers, b"12345");
            assert_eq!(bounded_body(response, 4).unwrap_err().code, "tooLarge");
        }
    }
}
