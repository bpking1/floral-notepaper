import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  getRemoteConfig: vi.fn(),
  readRemoteFile: vi.fn(),
  writeRemoteFile: vi.fn(),
}));
vi.mock("./api", () => api);
vi.mock("../notes/api", () => ({ getNote: vi.fn(), updateNote: vi.fn() }));
vi.stubGlobal("window", new EventTarget());

import {
  REMOTE_CONFLICT_EVENT,
  getNote,
  isRemoteNoteId,
  remoteNoteId,
  remoteNotePath,
  updateNote,
} from "./remoteNotes";

const save = (id: string, content: string) => updateNote(id, { title: "x", content, category: "" });

describe("remote note ids", () => {
  it("round-trips unicode paths through URL-safe ids", () => {
    for (const path of ["日记/2026/09-28.md", "a b&c#d?.md", "x/y+z=.markdown"]) {
      const id = remoteNoteId(path);
      expect(id).toMatch(/^remote:[A-Za-z0-9_-]+$/);
      expect(isRemoteNoteId(id)).toBe(true);
      expect(remoteNotePath(id)).toBe(path);
    }
    expect(isRemoteNoteId("20260928-abc")).toBe(false);
    expect(isRemoteNoteId(null)).toBe(false);
  });
});

describe("remote note saving", () => {
  const id = remoteNoteId("日记/今天.md");

  beforeEach(async () => {
    vi.resetAllMocks();
    api.getRemoteConfig.mockResolvedValue({ enabled: true, baseUrl: "https://n/" });
    api.readRemoteFile.mockResolvedValue({ content: "base", revision: "r1" });
    await getNote(id);
  });

  it("opens with the file name as title and writes against the read revision", async () => {
    api.writeRemoteFile.mockResolvedValue({ revision: "r2" });
    expect((await save(id, "edited")).title).toBe("今天");
    await save(id, "edited again");
    expect(api.writeRemoteFile.mock.calls).toEqual([
      ["https://n/", "日记/今天.md", "edited", "r1"],
      ["https://n/", "日记/今天.md", "edited again", "r2"],
    ]);
  });

  it("skips unchanged content", async () => {
    await save(id, "base");
    expect(api.writeRemoteFile).not.toHaveBeenCalled();
  });

  it("announces conflicts and keeps the old base so later saves cannot overwrite", async () => {
    const conflicts: string[] = [];
    const listener = (event: Event) => conflicts.push((event as CustomEvent<string>).detail);
    window.addEventListener(REMOTE_CONFLICT_EVENT, listener);
    api.writeRemoteFile.mockRejectedValue({ code: "conflict", message: "changed" });
    await expect(save(id, "mine")).rejects.toMatchObject({ code: "conflict" });
    await expect(save(id, "mine 2")).rejects.toMatchObject({ code: "conflict" });
    window.removeEventListener(REMOTE_CONFLICT_EVENT, listener);
    expect(conflicts).toEqual([id, id]);
    expect(api.writeRemoteFile.mock.calls.map((call) => call[3])).toEqual(["r1", "r1"]);
  });
});
