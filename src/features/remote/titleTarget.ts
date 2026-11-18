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

const MAX_SEARCH_SUGGESTIONS = 50;

interface RankedDirectory {
  relDir: string;
  depth: number;
  item: PathSuggestion;
}

/**
 * Suggests directories and notes for titles starting with `/`:
 * - Browsing (`/` or `/dir/`): lists all immediate subdirectories (alphabetical)
 *   and immediate notes (newest first) under that directory.
 * - Searching (`/query` or `/dir/query`): searches across all subdirectories and
 *   notes in the library (or recursively under `/dir/` when `dir/` exists),
 *   ranking prefix matches ahead of substring matches.
 */
export function pathSuggestions(title: string, files: RemoteFileEntry[]): PathSuggestion[] {
  if (!title.startsWith("/")) return [];
  const typed = title.replace(/^\/+/, "");

  // Directory browse mode (`/` for root, or `/dir/`): show all immediate entries.
  if (typed === "" || typed.endsWith("/")) {
    const directories = new Set<string>();
    const notes: PathSuggestion[] = [];
    const seenNotes = new Set<string>();
    for (const { path } of files) {
      if (!path.startsWith(typed)) continue;
      const rest = path.slice(typed.length);
      if (!rest) continue;
      const slash = rest.indexOf("/");
      if (slash >= 0) {
        directories.add(rest.slice(0, slash));
      } else {
        const name = rest.replace(/\.(md|markdown)$/i, "");
        const value = `/${typed}${name}`;
        if (!seenNotes.has(value)) {
          seenNotes.add(value);
          notes.push({ value, label: rest, isDirectory: false });
        }
      }
    }
    const directoryItems = [...directories]
      .sort((a, b) => a.localeCompare(b))
      .map((name) => ({ value: `/${typed}${name}/`, label: `${name}/`, isDirectory: true }));
    return [...directoryItems, ...notes].filter((item) => item.value !== title);
  }

  // Search mode: scope to `dir/` if the prefix before the last `/` is an existing directory,
  // otherwise search across the entire note library.
  const lastSlash = typed.lastIndexOf("/");
  const candidateDir = lastSlash >= 0 ? typed.slice(0, lastSlash + 1) : "";
  const hasCandidateDir =
    candidateDir !== "" && files.some((file) => file.path.startsWith(candidateDir));
  const scopeDir = hasCandidateDir ? candidateDir : "";
  const rawQuery = hasCandidateDir ? typed.slice(lastSlash + 1) : typed;
  const terms = rawQuery.toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return [];

  const singlePlainTerm = terms.length === 1 && !terms[0].includes("/") ? terms[0] : null;
  const seenDirs = new Set<string>();
  const seenNotes = new Set<string>();
  const prefixDirs: RankedDirectory[] = [];
  const substrDirs: RankedDirectory[] = [];
  const prefixNotes: PathSuggestion[] = [];
  const substrNotes: PathSuggestion[] = [];

  for (const { path } of files) {
    if (!path.startsWith(scopeDir)) continue;
    const rest = path.slice(scopeDir.length);
    if (!rest) continue;

    const segments = rest.split("/");
    for (let i = 0; i < segments.length - 1; i++) {
      const relDir = segments.slice(0, i + 1).join("/");
      if (seenDirs.has(relDir)) continue;
      seenDirs.add(relDir);

      const leafLower = segments[i].toLowerCase();
      const relDirLower = relDir.toLowerCase();
      let dirRank = -1;
      if (singlePlainTerm !== null) {
        if (leafLower.startsWith(singlePlainTerm)) dirRank = 0;
        else if (leafLower.includes(singlePlainTerm)) dirRank = 1;
      } else {
        const lastTerm = terms[terms.length - 1];
        const lastSeg = lastTerm.slice(lastTerm.lastIndexOf("/") + 1);
        if (terms.every((term) => relDirLower.includes(term)) && leafLower.includes(lastSeg)) {
          dirRank = leafLower.startsWith(lastSeg) ? 0 : 1;
        }
      }
      if (dirRank >= 0) {
        const entry: RankedDirectory = {
          relDir,
          depth: i + 1,
          item: { value: `/${scopeDir}${relDir}/`, label: `${relDir}/`, isDirectory: true },
        };
        if (dirRank === 0) prefixDirs.push(entry);
        else substrDirs.push(entry);
      }
    }

    const fileName = segments[segments.length - 1];
    const baseName = fileName.replace(/\.(md|markdown)$/i, "");
    const relNoExt =
      segments.length > 1 ? `${segments.slice(0, -1).join("/")}/${baseName}` : baseName;
    const value = `/${scopeDir}${relNoExt}`;
    if (seenNotes.has(value)) continue;
    seenNotes.add(value);

    const baseLower = baseName.toLowerCase();
    const fileLower = fileName.toLowerCase();
    const relNoExtLower = relNoExt.toLowerCase();
    const restLower = rest.toLowerCase();
    let noteRank = -1;
    if (singlePlainTerm !== null) {
      const target = singlePlainTerm.includes(".") ? fileLower : baseLower;
      if (target.startsWith(singlePlainTerm)) noteRank = 0;
      else if (target.includes(singlePlainTerm)) noteRank = 1;
    } else if (
      terms.every((term) => (term.includes(".") ? restLower : relNoExtLower).includes(term))
    ) {
      const lastTerm = terms[terms.length - 1];
      const lastSeg = lastTerm.slice(lastTerm.lastIndexOf("/") + 1);
      const leafTarget = lastSeg.includes(".") ? fileLower : baseLower;
      noteRank = leafTarget.startsWith(lastSeg) ? 0 : 1;
    }
    if (noteRank === 0) {
      prefixNotes.push({ value, label: rest, isDirectory: false });
    } else if (noteRank === 1) {
      substrNotes.push({ value, label: rest, isDirectory: false });
    }
  }

  const sortDirs = (list: RankedDirectory[]) =>
    list
      .sort((a, b) => a.depth - b.depth || a.relDir.localeCompare(b.relDir))
      .map((entry) => entry.item);

  // Files are newest first, as listed; hide an entry the title already names exactly.
  return [...sortDirs(prefixDirs), ...prefixNotes, ...sortDirs(substrDirs), ...substrNotes]
    .filter((item) => item.value !== title && (item.isDirectory || `/${scopeDir}${item.label}` !== title))
    .slice(0, MAX_SEARCH_SUGGESTIONS);
}
