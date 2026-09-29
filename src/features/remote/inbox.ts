import { moveNoteCategory } from "../notes/api";
import type { Note } from "../notes/types";
import { appendToInbox, uploadInboxImage } from "./api";
import { loadServerConfig } from "./remoteListing";
import { titleTarget } from "./titleTarget";

export const SENT_CATEGORY = "已发送";

// Images pasted into local notes are linked as `images/<noteId>/<file>`.
const LOCAL_IMAGE = /(!\[[^\]]*\]\(\s*)<?(images\/[^\s)>]+)>?/g;

/** Replaces local image links using `upload`, which returns the remote link for a local path. */
export async function replaceLocalImages(
  content: string,
  upload: (localPath: string) => Promise<string>,
): Promise<string> {
  const links = new Map<string, string>();
  for (const [, , localPath] of content.matchAll(LOCAL_IMAGE)) {
    if (!links.has(localPath)) links.set(localPath, await upload(localPath));
  }
  return content.replace(
    LOCAL_IMAGE,
    (_, open: string, localPath: string) => open + links.get(localPath),
  );
}

async function captureId(note: Note): Promise<string> {
  const data = new TextEncoder().encode(`${note.id}\n${note.title}\n${note.content}`);
  const digest = await crypto.subtle.digest("SHA-256", data);
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
}

/**
 * Uploads the note's images and sends it: untitled notes go to the remote
 * inbox, titled ones to the note the title names (see titleTarget). Then files
 * it under the sent category. Retries are safe: the server reuses identical
 * images and de-duplicates the append by id.
 */
export async function sendNoteToInbox(note: Note): Promise<void> {
  const newDir = note.title.trim() ? (await loadServerConfig()).newDir : "";
  const target = titleTarget(note.title, newDir);
  if (target && "error" in target) throw new Error(target.error);
  const path = target?.path;
  const text = await replaceLocalImages(
    note.content,
    async (localPath) => (await uploadInboxImage(localPath, path)).markdownPath,
  );
  await appendToInbox(await captureId(note), note.title, text, path);
  await moveNoteCategory(note.id, SENT_CATEGORY);
}
