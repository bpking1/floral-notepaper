import { Component, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { NotepadOpenPanel } from "../../components/NotepadOpenPanel";
import { showToast } from "../../components/Toast";
import { useViewportPopupPosition } from "../../components/popupPosition";
import { getErrorMessage } from "../notes/api";
import { formatShortDate } from "../notes/noteUtils";
import type { NoteMetadata } from "../notes/types";
import { openNotepadWindow } from "../windows/api";
import { deleteRemoteFile } from "./api";
import type { RemoteFileEntry } from "./api";
import { cachedListing, refreshListing, removeFromListing } from "./remoteListing";
import type { Listing } from "./remoteListing";
import { isRemoteNoteId, remoteFileTitle, remoteNoteId } from "./remoteNotes";
import { useRemoteEnabled } from "./useRemoteEnabled";

type Source = "local" | "remote";

const SOURCE_KEY = "floral.openPanel.source";
const MAX_VISIBLE = 200;

function readSource(): Source {
  try {
    return localStorage.getItem(SOURCE_KEY) === "remote" ? "remote" : "local";
  } catch {
    return "local";
  }
}

function shortDate(seconds: number): string {
  return seconds > 0 ? formatShortDate(new Date(seconds * 1000).toISOString()) : "--";
}

/** Every whitespace-separated term must appear in the path, e.g. `日记 09` matches `日记/2026/09-28.md`. */
export function filterRemoteFiles(files: RemoteFileEntry[], query: string): RemoteFileEntry[] {
  const terms = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (terms.length === 0) return files;
  return files.filter((file) => {
    const path = file.path.toLowerCase();
    return terms.every((term) => path.includes(term));
  });
}

interface OpenPanelProps {
  notes: NoteMetadata[];
  onOpenNote: (noteId: string) => void;
}

/** NotepadOpenPanel plus a remote tab when remote notes are enabled. */
export function OpenPanelWithRemote({ notes, onOpenNote }: OpenPanelProps) {
  const { t } = useTranslation();
  const enabled = useRemoteEnabled();
  const [source, setSource] = useState<Source>(readSource);
  // Saving a remote file adds it to the pad's note list; it belongs to the remote tab.
  const localNotes = useMemo(() => notes.filter((note) => !isRemoteNoteId(note.id)), [notes]);

  if (!enabled) return <NotepadOpenPanel notes={localNotes} onOpenNote={onOpenNote} />;

  const choose = (next: Source) => {
    setSource(next);
    try {
      localStorage.setItem(SOURCE_KEY, next);
    } catch {
      // Storage unavailable; the choice just isn't remembered.
    }
  };
  const tabClass = (value: Source) =>
    `px-2.5 py-0.5 text-[11px] rounded-md transition-colors cursor-pointer ${
      source === value
        ? "bg-bamboo-mist/60 text-bamboo font-medium"
        : "text-ink-ghost hover:text-ink-faint"
    }`;

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="flex items-center gap-1 px-3 pt-2 shrink-0">
        <button type="button" className={tabClass("local")} onClick={() => choose("local")}>
          {t("remote.tab.local", { defaultValue: "本地" })}
        </button>
        <button type="button" className={tabClass("remote")} onClick={() => choose("remote")}>
          {t("remote.tab.remote", { defaultValue: "远程" })}
        </button>
      </div>
      {source === "local" ? (
        <NotepadOpenPanel notes={localNotes} onOpenNote={onOpenNote} />
      ) : (
        <RemoteListBoundary>
          <RemoteFileList onOpenNote={onOpenNote} />
        </RemoteListBoundary>
      )}
    </div>
  );
}

// A render error here would otherwise unmount the whole (transparent) note window.
class RemoteListBoundary extends Component<{ children: ReactNode }, { error: string }> {
  state = { error: "" };

  static getDerivedStateFromError(error: unknown) {
    return { error: getErrorMessage(error) };
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <p className="px-4 py-6 text-center text-[12px] text-ink-ghost break-words">
        {this.state.error}
      </p>
    );
  }
}

interface FileMenu {
  x: number;
  y: number;
  path: string;
  confirmDelete: boolean;
}

