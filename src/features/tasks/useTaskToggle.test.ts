import { describe, expect, it } from "vitest";
import { toggleTaskLine } from "./useTaskToggle";

describe("toggleTaskLine", () => {
  const chat = [
    "#### 29 September, Tuesday",
    "- [ ] `14:32` 买牛奶",
    "- [x] `16:05` 已完成",
    "  * [X] 嵌套",
    "> 1. [ ] 引用里的任务",
    "普通文本 - [ ] 不是任务",
    "```",
  ].join("\n");

  it("checks and unchecks the task on the given line only", () => {
    expect(toggleTaskLine(chat, 2)?.split("\n")[1]).toBe("- [x] `14:32` 买牛奶");
    expect(toggleTaskLine(chat, 3)?.split("\n")[2]).toBe("- [ ] `16:05` 已完成");
    expect(toggleTaskLine(chat, 4)?.split("\n")[3]).toBe("  * [ ] 嵌套");
    expect(toggleTaskLine(chat, 5)?.split("\n")[4]).toBe("> 1. [x] 引用里的任务");
    const toggled = toggleTaskLine(chat, 2)!.split("\n");
    expect(toggled.filter((line, i) => line !== chat.split("\n")[i])).toHaveLength(1);
  });

  it("ignores lines that are not tasks", () => {
    for (const line of [1, 6, 7, 99]) expect(toggleTaskLine(chat, line)).toBeNull();
  });

  it("keeps Windows line endings", () => {
    expect(toggleTaskLine("- [ ] a\r\n- [ ] b\r\n", 2)).toBe("- [ ] a\r\n- [x] b\r\n");
  });
});
