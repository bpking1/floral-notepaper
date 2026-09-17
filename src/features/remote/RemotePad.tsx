import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { listen } from "@tauri-apps/api/event";
import { getErrorMessage } from "../notes/api";
import { countNoteChars } from "../notes/noteUtils";
import { getConfig } from "../settings/api";
import {
  DEFAULT_TILE_COLOR,
  normalizeTileColor,
  resolveTileColor,
} from "../settings/tileColor";
import type { TileColorMode } from "../settings/types";
import { reportInstallPreparation } from "../update/api";
import type { UpdateInstallPrepareRequest } from "../update/types";
import { Tile } from "../../components/Tile";
import { showToast } from "../../components/Toast";
import {
  animateCurrentWindowBounds,
  getCurrentWindowBounds,
  hideCurrentWindow,
  setCurrentWindowAlwaysOnTop,
  showCurrentWindow,
  startCurrentWindowDrag,
  startCurrentWindowDragWithOffset,
  startCurrentWindowResize,
} from "../windows/controls";
import type { ResizeDirection } from "../windows/controls";
import {
  getSurfaceTargetBounds,
  NOTE_SURFACE_MODE_EVENT,
  surfaceModeFromEvent,
} from "../windows/surfaceMode";
import type { NoteSurfaceMode } from "../windows/surfaceMode";
import { NOTE_SURFACE_ACTION_EVENT, surfaceActionFromEvent } from "../windows/surfaceActions";
import {
  shouldEnterPadFromTileOnDoubleClick,
  shouldReturnToTileAfterManualSave,
} from "../windows/noteSurfaceSavePolicy";
import {
  getRemoteConfig,
  listRemoteFiles,
  readRemoteFile,
  setRemoteDirty,
  uploadRemoteImage,
  writeRemoteFile,
} from "./api";
import type { RemoteConfig, RemoteDocument } from "./api";
import { RemoteImage } from "./RemoteImage";
import { insertImageLinks, validateImageUpload } from "./imageUpload";
import { buildRemoteNote, parseRemoteNote } from "./noteTitle";
import { blankDraft, isDirty, mergeBase } from "./session";
import type { RemoteSession } from "./session";
import { candidateFileNames } from "./remoteFileName";
import { RemoteOpenPanel } from "./RemoteOpenPanel";

type OpenMode = "new" | "open";
type RemoteStatus = "empty" | "opened" | "saved" | "dirty" | "saveFailed" | "copied";

const TILE_DRAG_START_THRESHOLD_PX = 5;

const surfaceResizeHandles: Array<{
  direction: ResizeDirection;
  size: string;
  className: string;
}> = [
  { direction: "NorthWest", size: "w-8 h-8", className: "top-0 left-0 cursor-nwse-resize" },
  { direction: "NorthEast", size: "w-5 h-5", className: "top-0 right-0 cursor-nesw-resize" },
  { direction: "SouthWest", size: "w-8 h-8", className: "bottom-0 left-0 cursor-nesw-resize" },
  { direction: "SouthEast", size: "w-5 h-5", className: "bottom-0 right-0 cursor-nwse-resize" },
];

function isTileControlDoubleClickTarget(target: EventTarget | null): boolean {
  return (
    target instanceof HTMLElement &&
    Boolean(target.closest('button,input,textarea,select,a,[data-surface-resize-handle="true"]'))
  );
}

function SurfaceResizeHandles() {
  return (
    <>
      {surfaceResizeHandles.map((handle) => (
        <div
          key={handle.direction}
          aria-hidden="true"
          data-surface-resize-handle="true"
          data-resize-direction={handle.direction}
          onMouseDown={(event) => {
            event.stopPropagation();
            void startCurrentWindowResize(handle.direction).catch(() => undefined);
          }}
          className={`absolute ${handle.size} opacity-0 ${handle.className}`}
        />
      ))}
    </>
  );
}

function getImageFiles(dataTransfer: DataTransfer): File[] {
  const files: File[] = [];
  for (let i = 0; i < dataTransfer.items.length; i++) {
    const item = dataTransfer.items[i];
    if (item.kind === "file" && item.type.startsWith("image/")) {
      const file = item.getAsFile();
      if (file) files.push(file);
    }
  }
  return files;
}

function errorCode(error: unknown): string | undefined {
  return (error as { code?: string })?.code;
}

