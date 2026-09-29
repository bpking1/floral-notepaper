import { describe, expect, it } from "vitest";
import { parseRemoteListing } from "./api";
import { filterRemoteFiles } from "./RemoteOpenPanel";

describe("filterRemoteFiles", () => {
  const files = ["日记/2026/09-28.md", "日记/2025/09-01.md", "项目/Notes.md"].map((path) => ({
    path,
    modified: 1790000000,
  }));
  const paths = (query: string) => filterRemoteFiles(files, query).map((file) => file.path);

  it("matches every term anywhere in the path, ignoring case", () => {
    expect(paths("日记 2026")).toEqual(["日记/2026/09-28.md"]);
    expect(paths("notes")).toEqual(["项目/Notes.md"]);
    expect(paths("  ")).toHaveLength(3);
    expect(paths("日记 missing")).toEqual([]);
  });
});

describe("parseRemoteListing", () => {
  it("accepts current, older and malformed server responses without throwing", () => {
    expect(
      parseRemoteListing({
        files: [
          { path: "a.md", modified: 1790000000 },
          { path: "b.md", modified: "2026-09-28T06:32:00Z" },
          "c.md",
          { path: "d.md", modified: "not a date" },
          { modified: 1 },
          null,
          42,
        ],
        truncated: true,
      }),
    ).toEqual({
      files: [
        { path: "a.md", modified: 1790000000 },
        { path: "b.md", modified: Date.parse("2026-09-28T06:32:00Z") / 1000 },
        { path: "c.md", modified: 0 },
        { path: "d.md", modified: 0 },
      ],
      truncated: true,
    });
    expect(parseRemoteListing(null)).toEqual({ files: [], truncated: false });
    expect(parseRemoteListing({ files: "x" })).toEqual({ files: [], truncated: false });
  });
});
