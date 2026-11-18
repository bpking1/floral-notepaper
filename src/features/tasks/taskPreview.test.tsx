import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { MarkdownPreview } from "../markdown/MarkdownPreview";

vi.mock("@tauri-apps/plugin-opener", () => ({ openUrl: vi.fn() }));

const content = "#### 29 September, Tuesday\n- [ ] a\n- [x] b\n  - [ ] nested\n\n> - [ ] quoted\n";

describe("task checkboxes in the preview", () => {
  it("stay disabled unless an onContentChange handler is given", () => {
    expect(renderToStaticMarkup(<MarkdownPreview content={content} />)).toMatch(
      /<input[^>]*disabled/,
    );
    const clickable = renderToStaticMarkup(
      <MarkdownPreview content={content} onContentChange={() => undefined} />,
    );
    expect(clickable).toContain('type="checkbox"');
    expect(clickable).not.toMatch(/<input[^>]*disabled/);
  });
});
