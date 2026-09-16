import { describe, expect, test } from "vitest";
import { rasterImageMime, resolveRemoteImageSource } from "./imagePaths";

describe("remote Markdown image paths", () => {
  test.each([
    ["Inbox.md", "images/photo.png", "images/photo.png"],
    ["daily/2026/note.md", "images/photo.png", "daily/2026/images/photo.png"],
    ["daily/2026/note.md", "../../images/photo.png", "images/photo.png"],
    ["daily/note.md", "./images/../photo.png", "daily/photo.png"],
    ["daily/note.md", "/images/photo.png", "images/photo.png"],
    ["daily/note.md", "../images/%E8%8A%B1%20%E7%AC%BA.png?width=500#image", "images/花 笺.png"],
    ["daily/note.md", "../images/a%23b%3Fc.png", "images/a#b?c.png"],
    ["daily/note.md", "%2e%2e/images/photo.png", "images/photo.png"],
    ["daily/note.md", "../images/100%2520.png", "images/100%20.png"],
    ["daily%20notes/note.md", "images/photo.png", "daily%20notes/images/photo.png"],
  ])("resolves %s + %s inside the vault", (note, src, expected) => {
    expect(resolveRemoteImageSource(note, src)).toEqual({ kind: "remote", path: expected });
  });

  test.each([
    "https://example.com/image.png?token=public#preview",
    "http://example.com/image.png",
    "HTTPS://example.com/image.png",
  ])("bypasses authenticated transport for %s", (url) => {
    expect(resolveRemoteImageSource("folder/note.md", url)).toEqual({ kind: "external", url });
  });

  test("uses HTTPS for protocol-relative images instead of inheriting the desktop scheme", () => {
    expect(resolveRemoteImageSource("folder/note.md", "//example.com/image.png")).toEqual({
      kind: "external",
      url: "https://example.com/image.png",
    });
  });

  test.each([
    undefined,
    "",
    "../../outside.png",
    "/../outside.png",
    "/%2e%2e/outside.png",
    "../%2e%2e/outside.png",
    "images\\photo.png",
    "images/%5cphoto.png",
    "images/%00photo.png",
    "images/\u0000photo.png",
    "images/%ZZ.png",
    "file:///etc/private.png",
    "data:image/svg+xml,<svg/>",
    "javascript:alert(1)",
    "C:/private.png",
    "ftp://example.com/photo.png",
    "https://",
    "//",
    "#anchor",
    "/",
  ])("rejects unsafe or unsupported image reference %s", (src) => {
    expect(resolveRemoteImageSource("notes/note.md", src).kind).toBe("invalid");
  });
});

describe("raster image detection", () => {
  test.each([
    [[0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a], "image/png"],
    [[0xff, 0xd8, 0xff, 0xe0], "image/jpeg"],
    [Array.from(new TextEncoder().encode("GIF87a")), "image/gif"],
    [Array.from(new TextEncoder().encode("GIF89a")), "image/gif"],
    [Array.from(new TextEncoder().encode("RIFF0000WEBP")), "image/webp"],
    [Array.from(new TextEncoder().encode("BM000000000000")), "image/bmp"],
  ])("recognizes raster bytes as %s", (bytes, expected) => {
    expect(rasterImageMime(new Uint8Array(bytes))).toBe(expected);
  });

  test.each([
    "<svg xmlns='http://www.w3.org/2000/svg'></svg>",
    "<html>Error</html>",
    "RIFF0000WAVE",
    "",
    "BM",
  ])("rejects unsupported or incomplete bytes %s", (content) =>
    expect(rasterImageMime(new TextEncoder().encode(content))).toBeUndefined(),
  );
});
