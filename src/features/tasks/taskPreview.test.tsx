import Markdown from "react-markdown";
import { renderToStaticMarkup } from "react-dom/server";
import remarkGfm from "remark-gfm";
import { describe, expect, it, vi } from "vitest";
import { MarkdownPreview } from "../markdown/MarkdownPreview";
import { toggleTaskLine } from "./useTaskToggle";

vi.mock("@tauri-apps/plugin-opener", () => ({ openUrl: vi.fn() }));

const content = "#### 29 September, Tuesday\n- [ ] a\n- [x] b\n  - [ ] nested\n\n> - [ ] quoted\n";

describe("task checkboxes in the preview", () => {
  it("stay disabled unless a toggle handler is given", () => {
    expect(renderToStaticMarkup(<MarkdownPreview content={content} />)).toMatch(
      /<input[^>]*disabled/,
    );
    const clickable = renderToStaticMarkup(
      <MarkdownPreview content={content} onToggleTask={() => undefined} />,
    );
    expect(clickable).toContain('type="checkbox"');
    expect(clickable).not.toMatch(/<input[^>]*disabled/);
  });

  it("reports task item lines that toggleTaskLine can flip", () => {
    const lines: number[] = [];
    renderToStaticMarkup(
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          li: ({ node, className, children }) => {
            if (String(className).includes("task-list-item"))
              lines.push(node!.position!.start.line);
            return <li>{children}</li>;
          },
        }}
      >
        {content}
      </Markdown>,
    );
    expect(lines).toEqual([2, 3, 4, 6]);
    for (const line of lines) expect(toggleTaskLine(content, line)).not.toBeNull();
  });
});
