const HEADING_PATTERN = /^# (.*)$/;

export interface RemoteNoteParts {
  title: string;
  body: string;
  hadHeading: boolean;
}

export function parseRemoteNote(content: string): RemoteNoteParts {
  if (!content.startsWith("# ")) {
    return { title: "", body: content, hadHeading: false };
  }
  const firstLineEnd = content.indexOf("\n");
  const firstLine =
    firstLineEnd === -1 ? content : content.slice(0, firstLineEnd);
  const rest = firstLineEnd === -1 ? "" : content.slice(firstLineEnd + 1);
  const title = HEADING_PATTERN.exec(firstLine)?.[1]?.trim() ?? "";
  return { title, body: rest, hadHeading: true };
}

export function buildRemoteNote(parts: RemoteNoteParts): string {
  const { title, body, hadHeading } = parts;
  if (title) {
    if (!body) return `# ${title}`;
    if (!hadHeading && !body.startsWith("\n")) return `# ${title}\n\n${body}`;
    return `# ${title}\n${body}`;
  }
  if (hadHeading) return body.replace(/^\n/, "");
  return body;
}
