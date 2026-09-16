export const MAX_REMOTE_IMAGE_SIZE = 20 * 1024 * 1024;
export const REMOTE_IMAGE_ACCEPT =
  "image/png,image/jpeg,image/gif,image/webp,image/bmp,.png,.jpg,.jpeg,.gif,.webp,.bmp";
const supportedTypes = new Set([
  "image/png",
  "image/jpeg",
  "image/gif",
  "image/webp",
  "image/bmp",
  "image/x-ms-bmp",
]);

export function validateImageUpload(file: Pick<File, "size" | "type" | "name">): void {
  if (
    !supportedTypes.has(file.type) &&
    !(file.type === "" && /\.(png|jpe?g|gif|webp|bmp)$/i.test(file.name))
  ) {
    throw new Error("请选择 PNG、JPEG、GIF、WebP 或 BMP 图片，暂不支持 SVG。");
  }
  if (file.size === 0) throw new Error("图片文件为空。");
  if (file.size > MAX_REMOTE_IMAGE_SIZE) throw new Error("图片过大，单张上限为 20 MiB。");
}

/** Insert completed uploads at the original selection; failure never removes selected text. */
export function insertImageLinks(content: string, start: number, end: number, paths: string[]) {
  if (paths.length === 0) return { content, caret: start };
  // The service generates hash-only names, keeping Markdown syntax and URLs out of filenames.
  if (paths.some((path) => !/^images\/[a-f0-9]{64}\.(png|jpg|gif|webp|bmp)$/.test(path))) {
    throw new Error("服务器返回的图片路径格式异常。");
  }
  const before = content.slice(0, start);
  const insertion =
    (before && !before.endsWith("\n") ? "\n" : "") +
    paths.map((path) => `![](${path})`).join("\n") +
    "\n";
  return {
    content: before + insertion + content.slice(end),
    caret: before.length + insertion.length,
  };
}