// Same icon as the local list's "open in editor" button.
function OpenExternalIcon() {
  return (
    <svg
      width="13"
      height="13"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
      <polyline points="15 3 21 3 21 9" />
      <line x1="10" y1="14" x2="21" y2="3" />
    </svg>
  );
}

// Markup and styles mirror NotepadOpenPanel, so both tabs look the same
// without changing the upstream component.
function RemoteFileList({ onOpenNote }: { onOpenNote: (noteId: string) => void }) {
  const { t } = useTranslation();
  const [listing, setListing] = useState<Listing | null>(cachedListing);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [hoveredPath, setHoveredPath] = useState<string | null>(null);
  const [menu, setMenu] = useState<FileMenu | null>(null);
  const { popupRef: menuRef, popupPosition: menuPosition } = useViewportPopupPosition(
    menu,
    menu?.confirmDelete,
  );

  useEffect(() => {
    let cancelled = false;
    void refreshListing()
      .then((next) => {
        if (!cancelled) setListing(next);
      })
      .catch((e) => {
        if (!cancelled) setError(getErrorMessage(e));
      });
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!menu) return undefined;
    const close = () => setMenu(null);
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") close();
    };
    document.addEventListener("mousedown", close);
    document.addEventListener("keydown", onKeyDown);
    window.addEventListener("blur", close);
    return () => {
      document.removeEventListener("mousedown", close);
      document.removeEventListener("keydown", onKeyDown);
      window.removeEventListener("blur", close);
    };
  }, [menu]);

  const openInNewWindow = (path: string) => {
    void openNotepadWindow(remoteNoteId(path)).catch((e) => showToast(getErrorMessage(e)));
  };

  const trash = async (path: string) => {
    setMenu(null);
    try {
      const { baseUrl } = listing ?? (await refreshListing());
      const { trashedTo } = await deleteRemoteFile(baseUrl, path);
      setListing(removeFromListing(path));
      showToast(
        t("remote.deleted", { defaultValue: "已移到服务器回收站：{{path}}", path: trashedTo }),
        "info",
      );
    } catch (e) {
      showToast(getErrorMessage(e));
    }
  };

  const files = listing?.files;
  const matches = useMemo(() => filterRemoteFiles(files ?? [], query), [files, query]);
  const message =
    error ||
    (!files
      ? t("remote.loading", { defaultValue: "正在加载远程文件…" })
      : files.length === 0
        ? t("remote.empty", { defaultValue: "服务器上还没有 Markdown 笔记" })
        : matches.length === 0
          ? t("notepad.search.noResults", { defaultValue: "没有匹配的笔记" })
          : matches.length > MAX_VISIBLE
            ? t("remote.moreResults", {
                defaultValue: "还有 {{count}} 个文件，输入关键词缩小范围",
                count: matches.length - MAX_VISIBLE,
              })
            : listing?.truncated
              ? t("remote.truncated", {
                  defaultValue: "文件过多，只列出最近修改的 {{count}} 个",
                  count: files.length,
                })
              : "");

  return (
    <div className="flex flex-col flex-1 min-h-0">
      <div className="px-3 pt-2 pb-1.5 shrink-0">
        <div className="flex items-center gap-2 px-2.5 py-1.5 rounded-lg bg-paper-warm/60 border border-paper-deep/30">
          <svg
            width="13"
            height="13"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="2.5"
            strokeLinecap="round"
            className="text-ink-ghost shrink-0"
          >
            <circle cx="11" cy="11" r="8" />
            <path d="m21 21-4.35-4.35" />
          </svg>
          <input
            type="text"
            value={query}
            autoFocus
            onChange={(event) => setQuery(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && matches[0]) onOpenNote(remoteNoteId(matches[0].path));
            }}
            placeholder={t("remote.search.placeholder", { defaultValue: "搜索路径，如：日记 09" })}
            className="flex-1 text-[12px] font-body text-ink placeholder:text-ink-ghost/60 bg-transparent"
          />
          {query && (
            <button
              type="button"
              onClick={() => setQuery("")}
              className="text-ink-ghost hover:text-ink-faint transition-colors cursor-pointer"
              title={t("notepad.search.clear", { defaultValue: "清空搜索" })}
            >
              <svg
                width="10"
                height="10"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="3"
                strokeLinecap="round"
              >
                <path d="M18 6L6 18M6 6l12 12" />
              </svg>
            </button>
          )}
        </div>
      </div>

      <div className="p-2 pt-0 flex-1 min-h-0 overflow-y-auto">
        <div className="space-y-0.5">
          {matches.slice(0, MAX_VISIBLE).map((file) => {
            const slash = file.path.lastIndexOf("/");
            return (
              <button
                key={file.path}
                type="button"
                onClick={() => onOpenNote(remoteNoteId(file.path))}
                onContextMenu={(event) => {
                  event.preventDefault();
                  setMenu({
                    x: event.clientX,
                    y: event.clientY,
                    path: file.path,
                    confirmDelete: false,
                  });
                }}
                onMouseEnter={() => setHoveredPath(file.path)}
                onMouseLeave={() => setHoveredPath(null)}
                title={file.path}
                className="w-full text-left px-3.5 py-3 rounded-xl transition-all duration-200 cursor-pointer group hover:bg-paper-warm/70"
              >
                <div className="flex items-center justify-between mb-0.5">
                  <span className="text-[13px] font-display font-medium text-ink-soft group-hover:text-ink transition-colors truncate pr-2">
                    {remoteFileTitle(file.path)}
                  </span>
                  <div className="flex items-center gap-1.5 shrink-0">
                    <span
                      role="button"
                      tabIndex={-1}
                      onClick={(event) => {
                        event.stopPropagation();
                        openInNewWindow(file.path);
                      }}
                      className="w-6 h-6 flex items-center justify-center rounded-md text-ink-ghost hover:text-bamboo hover:bg-bamboo-mist/50 transition-all duration-200 opacity-0 group-hover:opacity-100 cursor-pointer"
                      title={t("remote.openInNewWindow", { defaultValue: "在新窗口中打开" })}
                    >
                      <OpenExternalIcon />
                    </span>
                    <span className="text-[11px] text-ink-ghost font-mono tabular-nums">
                      {shortDate(file.modified)}
                    </span>
                  </div>
                </div>
                <p className="text-[12px] text-ink-ghost leading-relaxed line-clamp-1 group-hover:text-ink-faint transition-colors">
                  {slash > 0 ? file.path.slice(0, slash) : "/"}
                </p>
                {hoveredPath === file.path && (
                  <div className="mt-1.5 h-px bg-bamboo/10 transition-all duration-300" />
                )}
              </button>
            );
          })}
          {message && (
            <div className="px-4 py-8 text-center text-[12px] text-ink-ghost break-words">
              {message}
            </div>
          )}
        </div>
      </div>

      {menu && (
        // Styled like the app's context menu (components/ContextMenu).
        <div
          ref={menuRef}
          className="fixed z-[9999] min-w-[152px] py-1.5 bg-cloud/95 backdrop-blur-sm border border-paper-deep/50 rounded-lg overflow-hidden select-none animate-menu-enter"
          style={{ left: menuPosition?.x ?? menu.x, top: menuPosition?.y ?? menu.y }}
          onMouseDown={(event) => event.stopPropagation()}
        >
          <button
            type="button"
            onClick={() => {
              setMenu(null);
              openInNewWindow(menu.path);
            }}
            className="w-full flex items-center px-3 py-1.5 text-[12px] font-body transition-colors cursor-pointer text-ink-soft hover:bg-bamboo-mist/60 hover:text-bamboo"
          >
            {t("remote.openInNewWindow", { defaultValue: "在新窗口中打开" })}
          </button>
          <div className="mx-2 my-1 h-px bg-paper-deep/40" />
          <button
            type="button"
            onClick={() =>
              menu.confirmDelete ? void trash(menu.path) : setMenu({ ...menu, confirmDelete: true })
            }
            className="w-full flex items-center px-3 py-1.5 text-[12px] font-body transition-colors cursor-pointer text-red-400 hover:bg-danger-bg hover:text-red-500"
          >
            {menu.confirmDelete
              ? t("remote.confirmDelete", { defaultValue: "确认删除（移到回收站）" })
              : t("remote.delete", { defaultValue: "删除" })}
          </button>
        </div>
      )}
    </div>
  );
}
