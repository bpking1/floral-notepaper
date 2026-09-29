import type { RemoteFileEntry } from "./api";

export type TitleTarget = { path: string } | { error: string };

// Characters Windows rejects in file names; the notes library is also synced there.
function hasInvalidChars(part: string): boolean {
  return (
    /[\\:*?"<>|]/.test(part) ||
    [...part].some((ch) => ch.charCodeAt(0) < 0x20 || ch.charCodeAt(0) === 0x7f)
  );
}

/**
 * The note a titled capture is sent to. Titles are relative to `newDir` (the
 * server's NOTES_NEW_DIR), and a leading `/` starts from the library root:
 * with newDir `notes`, `读书笔记` → `notes/读书笔记.md` and `/diary/today` →
 * `diary/today.md`. Returns null for an empty title, which goes to the inbox.
 */
export function titleTarget(title: string, newDir = ""): TitleTarget | null {
  const trimmed = title.trim();
  if (!trimmed) return null;
  const fromRoot = trimmed.startsWith("/");
  const parts = trimmed
    .replace(/^\/+/, "")
    .split("/")
    .map((part) => part.trim());
  if (!fromRoot && newDir) parts.unshift(...newDir.split("/"));
  if (parts.some((part) => part === "" || part === "." || part === "..")) {
    return { error: "路径不完整：不能以 / 结尾，也不能包含空段、. 或 .." };
  }
  if (parts.some((part) => part.startsWith("."))) {
    return { error: "不能写入以 . 开头的隐藏文件或目录" };
  }
  if (parts.some(hasInvalidChars)) {
    return { error: '标题不能包含 \\ : * ? " < > |' };
  }
  const path = parts.join("/");
  return { path: /\.(md|markdown)$/i.test(path) ? path : `${path}.md` };
}

export interface PathSuggestion {
  /** Title to fill in, e.g. `/notes/read/` for a directory or `/notes/read/test` for a note. */
  value: string;
  label: string;
  isDirectory: boolean;
}

const MAX_SUGGESTIONS = 8;

/** Directories and notes under the path typed so far, for titles starting with `/`. */
export function pathSuggestions(title: string, files: RemoteFileEntry[]): PathSuggestion[] {
  if (!title.startsWith("/")) return [];
  const typed = title.replace(/^\/+/, "");
  const slash = typed.lastIndexOf("/");
  const dir = slash >= 0 ? typed.slice(0, slash + 1) : "";
  const prefix = typed.slice(slash + 1).toLowerCase();
  const directories = new Set<string>();
  const notes: PathSuggestion[] = [];
  for (const { path } of files) {
    if (!path.startsWith(dir)) continue;
    const rest = path.slice(dir.length);
    const child = rest.split("/")[0];
    if (!child.toLowerCase().startsWith(prefix)) continue;
    if (rest.includes("/")) {
      directories.add(child);
    } else {
      const name = child.replace(/\.(md|markdown)$/i, "");
      notes.push({ value: `/${dir}${name}`, label: child, isDirectory: false });
    }
  }
  const directoryItems = [...directories]
    .sort((a, b) => a.localeCompare(b))
    .map((name) => ({ value: `/${dir}${name}/`, label: `${name}/`, isDirectory: true }));
  // Files are newest first, as listed; hide an entry the title already names exactly.
  return [...directoryItems, ...notes]
    .filter((item) => item.value !== title)
    .slice(0, MAX_SUGGESTIONS);
}
