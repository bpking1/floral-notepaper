export type RemoteImageSource =
  | { kind: "remote"; path: string }
  | { kind: "external"; url: string }
  | { kind: "invalid"; reason: string };

function hasInvalidPathCharacters(value: string): boolean {
  for (const character of value) {
    const code = character.charCodeAt(0);
    if (character === "\\" || code < 32 || code === 127) return true;
  }
  return false;
}

/** Markdown URLs are decoded once; notePath is already a literal vault path. */
export function resolveRemoteImageSource(
  notePath: string,
  src: string | undefined,
): RemoteImageSource {
  const invalid = (reason: string): RemoteImageSource => ({ kind: "invalid", reason });
  const source = src?.trim();
  if (!source) return invalid("图片地址为空或不受支持。");
  if (hasInvalidPathCharacters(source)) return invalid("图片地址包含无效字符。");

  // External images use the browser directly and never receive the API token.
  if (/^https?:\/\//i.test(source) || source.startsWith("//")) {
    try {
      // A protocol-relative URL would otherwise inherit the desktop's tauri: scheme.
      const externalUrl = source.startsWith("//") ? `https:${source}` : source;
      const url = new URL(externalUrl);
      if (url.hostname) return { kind: "external", url: externalUrl };
    } catch {
      // Report a readable placeholder instead of treating malformed URLs as vault paths.
    }
    return invalid("图片网址无效。");
  }
  if (/^[a-z][a-z\d+.-]*:/i.test(source)) return invalid("不支持此图片地址类型。");

  let path: string;
  try {
    path = decodeURIComponent(source.split(/[?#]/, 1)[0]);
  } catch {
    return invalid("图片路径编码无效。");
  }
  if (!path || hasInvalidPathCharacters(path)) {
    return invalid("图片路径无效。");
  }
  const segments = path.startsWith("/") ? [] : notePath.split("/").slice(0, -1);
  if (
    segments.some(
      (part) => !part || part === "." || part === ".." || hasInvalidPathCharacters(part),
    )
  ) {
    return invalid("笔记路径无效。");
  }
  for (const part of path.split("/")) {
    if (!part || part === ".") continue;
    if (part === "..") {
      if (!segments.length) return invalid("图片路径超出了笔记库。");
      segments.pop();
    } else {
      segments.push(part);
    }
  }
  if (!segments.length) return invalid("图片路径未指向文件。");
  return { kind: "remote", path: segments.join("/") };
}

/** Never trust an extension or response header when creating browser image blobs. */
export function rasterImageMime(bytes: Uint8Array): string | undefined {
  const startsWith = (...signature: number[]) =>
    signature.every((value, index) => bytes[index] === value);
  const ascii = (offset: number, text: string) =>
    Array.from(text).every((character, index) => bytes[offset + index] === character.charCodeAt(0));
  if (startsWith(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a)) return "image/png";
  if (startsWith(0xff, 0xd8, 0xff)) return "image/jpeg";
  if (ascii(0, "GIF87a") || ascii(0, "GIF89a")) return "image/gif";
  if (ascii(0, "RIFF") && ascii(8, "WEBP")) return "image/webp";
  if (bytes.length >= 14 && ascii(0, "BM")) return "image/bmp";
  return undefined;
}
