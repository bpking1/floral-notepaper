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
use std::{
    fs,
    io::Read,
    sync::atomic::{AtomicBool, Ordering},
    time::Duration,
};

pub static REMOTE_DIRTY: AtomicBool = AtomicBool::new(false);
const MAX_IMAGE_BYTES: usize = 20 * 1024 * 1024;

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", default)]
pub struct RemoteConfig {
    pub enabled: bool,
    pub base_url: String,
    pub default_file: String,
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
    if REMOTE_DIRTY.load(Ordering::SeqCst) && remote_config_get()?.base_url != config.base_url {
        return Err(error(
            "unsavedDraft",
            "请先保存或放弃远程草稿，再更换 API 地址。",
        ));
    }
    if let Some(token) = token.filter(|t| !t.trim().is_empty()) {
        if config.base_url.is_empty() {
            return Err(error("remoteConfig", "请先填写 API 地址。"));
        }
        if token.trim().len() < 32 {
            return Err(error("remoteConfig", "Token 至少需要 32 个字符。"));
        }
        credential(&config.base_url)?
            .set_password(token.trim())
            .map_err(|e| error("secureStore", format!("无法保存到系统凭据库：{e}")))?;
    }
    write_json_atomic(&default_config_dir()?.join("remote.json"), &config)?;
    Ok(config)
}

#[tauri::command]
pub fn remote_set_dirty(window: tauri::WebviewWindow, dirty: bool) {
    if window.label() == "notepad-remote" {
        REMOTE_DIRTY.store(dirty, Ordering::SeqCst);
    }
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase")]
pub enum RemoteAction {
    List,
    Read,
    Write,
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
    let route = if matches!(action, RemoteAction::List) {
        "v1/files"
    } else {
        "v1/file"
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
            "conflict" => "服务器文件已被其他编辑器修改。草稿仍保留，请查看服务器版本后合并。",
            "notFound" => "文件或父目录不存在。可以新建文件，但请先在服务器创建目录。",
            "unauthorized" => "API Token 不正确，请在设置中更新。",
            "permission" => "服务没有读写笔记目录的权限。",
            _ => value["message"].as_str().unwrap_or("远程操作失败。"),
        };
        return Err(error(code, message));
    }
    Ok(value)
}

#[derive(Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct RemoteImageUpload {
    path: String,
    markdown_path: String,
}

// Raw IPC keeps image bytes out of JSON number arrays. Metadata is encoded with
// encodeURIComponent in headers so Unicode note paths also work on Windows.
#[tauri::command]
pub async fn remote_image_upload(
    request: tauri::ipc::Request<'_>,
) -> Result<RemoteImageUpload, AppError> {
    let tauri::ipc::InvokeBody::Raw(data) = request.body() else {
        return Err(error("invalidPayload", "图片上传需要二进制数据。"));
    };
    let mime = image_mime(data)?;
    let header = |name: &str| -> Result<String, AppError> {
        let encoded = request
            .headers()
            .get(name)
            .and_then(|value| value.to_str().ok())
            .ok_or_else(|| error("invalidPayload", format!("缺少 {name} 请求头。")))?;
        decode_header(encoded)
    };
    let base_url = bound_connection(&header("x-api-base-url")?)?;
    let note_path = header("x-note-path")?;
    if !valid_note_path(&note_path) {
        return Err(error("invalidPath", "请先选择 Markdown 笔记。"));
    }
    let data = data.clone();
    tauri::async_runtime::spawn_blocking(move || upload_image(&base_url, &note_path, data, mime))
        .await
        .map_err(|e| error("remoteTask", e.to_string()))?
}

#[tauri::command]
pub async fn remote_image_read(
    base_url: String,
    path: String,
) -> Result<tauri::ipc::Response, AppError> {
    let base_url = bound_connection(&base_url)?;
    if !valid_relative_path(&path) {
        return Err(error("invalidPath", "图片路径无效。"));
    }
    let data = tauri::async_runtime::spawn_blocking(move || read_image(&base_url, &path))
        .await
        .map_err(|e| error("remoteTask", e.to_string()))??;
    Ok(tauri::ipc::Response::new(data))
}

