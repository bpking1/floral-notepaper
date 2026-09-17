import { describe, expect, it } from "vitest";
import { buildRemoteNote, parseRemoteNote } from "./noteTitle";

describe("parseRemoteNote", () => {
  it("extracts a first-line heading as the title", () => {
    const parts = parseRemoteNote("# 购物清单\n牛奶\n鸡蛋");
    expect(parts).toEqual({ title: "购物清单", body: "牛奶\n鸡蛋", hadHeading: true });
  });

  it("keeps a lone heading without trailing newline", () => {
    expect(parseRemoteNote("# 标题")).toEqual({
      title: "标题",
      body: "",
      hadHeading: true,
    });
  });

  it("preserves blank line after heading", () => {
    expect(parseRemoteNote("# 标题\n\n正文")).toEqual({
      title: "标题",
      body: "\n正文",
      hadHeading: true,
    });
  });

  it("treats non-heading content as plain body", () => {
    expect(parseRemoteNote("## 二级标题\n正文")).toEqual({
      title: "",
      body: "## 二级标题\n正文",
      hadHeading: false,
    });
    expect(parseRemoteNote("")).toEqual({ title: "", body: "", hadHeading: false });
    expect(parseRemoteNote("\n# 后置标题")).toEqual({
      title: "",
      body: "\n# 后置标题",
      hadHeading: false,
    });
  });
});

describe("buildRemoteNote", () => {
  it("round-trips heading notes exactly", () => {
    for (const content of ["# 标题", "# 标题\n正文", "# 标题\n\n正文", "正文"]) {
      expect(buildRemoteNote(parseRemoteNote(content))).toBe(content);
    }
  });

  it("replaces and removes headings symmetrically", () => {
    const parsed = parseRemoteNote("# 旧标题\n正文");
    expect(buildRemoteNote({ ...parsed, title: "新标题" })).toBe("# 新标题\n正文");
    expect(buildRemoteNote({ ...parsed, title: "" })).toBe("正文");
  });

  it("inserts a blank line when adding a heading to plain content", () => {
    expect(buildRemoteNote({ title: "新", body: "正文", hadHeading: false })).toBe(
      "# 新\n\n正文",
    );
    expect(buildRemoteNote({ title: "新", body: "", hadHeading: false })).toBe("# 新");
  });
});
