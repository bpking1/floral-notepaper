import { describe, expect, it } from "vitest";
import { blankDraft, isDirty, mergeBase, openedSession, savedSession } from "./session";

describe("remote draft preservation", () => {
  it("keeps edits made after a save snapshot dirty", () => {
    const doc = {
      ...openedSession("Inbox.md", { content: "old", revision: '"v1"' }),
      content: "newer draft",
    };
    const result = savedSession(doc, "Inbox.md", "submitted draft", '"v2"');
    expect(result.content).toBe("newer draft");
    expect(isDirty(result)).toBe(true);
  });
  it("adopts a server version only as merge base, preserving the draft", () => {
    const doc = {
      ...openedSession("a.md", { content: "old", revision: '"v1"' }),
      content: "my work",
    };
    const merged = mergeBase(doc, { content: "nvim edit", revision: '"v2"' });
    expect(merged).toEqual({
      path: "a.md",
      content: "my work",
      baseline: "nvim edit",
      revision: '"v2"',
    });
    expect(isDirty(merged)).toBe(true);
  });
  it("treats even an empty new file as pending, and successful saves as clean", () => {
    const doc = { path: "new.md", content: "", baseline: "", revision: null };
    expect(isDirty(doc)).toBe(true);
    expect(isDirty(savedSession(doc, "new.md", "", '"v1"'))).toBe(false);
    expect(isDirty(null)).toBe(false);
  });
  it("starts every quick note as a pathless blank draft", () => {
    const draft = blankDraft();
    expect(draft.path).toBeNull();
    expect(isDirty(draft)).toBe(true);
  });
});
