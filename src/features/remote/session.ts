import type { RemoteDocument } from "./api";

export interface RemoteSession {
  path: string;
  content: string;
  baseline: string;
  revision: string | null;
}
export function openedSession(path: string, doc: RemoteDocument): RemoteSession {
  return { path, content: doc.content, baseline: doc.content, revision: doc.revision };
}
export function isDirty(doc: RemoteSession | null): boolean {
  return doc !== null && (doc.revision === null || doc.content !== doc.baseline);
}
export function savedSession(
  doc: RemoteSession,
  submitted: string,
  revision: string,
): RemoteSession {
  return { ...doc, baseline: submitted, revision };
}
export function mergeBase(doc: RemoteSession, latest: RemoteDocument): RemoteSession {
  return { ...doc, baseline: latest.content, revision: latest.revision };
}
