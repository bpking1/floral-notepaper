import { describe, expect, it, vi } from "vitest";

vi.mock("./api", () => ({}));

import { resolveLibraryPath, withRemoteImageUrls } from "./remoteImages";

describe("resolveLibraryPath", () => {
  it("resolves relative links from the note directory", () => {
    expect(resolveLibraryPath("inbox/2026-09.md", "../attachments/a.png")).toBe(
      "attachments/a.png",
    );
    expect(resolveLibraryPath("日记/今天.md", "./图%20片.png")).toBe("日记/图 片.png");
    expect(resolveLibraryPath("a.md", "attachments/a.png")).toBe("attachments/a.png");
  });

  it("ignores URLs, absolute paths and links leaving the library", () => {
    for (const link of [
      "https://x/a.png",
      "data:image/png;base64,x",
      "/etc/a.png",
      "#a",
      "../a.png",
    ]) {
      expect(resolveLibraryPath("a.md", link)).toBeNull();
    }
  });
});

describe("withRemoteImageUrls", () => {
  it("rewrites only resolvable image links", () => {
    const content = [
      "![](../attachments/a.png)",
      "![图](<../attachments/架构 图.png>)",
      "![](https://example.com/c.png) [link](../x.md)",
    ].join("\n");
    expect(withRemoteImageUrls(content, "inbox/2026-09.md", "http://nas:8789/")).toBe(
      [
        "![](http://nas:8789/v1/image?path=attachments%2Fa.png)",
        "![图](http://nas:8789/v1/image?path=attachments%2F%E6%9E%B6%E6%9E%84+%E5%9B%BE.png)",
        "![](https://example.com/c.png) [link](../x.md)",
      ].join("\n"),
    );
  });

  it("keeps a reverse proxy prefix", () => {
    expect(withRemoteImageUrls("![](a.png)", "n.md", "https://h/notes/")).toBe(
      "![](https://h/notes/v1/image?path=a.png)",
    );
  });
});
