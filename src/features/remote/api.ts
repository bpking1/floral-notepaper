import { invoke } from "@tauri-apps/api/core";

export interface RemoteConfig {
  enabled: boolean;
  baseUrl: string;
  defaultFile: string;
}
export interface RemoteDocument {
  content: string;
  revision: string;
}
export const emptyRemoteConfig: RemoteConfig = {
  enabled: false,
  baseUrl: "",
  defaultFile: "",
};
export const getRemoteConfig = () => invoke<RemoteConfig>("remote_config_get");
export const saveRemoteConfig = (config: RemoteConfig, token: string) =>
  invoke<RemoteConfig>("remote_config_save", { config, token: token || null });
export const openRemoteWindow = () => invoke<string>("open_remote_window");
export const setRemoteDirty = (dirty: boolean) => invoke<void>("remote_set_dirty", { dirty });
export const listRemoteFiles = (baseUrl: string) =>
  invoke<{ files: string[] }>("remote_request", { baseUrl, action: "list" });
export const readRemoteFile = (baseUrl: string, path: string) =>
  invoke<RemoteDocument>("remote_request", { baseUrl, action: "read", path });
export const writeRemoteFile = (
  baseUrl: string,
  path: string,
  content: string,
  revision: string | null,
) =>
  invoke<{ revision: string }>("remote_request", {
    baseUrl,
    action: "write",
    path,
    content,
    revision,
  });

export interface UploadedRemoteImage {
  path: string;
  markdownPath: string;
}
export const uploadRemoteImage = (baseUrl: string, notePath: string, data: Uint8Array) =>
  invoke<UploadedRemoteImage>("remote_image_upload", data, {
    headers: {
      "x-api-base-url": encodeURIComponent(baseUrl),
      "x-note-path": encodeURIComponent(notePath),
    },
  });
export const readRemoteImage = (baseUrl: string, path: string) =>
  invoke<ArrayBuffer>("remote_image_read", { baseUrl, path });