export function RemotePad() {
  const { t } = useTranslation();
  const [surfaceMode, setSurfaceMode] = useState<NoteSurfaceMode>("pad");
  const [mode, setMode] = useState<OpenMode>("new");
  const [files, setFiles] = useState<string[]>([]);
  const [doc, setDoc] = useState<RemoteSession>(() => blankDraft());
  const [title, setTitle] = useState("");
  const [titleBaseline, setTitleBaseline] = useState("");
  const [latest, setLatest] = useState<RemoteDocument | null>(null);
  const [status, setStatus] = useState<RemoteStatus>("empty");
  const [noteSurfaceAutoSave, setNoteSurfaceAutoSave] = useState(true);
  const [tileColorRaw, setTileColorRaw] = useState(normalizeTileColor(DEFAULT_TILE_COLOR));
  const [tileColorMode, setTileColorMode] = useState<TileColorMode>("system");
  const [surfaceFontSize, setSurfaceFontSize] = useState(14);
  const [tileRenderMarkdown, setTileRenderMarkdown] = useState(false);
  const [tileDoubleClickToEdit, setTileDoubleClickToEdit] = useState(false);
  const [tileSaveReturnsToPin, setTileSaveReturnsToPin] = useState(false);
  const [tileColor, setTileColor] = useState(() =>
    resolveTileColor("system", normalizeTileColor(DEFAULT_TILE_COLOR)),
  );
  const [isExiting, setIsExiting] = useState(false);
  const titleRef = useRef<HTMLInputElement>(null);
  const contentRef = useRef<HTMLTextAreaElement>(null);
  const tileDragIntentRef = useRef<{ x: number; y: number } | null>(null);
  const hadHeadingRef = useRef(false);
  const busyRef = useRef(false);
  const docRef = useRef(doc);
  docRef.current = doc;
  const titleValueRef = useRef(title);
  titleValueRef.current = title;
  const titleBaselineRef = useRef(titleBaseline);
  titleBaselineRef.current = titleBaseline;
  const configRef = useRef<RemoteConfig | null>(null);
  const dirtyReportedRef = useRef(false);
  const hasEnteredOnce = useRef(false);

  const isDraftDirty = useCallback(
    () => isDirty(docRef.current) || titleValueRef.current !== titleBaselineRef.current,
    [],
  );

  // 空白便笺不算待保存内容，避免每次唤起都生成空文件
  const pendingChanges = useCallback(
    () => isDraftDirty() && hasDraftContentRef.current(),
    [isDraftDirty],
  );

  const statusLabel = useMemo<Record<RemoteStatus, string>>(
    () => ({
      empty: t("notepad.status.empty", { defaultValue: "空" }),
      opened: t("notepad.status.opened", { defaultValue: "已打开" }),
      saved: t("notepad.status.saved", { defaultValue: "已保存" }),
      dirty: t("notepad.status.unsaved", { defaultValue: "未保存" }),
      saveFailed: t("notepad.status.saveFailed", { defaultValue: "保存失败" }),
      copied: t("notepad.status.copied", { defaultValue: "已复制" }),
    }),
    [t],
  );
  const tabLabels = useMemo(
    () => ({
      new: t("notepad.tab.new", { defaultValue: "新建" }),
      edit: t("notepad.tab.edit", { defaultValue: "编辑" }),
      open: t("notepad.tab.open", { defaultValue: "打开" }),
    }),
    [t],
  );

  function changeDoc(next: RemoteSession) {
    docRef.current = next;
    setDoc(next);
    const nextDirty = isDirty(next) || titleValueRef.current !== titleBaselineRef.current;
    if (nextDirty !== dirtyReportedRef.current) {
      dirtyReportedRef.current = nextDirty;
      void setRemoteDirty(nextDirty).catch(() => undefined);
    }
  }

  function applyDraft(next: RemoteSession, nextTitle: string, hadHeading: boolean) {
    hadHeadingRef.current = hadHeading;
    titleValueRef.current = nextTitle;
    titleBaselineRef.current = nextTitle;
    setTitleBaseline(nextTitle);
    setTitle(nextTitle);
    changeDoc(next);
  }

  const hasDraftContent = useCallback(() => {
    return Boolean(
      docRef.current?.path || titleValueRef.current.trim() || (docRef.current?.content ?? "").trim(),
    );
  }, []);
  const hasDraftContentRef = useRef(hasDraftContent);
  hasDraftContentRef.current = hasDraftContent;

  const refreshFiles = useCallback(async () => {
    const connection = configRef.current;
    if (!connection?.baseUrl) return;
    try {
      const { files: found } = await listRemoteFiles(connection.baseUrl);
      setFiles(found);
    } catch (error) {
      showToast(getErrorMessage(error));
    }
  }, []);

  const showConflict = useCallback(
    async (path: string) => {
      setStatus("saveFailed");
      try {
        setLatest(await readRemoteFile(configRef.current?.baseUrl ?? "", path));
        showToast(
          t("remote.conflictDetected", { defaultValue: "服务器版本已变化，请在下方合并后保存。" }),
        );
      } catch {
        showToast(t("remote.saveFailed", { defaultValue: "保存远程笔记失败。" }));
      }
    },
    [t],
  );

  const save = useCallback(async (): Promise<boolean> => {
    const current = docRef.current;
    const connection = configRef.current;
    if (!current || !connection?.baseUrl || !isDraftDirty()) return false;
    if (!current.path && !hasDraftContentRef.current()) return false;
    if (busyRef.current) return false;
    busyRef.current = true;
    const submittedTitle = titleValueRef.current;
    try {
      const full = buildRemoteNote({
        title: submittedTitle,
        body: current.content,
        hadHeading: hadHeadingRef.current,
      });
      let usedPath = current.path ?? "";
      let revision: string | null = null;
      if (current.revision) {
        const result = await writeRemoteFile(
          connection.baseUrl,
          usedPath,
          full,
          current.revision,
        );
        revision = result.revision;
      } else {
        const names = [
          ...(current.path ? [current.path] : []),
          ...candidateFileNames(submittedTitle).filter((name) => name !== current.path),
        ];
        let conflictPath = names[0];
        for (const name of names) {
          try {
            const result = await writeRemoteFile(connection.baseUrl, name, full, null);
            usedPath = name;
            revision = result.revision;
            break;
          } catch (error) {
            if (errorCode(error) === "conflict") continue;
            throw error;
          }
        }
        if (!revision) {
          await showConflict(conflictPath);
          return false;
        }
      }
      const savedParts = parseRemoteNote(full);
      const live = docRef.current;
      const contentTypedDuringSave = live !== null && live.content !== current.content;
      changeDoc({
        path: usedPath,
        content: contentTypedDuringSave ? live.content : savedParts.body,
        baseline: savedParts.body,
        revision,
      });
      hadHeadingRef.current = savedParts.hadHeading;
      if (titleValueRef.current === submittedTitle) {
        titleBaselineRef.current = submittedTitle;
        setTitleBaseline(submittedTitle);
      }
      setLatest(null);
      setStatus(contentTypedDuringSave ? "dirty" : "saved");
      setFiles((items) => (items.includes(usedPath) ? items : [...items, usedPath].sort()));
      return true;
    } catch (error) {
      if (errorCode(error) === "conflict" && current.path) {
        await showConflict(current.path);
      } else {
        setStatus("saveFailed");
        showToast(getErrorMessage(error));
      }
      return false;
    } finally {
      busyRef.current = false;
    }
  }, [isDraftDirty, showConflict, t]);

  const saveRef = useRef(save);
  saveRef.current = save;

  const switchSurfaceMode = useCallback(async (nextMode: NoteSurfaceMode) => {
    setSurfaceMode(nextMode);
    try {
      const currentBounds = await getCurrentWindowBounds();
      const targetBounds = getSurfaceTargetBounds(nextMode, currentBounds);
      if (nextMode === "tile") {
        await setCurrentWindowAlwaysOnTop(true);
      }
      await animateCurrentWindowBounds(targetBounds);
    } catch (error) {
      showToast(getErrorMessage(error));
    }
  }, []);

  const switchSurfaceModeRef = useRef(switchSurfaceMode);
  switchSurfaceModeRef.current = switchSurfaceMode;

  const handleSave = useCallback(
    async ({ isAutoSave = false }: { isAutoSave?: boolean } = {}) => {
      const saved = await saveRef.current();
      if (
        saved &&
        shouldReturnToTileAfterManualSave({
          enabled: tileSaveReturnsToPin,
          noteId: docRef.current?.path ?? "",
          currentMode: surfaceMode,
          isAutoSave,
        })
      ) {
        await switchSurfaceModeRef.current("tile");
      }
      return saved;
    },
    [surfaceMode, tileSaveReturnsToPin],
  );

  const handleSaveRef = useRef(handleSave);
  handleSaveRef.current = handleSave;

  const resetDraft = useCallback(() => {
    applyDraft(blankDraft(), "", false);
    setLatest(null);
    setStatus("empty");
    setMode("new");
  }, []);

  const openFile = useCallback(async (path: string) => {
    const connection = configRef.current;
    if (!connection?.baseUrl || busyRef.current) return;
    busyRef.current = true;
    try {
      const document = await readRemoteFile(connection.baseUrl, path);
      const parts = parseRemoteNote(document.content);
      applyDraft(
        { path, content: parts.body, baseline: parts.body, revision: document.revision },
        parts.title,
        parts.hadHeading,
      );
      setLatest(null);
      setMode("new");
      setStatus("opened");
    } catch (error) {
      showToast(getErrorMessage(error));
    } finally {
      busyRef.current = false;
    }
  }, []);

  const handleOpenNote = useCallback(
    async (path: string) => {
      if (pendingChanges()) {
        const saved = await saveRef.current();
        if (!saved) return;
      }
      await openFile(path);
      await switchSurfaceModeRef.current("pad");
    },
    [openFile, pendingChanges],
  );

  const ensureDraftPath = useCallback((): string | null => {
    const current = docRef.current;
    if (!current) return null;
    if (current.path) return current.path;
    const path = candidateFileNames(titleValueRef.current)[0];
    changeDoc({ ...current, path });
    return path;
  }, []);

  const insertImages = useCallback(
    async (selected: File[]) => {
      const connection = configRef.current;
      const textarea = contentRef.current;
      if (!docRef.current || !connection?.baseUrl || !textarea || busyRef.current) return;
      const notePath = ensureDraftPath();
      if (!notePath) return;
      let cursor = textarea.selectionStart;
      let selectionEnd = textarea.selectionEnd;
      let updated = docRef.current;
      let completed = 0;
      try {
        selected.forEach(validateImageUpload);
        for (const file of selected) {
          const result = await uploadRemoteImage(
            connection.baseUrl,
            notePath,
            new Uint8Array(await file.arrayBuffer()),
          );
          const inserted = insertImageLinks(updated.content, cursor, selectionEnd, [
            result.markdownPath,
          ]);
          updated = { ...updated, content: inserted.content };
          cursor = selectionEnd = inserted.caret;
          changeDoc(updated);
          setStatus("dirty");
          completed++;
        }
      } catch (error) {
        showToast(
          completed > 0
            ? `${t("remote.image.partialUpload", {
                defaultValue: "已插入 {count} 张图片，其余上传失败。",
                count: completed,
              })}${getErrorMessage(error)}`
            : `${t("remote.image.uploadFailed", { defaultValue: "图片上传失败。" })}${getErrorMessage(error)}`,
        );
      }
      requestAnimationFrame(() => {
        const editor = contentRef.current;
        if (editor && docRef.current) {
          editor.focus();
          editor.setSelectionRange(cursor, selectionEnd);
        }
      });
    },
    [ensureDraftPath, t],
  );

  const handlePin = useCallback(async () => {
    if (pendingChanges()) {
      const saved = await saveRef.current();
      if (!saved) return;
    }
    await switchSurfaceModeRef.current("tile");
  }, [pendingChanges]);

  const handleClose = useCallback(() => {
    setIsExiting(true);
    void hideCurrentWindow()
      .catch((error) => {
        setIsExiting(false);
        showToast(getErrorMessage(error));
      })
      .finally(() => {
        window.setTimeout(() => setIsExiting(false), 240);
      });
  }, []);

  const handleCloseRef = useRef(handleClose);
  handleCloseRef.current = handleClose;

  const copyTileContent = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(docRef.current?.content ?? "");
      setStatus("copied");
    } catch (error) {
      showToast(getErrorMessage(error));
    }
  }, []);

  const copyTileContentRef = useRef(copyTileContent);
  copyTileContentRef.current = copyTileContent;

  useEffect(() => {
    function handleSurfaceActionRequest(event: Event) {
      const action = surfaceActionFromEvent(event);
      if (!action) return;

      if (action === "copy") {
        void copyTileContentRef.current();
        return;
      }

      if (action === "save") {
        void handleSaveRef.current();
        return;
      }

      if (action === "close") {
        handleCloseRef.current();
        return;
      }

      void switchSurfaceModeRef.current("pad");
    }

    window.addEventListener(NOTE_SURFACE_ACTION_EVENT, handleSurfaceActionRequest);
    return () => {
      window.removeEventListener(NOTE_SURFACE_ACTION_EVENT, handleSurfaceActionRequest);
    };
  }, []);

  useEffect(() => {
    function handleSurfaceModeRequest(event: Event) {
      const nextMode = surfaceModeFromEvent(event);
      if (!nextMode) return;
      void switchSurfaceModeRef.current(nextMode);
    }

    window.addEventListener(NOTE_SURFACE_MODE_EVENT, handleSurfaceModeRequest);
    return () => {
      window.removeEventListener(NOTE_SURFACE_MODE_EVENT, handleSurfaceModeRequest);
    };
  }, []);

  useEffect(() => {
    if (surfaceMode !== "tile") return;
    void setCurrentWindowAlwaysOnTop(true).catch(() => undefined);
  }, [surfaceMode]);

  useEffect(() => {
    let cancelled = false;

    async function bootstrap() {
      try {
        const [appConfig, remote] = await Promise.all([getConfig(), getRemoteConfig()]);
        if (cancelled) return;
        setNoteSurfaceAutoSave(appConfig.noteSurfaceAutoSave);
        setSurfaceFontSize(appConfig.surfaceFontSize ?? 14);
        setTileRenderMarkdown(appConfig.tileRenderMarkdown ?? false);
        setTileDoubleClickToEdit(appConfig.tileDoubleClickToEdit ?? false);
        setTileSaveReturnsToPin(appConfig.tileSaveReturnsToPin ?? false);
        setTileColorRaw(normalizeTileColor(appConfig.tileColor));
        setTileColorMode(appConfig.tileColorMode ?? "system");
        setTileColor(resolveTileColor(appConfig.tileColorMode ?? "system", appConfig.tileColor));
        configRef.current = remote;
        if (!remote.baseUrl) {
          showToast(
            t("remote.notice.unconfigured", {
              defaultValue: "请先在应用设置中配置远程笔记 API。",
            }),
          );
          return;
        }
        const { files: found } = await listRemoteFiles(remote.baseUrl);
        if (!cancelled) setFiles(found);
      } catch (error) {
        showToast(getErrorMessage(error));
      }
    }

    void bootstrap();
    return () => {
      cancelled = true;
    };
  }, [t]);

  useEffect(() => {
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        hasEnteredOnce.current = true;
        void showCurrentWindow()
          .then(() => contentRef.current?.focus())
          .catch(() => undefined);
      });
    });
  }, []);

  useEffect(() => {
    const unlisten = listen("remote:activate", () => {
      void (async () => {
        if (pendingChanges()) {
          const saved = await saveRef.current();
          if (!saved) return;
        }
        setSurfaceMode("pad");
        resetDraft();
        void refreshFiles();
      })();
    });
    return () => {
      void unlisten.then((fn) => fn());
    };
  }, [pendingChanges, refreshFiles, resetDraft]);

  useEffect(() => {
    const unlisten = listen<UpdateInstallPrepareRequest>(
      "update://prepare-install",
      (event) => {
        const respond = async () => {
          const windowLabel = getCurrentWindow().label;
          if (!pendingChanges()) {
            await reportInstallPreparation(event.payload.requestId, windowLabel, "ready");
            return;
          }
          const saved = await saveRef.current();
          if (saved) {
            await reportInstallPreparation(event.payload.requestId, windowLabel, "ready");
          } else {
            await reportInstallPreparation(
              event.payload.requestId,
              windowLabel,
              "failed",
              t("remote.notice.busySaving", {
                defaultValue: "远程笔记保存失败或正在同步，请稍后重试。",
              }),
            );
          }
        };

        void respond().catch(() => undefined);
      },
    );
    return () => {
      void unlisten.then((fn) => fn());
    };
  }, [pendingChanges, t]);

  useEffect(() => {
    const exit = listen("remote-exit-blocked", () => {
      showToast(
        t("remote.notice.exitBlocked", {
          defaultValue: "还有未保存的远程草稿。请保存后再退出应用。",
        }),
      );
    });
    return () => {
      void exit.then((fn) => fn());
    };
  }, [t]);

  useEffect(() => {
    const focus = getCurrentWindow().onFocusChanged(({ payload }) => {
      if (!payload) return;
      const connection = configRef.current;
      if (!connection?.baseUrl) return;
      void (async () => {
        try {
          const { files: found } = await listRemoteFiles(connection.baseUrl);
          setFiles(found);
          const current = docRef.current;
          if (current?.path && !isDraftDirty()) {
            const fresh = await readRemoteFile(connection.baseUrl, current.path);
            if (docRef.current === current) {
              const parts = parseRemoteNote(fresh.content);
              applyDraft(
                {
                  path: current.path,
                  content: parts.body,
                  baseline: parts.body,
                  revision: fresh.revision,
                },
                parts.title,
                parts.hadHeading,
              );
            }
          }
        } catch {
          // 聚焦时后台刷新失败不打扰输入
        }
      })();
    });
    return () => {
      void focus.then((off) => off());
    };
  }, [isDraftDirty]);

  useEffect(() => {
    const unlisten = listen<{
      tileColor?: string;
      tileColorMode?: TileColorMode;
      surfaceFontSize?: number;
      tileRenderMarkdown?: boolean;
      tileDoubleClickToEdit?: boolean;
      tileSaveReturnsToPin?: boolean;
      noteSurfaceAutoSave?: boolean;
    }>("config-changed", (event) => {
      const mode = event.payload.tileColorMode ?? tileColorMode;
      const raw = event.payload.tileColor ?? tileColorRaw;
      setTileColorMode(mode);
      setTileColorRaw(normalizeTileColor(raw));
      setTileColor(resolveTileColor(mode, raw));
      if (event.payload.surfaceFontSize != null) setSurfaceFontSize(event.payload.surfaceFontSize);
      if (event.payload.tileRenderMarkdown != null)
        setTileRenderMarkdown(event.payload.tileRenderMarkdown);
      if (event.payload.tileDoubleClickToEdit != null)
        setTileDoubleClickToEdit(event.payload.tileDoubleClickToEdit);
      if (event.payload.tileSaveReturnsToPin != null)
        setTileSaveReturnsToPin(event.payload.tileSaveReturnsToPin);
      if (event.payload.noteSurfaceAutoSave != null)
        setNoteSurfaceAutoSave(event.payload.noteSurfaceAutoSave);
    });
    return () => {
      void unlisten.then((fn) => fn());
    };
  }, []);

  useEffect(() => {
    if (tileColorMode !== "system") return;
    const observer = new MutationObserver(() => {
      setTileColor(resolveTileColor("system", tileColorRaw));
    });
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["data-theme"],
    });
    return () => observer.disconnect();
  }, [tileColorMode, tileColorRaw]);

  useEffect(() => {
    if (mode !== "open") return;
    void refreshFiles();
  }, [mode, refreshFiles]);

  useEffect(() => {
    if (!noteSurfaceAutoSave || mode !== "new" || status !== "dirty") {
      return undefined;
    }
    if (!hasDraftContentRef.current()) return undefined;

    const timer = window.setTimeout(() => {
      void handleSaveRef.current({ isAutoSave: true });
    }, 900);

    return () => window.clearTimeout(timer);
    // doc/title 作为依赖：持续输入时像速记一样不断重置防抖计时
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [doc, title, mode, noteSurfaceAutoSave, status]);

  const clearPendingTileDrag = useCallback(() => {
    tileDragIntentRef.current = null;
  }, []);

  useEffect(() => () => clearPendingTileDrag(), [clearPendingTileDrag]);

  useEffect(() => {
    if (surfaceMode !== "tile" || !tileDoubleClickToEdit) {
      clearPendingTileDrag();
      return undefined;
    }

    const handleMouseMove = (event: globalThis.MouseEvent) => {
      const intent = tileDragIntentRef.current;
      if (!intent) return;
      if ((event.buttons & 1) === 0) {
        clearPendingTileDrag();
        return;
      }

      const distanceX = event.screenX - intent.x;
      const distanceY = event.screenY - intent.y;
      const distance = Math.hypot(distanceX, distanceY);
      if (distance < TILE_DRAG_START_THRESHOLD_PX) return;

      tileDragIntentRef.current = null;
      void startCurrentWindowDragWithOffset(distanceX, distanceY).catch(() => undefined);
    };

    const handleMouseUp = () => clearPendingTileDrag();

    window.addEventListener("mousemove", handleMouseMove);
    window.addEventListener("mouseup", handleMouseUp);
    return () => {
      window.removeEventListener("mousemove", handleMouseMove);
      window.removeEventListener("mouseup", handleMouseUp);
    };
  }, [clearPendingTileDrag, surfaceMode, tileDoubleClickToEdit]);

  const handleTileDoubleClick = useCallback(
    (event: MouseEvent<HTMLElement>) => {
      if (
        !shouldEnterPadFromTileOnDoubleClick(
          tileDoubleClickToEdit,
          isTileControlDoubleClickTarget(event.target),
        )
      ) {
        return;
      }

      clearPendingTileDrag();
      event.preventDefault();
      event.stopPropagation();
      void switchSurfaceModeRef.current("pad");
    },
    [clearPendingTileDrag, tileDoubleClickToEdit],
  );

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key === "s") {
        event.preventDefault();
        void handleSaveRef.current();
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, []);

  const handleDrag = (event: MouseEvent<HTMLElement>) => {
    const target = event.target as HTMLElement;
    if (target.closest("button,input,textarea")) return;

    if (surfaceMode === "tile" && tileDoubleClickToEdit) {
      if (event.button !== 0 || event.detail > 1) return;
      clearPendingTileDrag();
      tileDragIntentRef.current = {
        x: event.screenX,
        y: event.screenY,
      };
      return;
    }

    void startCurrentWindowDrag().catch(() => undefined);
  };

  const adoptServerBase = useCallback(() => {
    const current = docRef.current;
    if (!current?.path || !latest) return;
    const parts = parseRemoteNote(latest.content);
    applyDraft(
      mergeBase(current, { content: parts.body, revision: latest.revision }),
      parts.title,
      parts.hadHeading,
    );
    setLatest(null);
    setStatus("dirty");
  }, [latest]);

  const renderRemoteImage = useCallback(
    (props: { src?: string; alt?: string; title?: string }) => (
      <RemoteImage
        key={`${configRef.current?.baseUrl}/${docRef.current?.path}/${props.src}`}
        baseUrl={configRef.current?.baseUrl ?? ""}
        notePath={docRef.current?.path ?? ""}
        {...props}
      />
    ),
    [],
  );

  const isTile = surfaceMode === "tile";
  const tileTitle = title.trim();
  const enterClass = hasEnteredOnce.current ? "" : "animate-window-enter";
  const surfaceWrapperClassName = `w-full h-screen flex flex-col bg-transparent p-0 ${
    isExiting ? "animate-window-exit" : enterClass
  }`;
  const padSurfaceClassName =
    "app-surface-frame relative noise-bg w-full h-full min-h-0 bg-cloud overflow-hidden flex flex-col flex-1 border border-paper-deep/70 shadow-[0_1px_10px_rgba(26,26,24,0.06)] transition-all duration-200 ease-out";

  return (
    <div className={surfaceWrapperClassName}>
      {isTile ? (
        <Tile
          title={tileTitle || undefined}
          content={doc?.content ?? ""}
          color={tileColor}
          fontSize={surfaceFontSize}
          renderMarkdown={tileRenderMarkdown}
          renderImage={renderRemoteImage}
          width="100%"
          className="h-full cursor-default"
          data-surface-mode={surfaceMode}
          data-context-menu="tile"
          data-remote-note-path={doc?.path ?? ""}
          onMouseDown={handleDrag}
          onDoubleClick={handleTileDoubleClick}
        >
          <button
            type="button"
            aria-label="取消钉屏"
            title="取消钉屏"
            onMouseDown={(event) => event.stopPropagation()}
            onClick={() => handleCloseRef.current()}
            className="absolute top-2 right-2 z-10 w-6 h-6 flex items-center justify-center rounded-full text-ink-ghost/70 hover:text-red-400 hover:bg-danger-bg/80 transition-colors cursor-pointer"
          >
            <svg
              width="12"
              height="12"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2.5"
              strokeLinecap="round"
            >
              <path d="M18 6L6 18M6 6l12 12" />
            </svg>
          </button>
          <SurfaceResizeHandles />
        </Tile>
      ) : (
        <div className={padSurfaceClassName} data-surface-mode={surfaceMode}>
          <>
            <div
              className="flex items-center justify-between px-4 pt-3 pb-0 cursor-default"
              onMouseDown={handleDrag}
            >
              <div className="flex items-center gap-0.5">
                <button
                  onClick={resetDraft}
                  className={`relative px-3.5 py-1.5 text-[13px] rounded-t-lg transition-all duration-200 cursor-pointer ${
                    mode === "new"
                      ? "text-bamboo font-medium"
                      : "text-ink-ghost hover:text-ink-faint"
                  }`}
                >
                  {doc?.path ? tabLabels.edit : tabLabels.new}
                  {mode === "new" && (
                    <div className="absolute bottom-0 left-3 right-3 h-[2px] bg-bamboo rounded-full" />
                  )}
                </button>
                <button
                  onClick={() => setMode("open")}
                  className={`relative px-3.5 py-1.5 text-[13px] rounded-t-lg transition-all duration-200 cursor-pointer ${
                    mode === "open"
                      ? "text-bamboo font-medium"
                      : "text-ink-ghost hover:text-ink-faint"
                  }`}
                >
                  {tabLabels.open}
                  {mode === "open" && (
                    <div className="absolute bottom-0 left-3 right-3 h-[2px] bg-bamboo rounded-full" />
                  )}
                </button>
              </div>

              <div className="ml-auto flex items-center gap-1.5">
                <button
                  onClick={() => void handlePin()}
                  disabled={!doc}
                  className="group w-7 h-7 flex items-center justify-center rounded-lg transition-all duration-200 cursor-pointer text-ink-ghost hover:text-ink-faint hover:bg-paper-warm disabled:opacity-40 disabled:cursor-default"
                  title={t("notepad.tooltip.pinToTile", { defaultValue: "转为磁贴" })}
                >
                  <svg
                    width="14"
                    height="14"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2"
                    strokeLinecap="round"
                    strokeLinejoin="round"
                  >
                    <path d="M12 17v5" />
                    <path d="M9 10.76a2 2 0 0 1-1.11 1.79l-1.78.9A2 2 0 0 0 5 15.24V16a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-.76a2 2 0 0 0-1.11-1.79l-1.78-.9A2 2 0 0 1 15 10.76V7a1 1 0 0 1 1-1 1 1 0 0 0 1-1V4a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v1a1 1 0 0 0 1 1 1 1 0 0 1 1 1z" />
                  </svg>
                </button>

                <button
                  onClick={handleClose}
                  className="group w-7 h-7 flex items-center justify-center rounded-lg text-ink-ghost hover:bg-danger-bg hover:text-red-400 transition-all duration-200 cursor-pointer"
                  title={t("notepad.tooltip.close", { defaultValue: "关闭" })}
                >
                  <svg
                    width="13"
                    height="13"
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="2.5"
                    strokeLinecap="round"
                  >
                    <path d="M18 6L6 18M6 6l12 12" />
                  </svg>
                </button>
              </div>
            </div>

            <div className="mx-4 mt-1 h-px bg-paper-deep/50" />

            {mode === "new" ? (
              <div
                data-pad-editor-body="true"
                className="px-4 pt-3 pb-2 flex flex-col flex-1 min-h-0"
              >
                <input
                  ref={titleRef}
                  type="text"
                  value={title}
                  onChange={(event) => {
                    setTitle(event.target.value);
                    setStatus("dirty");
                  }}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === "ArrowDown") {
                      event.preventDefault();
                      contentRef.current?.focus();
                    }
                  }}
                  placeholder={t("notepad.placeholder.title", { defaultValue: "标题（可选）" })}
                  className="w-full font-display font-medium text-ink placeholder:text-ink-ghost/60 mb-2 tracking-wide shrink-0 bg-transparent outline-none"
                  style={{ fontSize: `${surfaceFontSize}px` }}
                />

                <textarea
                  ref={contentRef}
                  data-tab-indent="true"
                  value={doc?.content ?? ""}
                  onChange={(event) => {
                    if (!doc) return;
                    changeDoc({ ...doc, content: event.target.value });
                    setStatus("dirty");
                  }}
                  onPaste={(event) => {
                    const images = getImageFiles(event.clipboardData);
                    if (images.length > 0) {
                      event.preventDefault();
                      void insertImages(images);
                    }
                  }}
                  onDrop={(event) => {
                    const images = getImageFiles(event.dataTransfer);
                    if (images.length > 0) {
                      event.preventDefault();
                      void insertImages(images);
                    }
                  }}
                  onDragOver={(event) => {
                    const hasImage = Array.from(event.dataTransfer.items).some(
                      (item) => item.kind === "file" && item.type.startsWith("image/"),
                    );
                    if (hasImage) {
                      event.preventDefault();
                      event.dataTransfer.dropEffect = "copy";
                    }
                  }}
                  onKeyDown={(event) => {
                    if (event.key === "ArrowUp") {
                      const ta = contentRef.current;
                      if (ta && ta.selectionStart === ta.selectionEnd) {
                        const textBeforeCursor = (doc?.content ?? "").slice(
                          0,
                          ta.selectionStart,
                        );
                        if (!textBeforeCursor.includes("\n")) {
                          event.preventDefault();
                          titleRef.current?.focus();
                        }
                      }
                    }
                  }}
                  placeholder={t("notepad.placeholder.content", { defaultValue: "写点什么……" })}
                  className="w-full flex-1 min-h-0 pb-2 leading-relaxed text-ink-soft font-body placeholder:text-ink-ghost/50 bg-transparent outline-none resize-none"
                  style={{ fontSize: `${surfaceFontSize}px`, tabSize: `var(--tab-indent-size, 2)` }}
                />

                {latest && (
                  <section className="mb-2 rounded-xl border border-amber-500/40 bg-amber-50/60 p-2 flex flex-col gap-1.5 shrink-0">
                    <p className="text-[11px] text-amber-700 leading-relaxed">
                      {t("remote.conflict.panelHint", {
                        defaultValue:
                          "服务器版本已变化（下方为服务器内容，可复制合并到上方草稿）。合并完成后采用它为保存基准。",
                      })}
                    </p>
                    <textarea
                      readOnly
                      value={latest.content}
                      aria-label={t("remote.conflict.serverVersion", {
                        defaultValue: "服务器当前版本",
                      })}
                      className="min-h-16 max-h-28 flex-1 resize-none rounded-lg bg-cloud/70 p-2 text-[11px] leading-relaxed text-ink-soft"
                    />
                    <div className="flex items-center gap-2">
                      <button
                        type="button"
                        onClick={adoptServerBase}
                        className="px-3 py-1.5 text-[12px] text-cloud bg-bamboo hover:bg-bamboo-light rounded-lg transition-all duration-200 font-medium cursor-pointer"
                      >
                        {t("remote.conflict.adoptBase", {
                          defaultValue: "已完成合并，采用此基准保存",
                        })}
                      </button>
                      <button
                        type="button"
                        onClick={() => setLatest(null)}
                        className="px-3 py-1.5 text-[12px] text-ink-faint hover:text-ink-soft rounded-lg hover:bg-paper-warm transition-all duration-200 cursor-pointer"
                      >
                        {t("remote.conflict.later", { defaultValue: "稍后处理" })}
                      </button>
                    </div>
                  </section>
                )}

                <div className="flex items-center justify-between mt-auto pt-2 border-t border-paper-deep/30 shrink-0">
                  <span className="text-[11px] text-ink-ghost font-mono tabular-nums truncate max-w-[170px]">
                    {`${countNoteChars(doc?.content ?? "")} ${t("common.wordCountUnit", { defaultValue: "字" })} · ${statusLabel[status]}`}
                  </span>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={resetDraft}
                      className="px-4 py-1.5 text-[12px] text-ink-faint hover:text-ink-soft rounded-lg hover:bg-paper-warm transition-all duration-200 cursor-pointer"
                    >
                      {t("notepad.button.clear", { defaultValue: "清空" })}
                    </button>
                    <button
                      onClick={() => void handleSaveRef.current()}
                      className="px-4 py-1.5 text-[12px] text-cloud bg-bamboo hover:bg-bamboo-light rounded-lg transition-all duration-200 font-medium cursor-pointer"
                    >
                      {t("common.save", { defaultValue: "保存" })}
                    </button>
                  </div>
                </div>
              </div>
            ) : (
              <RemoteOpenPanel
                files={files}
                currentPath={doc?.path ?? null}
                onOpenFile={(path) => void handleOpenNote(path)}
              />
            )}
          </>
          <SurfaceResizeHandles />
        </div>
      )}
    </div>
  );
}
