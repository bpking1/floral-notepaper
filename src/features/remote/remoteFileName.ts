const ILLEGAL_CHARS = new Set(["\\", "/", ":", "*", "?", '"', "<", ">", "|"]);
const MAX_NAME_LENGTH = 50;

function isIllegalFileNameChar(ch: string): boolean {
  const code = ch.codePointAt(0) ?? 0;
  return ILLEGAL_CHARS.has(ch) || code < 32 || code === 127;
}

function pad(value: number): string {
  return String(value).padStart(2, "0");
}

function timestamp(date: Date): string {
  return `${date.getFullYear()}${pad(date.getMonth() + 1)}${pad(date.getDate())}-${pad(
    date.getHours(),
  )}${pad(date.getMinutes())}${pad(date.getSeconds())}`;
}

export function sanitizeFileNamePart(title: string): string {
  return Array.from(title)
    .map((ch) => (isIllegalFileNameChar(ch) ? " " : ch))
    .join("")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/[. ]+$/g, "")
    .slice(0, MAX_NAME_LENGTH)
    .trim();
}

/** `购物清单` → `购物清单.md`, `购物清单-2.md`, …, finally a timestamped name. */
export function candidateFileNames(title: string, date = new Date()): string[] {
  const base = sanitizeFileNamePart(title);
  if (!base) return [`笔记-${timestamp(date)}.md`];
  const names = [`${base}.md`];
  for (let i = 2; i <= 9; i++) names.push(`${base}-${i}.md`);
  names.push(`${base}-${timestamp(date)}.md`);
  return names;
}
