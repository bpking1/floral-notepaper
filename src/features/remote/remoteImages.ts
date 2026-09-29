import { useEffect, useMemo, useState } from "react";
import { getRemoteConfig } from "./api";
import { isRemoteNoteId, remoteNotePath } from "./remoteNotes";

const MARKDOWN_IMAGE = /(!\[[^\]]*\]\(\s*)(<[^>\n]*>|[^\s)]+)/g;

/** Resolves a relative image link against the note's directory; null for URLs or links leaving the library. */
export function resolveLibraryPath(notePath: string, link: string): string | null {
  if (/^[a-z][a-z0-9+.-]*:/i.test(link) || link.startsWith("/") || link.startsWith("#"))
    return null;
  let decoded = link;
  try {
    decoded = decodeURI(link);
  } catch {
    // Keep malformed escapes as written.
  }
  const parts = notePath.split("/").slice(0, -1);
  for (const part of decoded.split("/")) {
    if (part === "" || part === ".") continue;
    if (part !== "..") parts.push(part);
    else if (parts.pop() === undefined) return null;
  }
  return parts.length > 0 ? parts.join("/") : null;
}

export function remoteImageUrl(baseUrl: string, path: string): string {
  const url = new URL("v1/image", baseUrl);
  url.searchParams.set("path", path);
  return url.toString();
}

/** Points relative image links at the server so a tile can render them (needs NOTES_PUBLIC_IMAGES). */
export function withRemoteImageUrls(content: string, notePath: string, baseUrl: string): string {
  return content.replace(MARKDOWN_IMAGE, (whole, open: string, target: string) => {
    const link = target.startsWith("<") ? target.slice(1, -1) : target;
    const path = resolveLibraryPath(notePath, link);
    return path ? open + remoteImageUrl(baseUrl, path) : whole;
  });
}

/** Tile content for `noteId`: remote files get server image URLs, local notes are unchanged. */
export function useRemoteTileContent(noteId: string, content: string): string {
  const remote = isRemoteNoteId(noteId);
  const [baseUrl, setBaseUrl] = useState("");

  useEffect(() => {
    if (!remote) return;
    void getRemoteConfig()
      .then((config) => setBaseUrl(config.baseUrl))
      .catch(() => setBaseUrl(""));
  }, [remote]);

  return useMemo(
    () =>
      remote && baseUrl ? withRemoteImageUrls(content, remoteNotePath(noteId), baseUrl) : content,
    [baseUrl, content, noteId, remote],
  );
}
