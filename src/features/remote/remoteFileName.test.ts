import { describe, expect, it } from "vitest";
import { candidateFileNames, sanitizeFileNamePart } from "./remoteFileName";

describe("sanitizeFileNamePart", () => {
  it("strips characters that are invalid in file names", () => {
    expect(sanitizeFileNamePart('a/b\\c:d*e?f"g<h>i|j')).toBe("a b c d e f g h i j");
  });

  it("collapses whitespace and trims trailing dots", () => {
    expect(sanitizeFileNamePart("  多个   空格  ")).toBe("多个 空格");
    expect(sanitizeFileNamePart("结尾点...")).toBe("结尾点");
  });

  it("caps the length at 50 characters", () => {
    expect(sanitizeFileNamePart("长".repeat(80)).length).toBeLessThanOrEqual(50);
  });

  it("returns empty for titles with only invalid characters", () => {
    expect(sanitizeFileNamePart("///")).toBe("");
  });
});

describe("candidateFileNames", () => {
  it("generates numbered candidates from a title", () => {
    const names = candidateFileNames("购物清单");
    expect(names[0]).toBe("购物清单.md");
    expect(names[1]).toBe("购物清单-2.md");
    expect(names).toHaveLength(10);
  });

  it("falls back to a timestamped name without a title", () => {
    const names = candidateFileNames("  ", new Date(2026, 8, 18, 0, 30, 15));
    expect(names).toEqual(["笔记-20260918-003015.md"]);
  });

  it("ends with a timestamped candidate to escape collisions", () => {
    const names = candidateFileNames("笔记", new Date(2026, 8, 18, 0, 30, 15));
    expect(names[names.length - 1]).toBe("笔记-20260918-003015.md");
  });
});
