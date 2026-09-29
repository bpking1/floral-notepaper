import { getNote as getLocalNote, updateNote as updateLocalNote } from "../notes/api";
import { countNoteChars } from "../notes/noteUtils";
import type { Note, SaveNoteRequest } from "../notes/types";
import { getRemoteConfig, readRemoteFile, writeRemoteFile } from "./api";
import type { RemoteDocument } from "./api";

const PREFIX = "remote:";

/** Fired on `window` when a save hits a newer server version; detail is the note id. */
export const REMOTE_CONFLICT_EVENT = "remote-note:conflict";

export function isRemoteNoteId(id: string | null | undefined): id is string {
  return Boolean(id?.startsWith(PREFIX));
}

// base64url keeps ids safe in window URLs and labels, which Rust builds without escaping.
export function remoteNoteId(path: string): string {
  let binary = "";
  for (const byte of new TextEncoder().encode(path)) binary += String.fromCharCode(byte);
  return PREFIX + btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

export function remoteNotePath(id: string): string {
  const base64 = id.slice(PREFIX.length).replace(/-/g, "+").replace(/_/g, "/");
  const binary = atob(base64 + "=".repeat((4 - (base64.length % 4)) % 4));
  return new TextDecoder().decode(Uint8Array.from(binary, (ch) => ch.charCodeAt(0)));
}

export function remoteFileTitle(path: string): string {
  return (path.split("/").pop() ?? path).replace(/\.(md|markdown)$/i, "");
}

interface SyncedFile {
  baseUrl: string;
  revision: string;
  content: string;
}

// The server version each open file was last read or saved at; saves are
// conditional on it, so an edit made elsewhere is never overwritten.
const synced = new Map<string, SyncedFile>();

function toNote(id: string, content: string): Note {
  const path = remoteNotePath(id);
  const now = new Date().toISOString();
  return {
    id,
    title: remoteFileTitle(path),
    fileName: path,
    category: "",
    createdAt: now,
    updatedAt: now,
    wordCount: countNoteChars(content),
    content,
  };
}

async function remoteBaseUrl(): Promise<string> {
  const config = await getRemoteConfig();
  if (!config.enabled || !config.baseUrl) throw new Error("远程笔记库未启用。");
  return config.baseUrl;
}

/** Reads the current server version without adopting it as the editing base. */
export async function fetchRemoteFile(id: string): Promise<RemoteDocument & { baseUrl: string }> {
  const baseUrl = synced.get(id)?.baseUrl ?? (await remoteBaseUrl());
  return { ...(await readRemoteFile(baseUrl, remoteNotePath(id))), baseUrl };
}

/** Makes `doc` the version the editor content is based on, and returns it as a note. */
export function adoptRemoteFile(id: string, doc: RemoteDocument & { baseUrl: string }): Note {
  synced.set(id, { baseUrl: doc.baseUrl, revision: doc.revision, content: doc.content });
  return toNote(id, doc.content);
}

export function syncedRemoteContent(id: string): string | undefined {
  return synced.get(id)?.content;
}

async function saveRemoteNote(id: string, content: string): Promise<Note> {
  const state = synced.get(id);
  if (!state) throw new Error("请重新打开远程文件。");
  if (content === state.content) return toNote(id, content);
  try {
    const { revision } = await writeRemoteFile(
      state.baseUrl,
      remoteNotePath(id),
      content,
      state.revision,
    );
    synced.set(id, { ...state, revision, content });
    return toNote(id, content);
  } catch (error) {
    if ((error as { code?: unknown } | null)?.code === "conflict") {
      // The base revision stays unchanged, so later saves keep failing until
      // the editor adopts the server version (see useRemoteNote).
      window.dispatchEvent(new CustomEvent(REMOTE_CONFLICT_EVENT, { detail: id }));
    }
    throw error;
  }
}

/** `getNote` that also opens `remote:` ids. */
export async function getNote(id: string): Promise<Note> {
  if (!isRemoteNoteId(id)) return getLocalNote(id);
  return adoptRemoteFile(id, await fetchRemoteFile(id));
}

/** `updateNote` that also saves `remote:` ids. Remote files keep their name; only content is written. */
export function updateNote(id: string, request: SaveNoteRequest): Promise<Note> {
  return isRemoteNoteId(id) ? saveRemoteNote(id, request.content) : updateLocalNote(id, request);
}
