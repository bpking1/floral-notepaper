import { describe, expect, it, vi } from "vitest";

vi.mock("./api", () => ({}));
vi.mock("../notes/api", () => ({}));

import { replaceLocalImages } from "./inbox";

describe("replaceLocalImages", () => {
  it("uploads each local image once and rewrites only local links", async () => {
    const upload = vi.fn(async (path: string) => `../attachments/${path.split("/").pop()}`);
    const content = [
      "![](images/n1/a.png)",
      "![图](<images/n1/b.png>) 文字 ![](images/n1/a.png)",
      "![](https://example.com/images/c.png)",
      "纯文本 images/n1/d.png",
    ].join("\n");
    expect(await replaceLocalImages(content, upload)).toBe(
      [
        "![](../attachments/a.png)",
        "![图](../attachments/b.png) 文字 ![](../attachments/a.png)",
        "![](https://example.com/images/c.png)",
        "纯文本 images/n1/d.png",
      ].join("\n"),
    );
    expect(upload.mock.calls.map(([path]) => path)).toEqual(["images/n1/a.png", "images/n1/b.png"]);
  });

  it("leaves content untouched when an upload fails", async () => {
    await expect(
      replaceLocalImages("![](images/n1/a.png)", () => Promise.reject(new Error("offline"))),
    ).rejects.toThrow("offline");
  });
});
