import { describe, expect, it } from "vitest";
import { pathSuggestions, titleTarget } from "./titleTarget";

describe("titleTarget", () => {
  it("maps titles to notes under the library root", () => {
    expect(titleTarget("")).toBeNull();
    expect(titleTarget("   ")).toBeNull();
    expect(titleTarget("读书笔记")).toEqual({ path: "读书笔记.md" });
    expect(titleTarget("/notes/read/test")).toEqual({ path: "notes/read/test.md" });
    expect(titleTarget(" notes / read / 三体 ")).toEqual({ path: "notes/read/三体.md" });
    expect(titleTarget("a/b.MARKDOWN")).toEqual({ path: "a/b.MARKDOWN" });
  });

  it("puts relative titles under the configured directory", () => {
    expect(titleTarget("读书笔记", "notes")).toEqual({ path: "notes/读书笔记.md" });
    expect(titleTarget("read/三体", "notes/new")).toEqual({ path: "notes/new/read/三体.md" });
    expect(titleTarget("/diary/today", "notes")).toEqual({ path: "diary/today.md" });
    expect(titleTarget("", "notes")).toBeNull();
  });

  it("rejects paths the server would refuse", () => {
    for (const title of [
      "/notes/",
      "a//b",
      "../x",
      "a/./b",
      ".git/x",
      "a/.hidden",
      "a:b",
      "why?",
      'x"y',
      "a\\b",
    ]) {
      expect(titleTarget(title)).toHaveProperty("error");
    }
  });
});

describe("pathSuggestions", () => {
  const files = [
    "notes/read/test.md",
    "notes/read/三体.md",
    "notes/recipes.md",
    "notes/work/plan.md",
    "diary/2026/09-28-读书三体.md",
    "Chat.md",
  ].map((path, i) => ({ path, modified: 100 - i }));
  const values = (title: string) => pathSuggestions(title, files).map((item) => item.value);

  it("lists all immediate directories and notes when browsing / or /dir/", () => {
    expect(values("/")).toEqual(["/diary/", "/notes/", "/Chat"]);
    expect(values("/notes/")).toEqual(["/notes/read/", "/notes/work/", "/notes/recipes"]);
    expect(values("/notes/read/")).toEqual(["/notes/read/test", "/notes/read/三体"]);
    expect(values("/NOTES/READ/")).toEqual([]);

    const manyRootFiles = Array.from({ length: 12 }, (_, i) => ({
      path: `note-${String(i + 1).padStart(2, "0")}.md`,
      modified: 100 - i,
    }));
    expect(pathSuggestions("/", manyRootFiles)).toHaveLength(12);
  });

  it("searches across all subdirectories and notes in the library", () => {
    // Prefix matches come before substring matches; directories precede notes in the same tier.
    expect(values("/re")).toEqual(["/notes/read/", "/notes/recipes"]);
    expect(values("/三体")).toEqual(["/notes/read/三体", "/diary/2026/09-28-读书三体"]);
    expect(values("/2026")).toEqual(["/diary/2026/"]);
    expect(values("/diary 09")).toEqual(["/diary/2026/09-28-读书三体"]);
    expect(values("/read/三")).toEqual(["/notes/read/三体"]);
    // Scoped search under an existing directory also searches its subdirectories recursively.
    expect(values("/notes/re")).toEqual(["/notes/read/", "/notes/recipes"]);
    expect(values("/notes/三体")).toEqual(["/notes/read/三体"]);
    // Does not match every file just because of its .md extension.
    expect(values("/md")).toEqual([]);
  });

  it("only suggests for titles starting with / and hides an exact match", () => {
    expect(values("notes")).toEqual([]);
    expect(values("/notes/read/test")).toEqual([]);
    expect(values("/notes/read/test.md")).toEqual([]);
  });
});
