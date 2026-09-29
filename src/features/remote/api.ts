import { invoke } from "@tauri-apps/api/core";

export interface RemoteConfig {
  enabled: boolean;
  baseUrl: string;
}
export interface AppendResult {
  path: string;
  revision: string;
}
export const emptyRemoteConfig: RemoteConfig = {
  enabled: false,
  baseUrl: "",
};
export const getRemoteConfig = () => invoke<RemoteConfig>("remote_config_get");
export const saveRemoteConfig = (config: RemoteConfig, token: string) =>
  invoke<RemoteConfig>("remote_config_save", { config, token: token || null });
export interface RemoteFileEntry {
  path: string;
  /** Modification time in Unix seconds. */
  modified: number;
}
export interface RemoteDocument {
  content: string;
  revision: string;
}
export interface RemoteListing {
  files: RemoteFileEntry[];
  truncated: boolean;
}

// Older servers return plain paths or RFC 3339 times; a malformed entry must not break the list.
function toFileEntry(item: unknown): RemoteFileEntry | null {
  if (typeof item === "string") return { path: item, modified: 0 };
  if (!item || typeof item !== "object") return null;
  const { path, modified } = item as { path?: unknown; modified?: unknown };
  if (typeof path !== "string" || !path) return null;
  const seconds =
    typeof modified === "number"
      ? modified
      : typeof modified === "string"
        ? Date.parse(modified) / 1000
        : 0;
  return { path, modified: Number.isFinite(seconds) ? seconds : 0 };
}

export function parseRemoteListing(raw: unknown): RemoteListing {
  const { files, truncated } = (raw ?? {}) as { files?: unknown; truncated?: unknown };
  return {
    files: Array.isArray(files)
      ? files.map(toFileEntry).filter((entry): entry is RemoteFileEntry => entry !== null)
      : [],
    truncated: truncated === true,
  };
}

export const listRemoteFiles = async (baseUrl: string): Promise<RemoteListing> =>
  parseRemoteListing(await invoke<unknown>("remote_request", { baseUrl, action: "list" }));
export const readRemoteFile = (baseUrl: string, path: string) =>
  invoke<RemoteDocument>("remote_request", { baseUrl, action: "read", path });
export interface ServerConfig {
  inbox: string;
  /** Directory for titled captures without a leading `/`; empty is the library root. */
  newDir: string;
}
export const getServerConfig = (baseUrl: string) =>
  invoke<ServerConfig>("remote_request", { baseUrl, action: "config" });
/** Moves the file to the server's `.trash/` directory, from where it can be restored. */
export const deleteRemoteFile = (baseUrl: string, path: string) =>
  invoke<{ trashedTo: string }>("remote_request", { baseUrl, action: "delete", path });
/** Overwrites the file only if it is still at `revision`; otherwise fails with code `conflict`. */
export const writeRemoteFile = (baseUrl: string, path: string, content: string, revision: string) =>
  invoke<{ revision: string }>("remote_request", {
    baseUrl,
    action: "write",
    path,
    content,
    revision,
  });
export interface UploadedRemoteImage {
  /** Path in the notes library, e.g. `attachments/20260928-143205.png`. */
  path: string;
  /** Link relative to the note it was uploaded for. */
  markdownPath: string;
}
/** `name` is an optional file name hint; the server falls back to the upload time. */
export const uploadRemoteImage = (notePath: string, data: Uint8Array, name = "") =>
  invoke<UploadedRemoteImage>("remote_image_upload", data, {
    headers: {
      "x-note-path": encodeURIComponent(notePath),
      "x-image-name": encodeURIComponent(name),
    },
  });
/** Uploads a local note image (`images/<noteId>/<file>`) linked from the next inbox entry. */
export const uploadInboxImage = (imagePath: string, notePath?: string) =>
  invoke<UploadedRemoteImage>("remote_inbox_image_upload", {
    imagePath,
    notePath: notePath ?? null,
  });
/** `id` must stay the same when retrying the same content, so the server can de-duplicate. */
/** Without `path` the capture goes to the inbox; with it, that note is created or extended. */
export const appendToInbox = (id: string, title: string, text: string, path?: string) =>
  invoke<AppendResult>("remote_append", { id, title, text, path: path ?? null });
