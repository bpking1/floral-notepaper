import type { RemoteDocument } from "./api";

export interface RemoteSession {
  /** Null for a fresh draft whose file name is generated on first save. */
  path: string | null;
  content: string;
  baseline: string;
  revision: string | null;
}
export function blankDraft(): RemoteSession {
  return { path: null, content: "", baseline: "", revision: null };
}
export function openedSession(path: string, doc: RemoteDocument): RemoteSession {
  return { path, content: doc.content, baseline: doc.content, revision: doc.revision };
}
export function isDirty(doc: RemoteSession | null): boolean {
  return doc !== null && (doc.revision === null || doc.content !== doc.baseline);
}
export function savedSession(
  doc: RemoteSession,
  path: string,
  submitted: string,
  revision: string,
): RemoteSession {
  return { ...doc, path, baseline: submitted, revision };
}
export function mergeBase(doc: RemoteSession, latest: RemoteDocument): RemoteSession {
  return { ...doc, baseline: latest.content, revision: latest.revision };
}
