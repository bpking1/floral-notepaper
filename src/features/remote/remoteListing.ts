import { getRemoteConfig, getServerConfig, listRemoteFiles } from "./api";
import type { RemoteFileEntry, ServerConfig } from "./api";

export interface Listing {
  baseUrl: string;
  /** Newest first. */
  files: RemoteFileEntry[];
  truncated: boolean;
}

// The last listing, shown immediately next time while a fresh one loads.
let cached: Listing | null = null;

export function cachedListing(): Listing | null {
  return cached;
}

/** Drops a deleted file from the cached listing and returns the updated listing. */
export function removeFromListing(path: string): Listing | null {
  if (cached) cached = { ...cached, files: cached.files.filter((file) => file.path !== path) };
  return cached;
}

export async function refreshListing(): Promise<Listing> {
  const { baseUrl } = await getRemoteConfig();
  const { files, truncated } = await listRemoteFiles(baseUrl);
  files.sort((a, b) => b.modified - a.modified);
  cached = { baseUrl, files, truncated };
  return cached;
}

let config: { baseUrl: string; value: ServerConfig } | null = null;

/** The server's capture settings, cached per API address. */
export async function loadServerConfig(): Promise<ServerConfig> {
  const { baseUrl } = await getRemoteConfig();
  if (config?.baseUrl === baseUrl) return config.value;
  let value: ServerConfig;
  try {
    value = await getServerConfig(baseUrl);
  } catch (error) {
    // Servers before /v1/config put titled captures in the library root.
    if ((error as { code?: unknown } | null)?.code !== "notFound") throw error;
    value = { inbox: "Chat.md", newDir: "" };
  }
  config = { baseUrl, value };
  return value;
}