fn upload_image(
    base_url: &str,
    note_path: &str,
    data: Vec<u8>,
    mime: &'static str,
) -> Result<RemoteImageUpload, AppError> {
    let (client, token, base) = connection(base_url)?;
    let mut url = base
        .join("v1/images")
        .map_err(|e| error("remoteConfig", e.to_string()))?;
    url.query_pairs_mut().append_pair("note", note_path);
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
    validate_uploaded_path(note_path, &result)?;
    Ok(result)
}

fn read_image(base_url: &str, path: &str) -> Result<Vec<u8>, AppError> {
    let (client, token, base) = connection(base_url)?;
    let mut url = base
        .join("v1/image")
        .map_err(|e| error("remoteConfig", e.to_string()))?;
    url.query_pairs_mut().append_pair("path", path);
    let response = client
        .get(url)
        .bearer_auth(token)
        .send()
        .map_err(|_| error("remoteNetwork", "图片加载失败或请求超时，请重试。"))?;
    image_response(response)
}

fn image_response(response: Response) -> Result<Vec<u8>, AppError> {
    let status = response.status();
    if !status.is_success() {
        json_response(status, &bounded_body(response, 16 * 1024)?)?;
        return Err(error("remoteHttp", "图片加载失败。"));
    }
    let mime = response
        .headers()
        .get(CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .unwrap_or("")
        .split(';')
        .next()
        .unwrap_or("")
        .trim()
        .to_ascii_lowercase();
    if !matches!(
        mime.as_str(),
        "image/png" | "image/jpeg" | "image/gif" | "image/webp" | "image/bmp"
    ) {
        return Err(error("remoteProtocol", "服务器未返回支持的图片类型。"));
    }
    let body = bounded_body(response, MAX_IMAGE_BYTES)?;
    if image_mime(&body)? != mime {
        return Err(error(
            "remoteProtocol",
            "服务器返回的图片类型与内容不一致。",
        ));
    }
    Ok(body)
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

// Paths are query parameters for the Linux server, not local Windows paths or URLs.
fn valid_relative_path(path: &str) -> bool {
    let bytes = path.as_bytes();
    let windows_absolute =
        bytes.len() >= 3 && bytes[0].is_ascii_alphabetic() && bytes[1] == b':' && bytes[2] == b'/';
    !windows_absolute
        && !path.is_empty()
        && !path.contains('\\')
        && !path.chars().any(char::is_control)
        && path.split('/').all(|part| !matches!(part, "" | "." | ".."))
}

fn valid_note_path(path: &str) -> bool {
    valid_relative_path(path)
        && path.rsplit_once('.').is_some_and(|(_, extension)| {
            extension.eq_ignore_ascii_case("md") || extension.eq_ignore_ascii_case("markdown")
        })
}

fn validate_uploaded_path(note_path: &str, image: &RemoteImageUpload) -> Result<(), AppError> {
    let filename = image.markdown_path.strip_prefix("images/").unwrap_or("");
    let extension = filename.rsplit('.').next().unwrap_or("");
    let parent = note_path.rsplit_once('/').map(|(parent, _)| parent);
    let expected_path = match parent {
        Some(parent) => format!("{parent}/{}", image.markdown_path),
        None => image.markdown_path.clone(),
    };
    if filename.is_empty()
        || !filename
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'.' | b'-' | b'_'))
        || !matches!(extension, "png" | "jpg" | "jpeg" | "gif" | "webp" | "bmp")
        || image.path != expected_path
    {
        return Err(error("remoteProtocol", "服务器返回了无效的图片路径。"));
    }
    Ok(())
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
        assert_eq!(
            decode_header("https%3A%2F%2Fnotes.example%2F").unwrap(),
            "https://notes.example/"
        );
        for value in ["%", "%2", "%GG", "%FF", "笔记.md"] {
            assert!(decode_header(value).is_err(), "{value}");
        }
    }

    #[test]
    fn image_upload_accepts_both_markdown_extensions() {
        for path in [
            "inbox.md",
            "inbox.MD",
            "笔记/收件箱.markdown",
            "笔记/收件箱.MARKDOWN",
        ] {
            assert!(valid_note_path(path), "{path}");
        }
        for path in [
            "笔记/收件箱.txt",
            "note.md/image.png",
            "../note.markdown",
            "",
        ] {
            assert!(!valid_note_path(path), "{path:?}");
        }
    }

    #[test]
    fn image_paths_stay_in_note_directory_and_are_safe_markdown() {
        assert!(valid_note_path("日记/10:30.markdown"));
        let upload = |path: &str, markdown_path: &str| RemoteImageUpload {
            path: path.into(),
            markdown_path: markdown_path.into(),
        };
        assert!(validate_uploaded_path(
            "笔记/inbox.md",
            &upload("笔记/images/abc.png", "images/abc.png")
        )
        .is_ok());
        assert!(
            validate_uploaded_path("inbox.md", &upload("images/abc.webp", "images/abc.webp"))
                .is_ok()
        );
        for (path, markdown_path) in [
            ("elsewhere/images/abc.png", "images/abc.png"),
            ("笔记/images/../abc.png", "images/../abc.png"),
            ("笔记/images/abc).png", "images/abc).png"),
            ("笔记/images/abc.svg", "images/abc.svg"),
            ("https://other/image.png", "https://other/image.png"),
        ] {
            assert!(validate_uploaded_path("笔记/inbox.md", &upload(path, markdown_path)).is_err());
        }
        for path in [
            "/root.png",
            "../secret.png",
            "a/../b.png",
            "a\\b.png",
            "C:/b.png",
            "a\nb.png",
            "a//b.png",
            "",
        ] {
            assert!(!valid_relative_path(path), "{path:?}");
        }
        assert!(valid_relative_path("笔记/图片/a b.png"));
    }

    #[test]
    fn only_bounded_raster_payloads_are_accepted() {
        for (data, mime) in [
            (b"\x89PNG\r\n\x1a\n".as_slice(), "image/png"),
            (b"\xff\xd8\xff", "image/jpeg"),
            (b"GIF89a", "image/gif"),
            (b"GIF87a", "image/gif"),
            (b"RIFF1234WEBP", "image/webp"),
            (b"BM", "image/bmp"),
        ] {
            assert_eq!(image_mime(data).unwrap(), mime);
        }
        for data in [
            b"<svg></svg>".as_slice(),
            b"<html>image</html>",
            b"RIFF1234WAVE",
            b"",
        ] {
            assert!(image_mime(data).is_err());
        }
        assert_eq!(
            image_mime(&vec![0; MAX_IMAGE_BYTES + 1]).unwrap_err().code,
            "tooLarge"
        );
    }

    #[test]
    fn image_responses_validate_mime_and_preserve_api_errors() {
        let png = b"\x89PNG\r\n\x1a\n";
        let response = http_response("HTTP/1.1 200 OK\r\nContent-Type: image/png", png);
        assert_eq!(image_response(response).unwrap(), png);
        for headers in [
            "HTTP/1.1 200 OK\r\nContent-Type: text/html",
            "HTTP/1.1 200 OK\r\nContent-Type: image/jpeg",
            "HTTP/1.1 200 OK",
        ] {
            assert_eq!(
                image_response(http_response(headers, png))
                    .unwrap_err()
                    .code,
                "remoteProtocol"
            );
        }
        let response = http_response(
            "HTTP/1.1 401 Unauthorized\r\nContent-Type: application/json",
            br#"{"code":"unauthorized","message":"unauthorized"}"#,
        );
        assert_eq!(image_response(response).unwrap_err().code, "unauthorized");
    }

    #[test]
    fn bounded_responses_reject_declared_and_streamed_excess() {
        for headers in ["HTTP/1.1 200 OK", "HTTP/1.1 200 OK\r\nContent-Length: 5"] {
            let response = http_response(headers, b"12345");
            assert_eq!(bounded_body(response, 4).unwrap_err().code, "tooLarge");
        }
    }
}
