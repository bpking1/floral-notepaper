import { describe, expect, it } from "vitest";
import { insertImageLinks, MAX_REMOTE_IMAGE_SIZE, validateImageUpload } from "./imageUpload";

const path = `images/${"a".repeat(64)}.png`;
describe("remote image uploads", () => {
  it("preserves surrounding text and inserts at a selection", () => {
    const result = insertImageLinks("beforeSELECTafter", 6, 12, [path]);
    expect(result.content).toBe(`before\n![](${path})\nafter`);
    expect(result.content.slice(result.caret)).toBe("after");
  });
  it("keeps selection unchanged when no image finished uploading", () => {
    expect(insertImageLinks("keep this", 0, 9, []).content).toBe("keep this");
  });
  it("can retain a successful first upload when a later upload fails", () => {
    const first = insertImageLinks("draft", 5, 5, [path]);
    expect(() =>
      insertImageLinks(first.content, first.caret, first.caret, ["images/bad)path.png"]),
    ).toThrow();
    expect(first.content).toBe(`draft\n![](${path})\n`);
  });
  it("accepts a raster image with empty MIME but rejects unsupported or excessive files", () => {
    expect(() => validateImageUpload({ name: "图.PNG", type: "", size: 20 })).not.toThrow();
    expect(() =>
      validateImageUpload({ name: "x.jpg", type: "image/jpeg", size: MAX_REMOTE_IMAGE_SIZE }),
    ).not.toThrow();
    for (const file of [
      { name: "x.svg", type: "image/svg+xml", size: 20 },
      { name: "x.png", type: "image/png", size: MAX_REMOTE_IMAGE_SIZE + 1 },
      { name: "x.png", type: "image/png", size: 0 },
      { name: "x.png", type: "text/html", size: 20 },
    ])
      expect(() => validateImageUpload(file)).toThrow();
  });
});
