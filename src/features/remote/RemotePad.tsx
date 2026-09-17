import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { MouseEvent } from "react";
import { useTranslation } from "react-i18next";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { listen } from "@tauri-apps/api/event";
import { confirm } from "@tauri-apps/plugin-dialog";
import { getErrorMessage } from "../notes/api";
import { countNoteChars } from "../notes/noteUtils";
import { getConfig } from "../settings/api";
import { reportInstallPreparation } from "../update/api";
import type { UpdateInstallPrepareRequest } from "../update/types";
import { MarkdownPreviewLazy } from "../markdown/MarkdownPreviewLazy";
import { showToast } from "../../components/Toast";
import {
  hideCurrentWindow,
  showCurrentWindow,
  startCurrentWindowDrag,
  startCurrentWindowResize,
} from "../windows/controls";
import type { ResizeDirection } from "../windows/controls";
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
import { isDirty, mergeBase, openedSession, savedSession } from "./session";
import type { RemoteSession } from "./session";
import { RemoteOpenPanel } from "./RemoteOpenPanel";

type RemoteStatus = "empty" | "opened" | "saved" | "dirty" | "saveFailed";

const surfaceResizeHandles: Array<{
  direction: ResizeDirection;
  size: string;
  className: string;
}> = [
  {
    direction: "NorthWest",
    size: "w-8 h-8",
    className: "top-0 left-0 cursor-nwse-resize",
  },
  {
    direction: "NorthEast",
    size: "w-5 h-5",
    className: "top-0 right-0 cursor-nesw-resize",
  },
  {
    direction: "SouthWest",
    size: "w-8 h-8",
    className: "bottom-0 left-0 cursor-nesw-resize",
  },
  {
    direction: "SouthEast",
    size: "w-5 h-5",
    className: "bottom-0 right-0 cursor-nwse-resize",
  },
];

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

export function RemotePad() {
  const { t } = useTranslation();
  const [mode, setMode] = useState<"edit" | "open">("edit");
  const [doc, setDoc] = useState<RemoteSession | null>(null);
  const [files, setFiles] = useState<string[]>([]);
  const [latest, setLatest] = useState<RemoteDocument | null>(null);
  const [status, setStatus] = useState<RemoteStatus>("empty");
  const [preview, setPreview] = useState(false);
  const [autoSave, setAutoSave] = useState(true);
  const [fontSize, setFontSize] = useState(14);
  const [busy, setBusy] = useState(false);
  const [isExiting, setIsExiting] = useState(false);
  const busyRef = useRef(false);
  const contentRef = useRef<HTMLTextAreaElement>(null);
  const docRef = useRef(doc);
  docRef.current = doc;
  const configRef = useRef<RemoteConfig | null>(null);
  const dirtyReportedRef = useRef(false);
  const hasEnteredOnce = useRef(false);
  const dirty = isDirty(doc);

  const statusLabel = useMemo<Record<RemoteStatus, string>>(
    () => ({
      empty: t("notepad.status.empty", { defaultValue: "空" }),
      opened: t("notepad.status.opened", { defaultValue: "已打开" }),
      saved: t("notepad.status.saved", { defaultValue: "已保存" }),
      dirty: t("notepad.status.unsaved", { defaultValue: "未保存" }),
      saveFailed: t("notepad.status.saveFailed", { defaultValue: "保存失败" }),
    }),
    [t],
  );

  function changeDoc(next: RemoteSession | null) {
    docRef.current = next;
    setDoc(next);
    const nextDirty = isDirty(next);
    if (nextDirty !== dirtyReportedRef.current) {
      dirtyReportedRef.current = nextDirty;
      void setRemoteDirty(nextDirty).catch(() => undefined);
    }
  }

  async function syncDirtyFlag(value: boolean) {
    dirtyReportedRef.current = value;
    await setRemoteDirty(value).catch(() => undefined);
  }

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

  const openFile = useCallback(async (path: string) => {
    const connection = configRef.current;
    if (!connection?.baseUrl) return;
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    try {
      changeDoc(openedSession(path, await readRemoteFile(connection.baseUrl, path)));
      setLatest(null);
      setStatus("opened");
      setMode("edit");
      setPreview(false);
    } catch (error) {
      showToast(getErrorMessage(error));
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }, []);

  const createDraft = useCallback((path: string) => {
    changeDoc({ path, content: "", baseline: "", revision: null });
    setLatest(null);
    setStatus("dirty");
    setMode("edit");
    setPreview(false);
  }, []);

  const requestOpen = useCallback(
    async (path: string) => {
      if (!dirty) {
        void openFile(path);
        return;
      }
      const accepted = await confirm(
        t("remote.confirm.discardToOpen", {
          defaultValue: "当前笔记未保存。切换文件将放弃这些更改，确定继续？",
        }),
        { title: t("remote.title", { defaultValue: "远程笔记" }), kind: "warning" },
      );
      if (accepted) void openFile(path);
    },
    [dirty, openFile, t],
  );

  const requestCreate = useCallback(
    async (path: string, exists: boolean) => {
      if (exists) {
        void openFile(path);
        return;
      }
      if (dirty) {
        const accepted = await confirm(
          t("remote.confirm.discardToOpen", {
            defaultValue: "当前笔记未保存。切换文件将放弃这些更改，确定继续？",
          }),
          { title: t("remote.title", { defaultValue: "远程笔记" }), kind: "warning" },
        );
        if (!accepted) return;
      }
      createDraft(path);
    },
    [createDraft, dirty, openFile, t],
  );

  const save = useCallback(async (): Promise<boolean> => {
    const current = docRef.current;
    const connection = configRef.current;
    if (!current || !connection?.baseUrl || !isDirty(current)) return false;
    if (busyRef.current) return false;
    busyRef.current = true;
    setBusy(true);
    try {
      const result = await writeRemoteFile(
        connection.baseUrl,
        current.path,
        current.content,
        current.revision,
      );
      const after = docRef.current;
      changeDoc(savedSession(current, current.content, result.revision));
      setLatest(null);
      setStatus(after !== null && after.content !== current.content ? "dirty" : "saved");
      setFiles((items) => (items.includes(current.path) ? items : [...items, current.path].sort()));
      return true;
    } catch (error) {
      const code = (error as { code?: string }).code;
      setStatus("saveFailed");
      if (code === "conflict") {
        try {
          setLatest(await readRemoteFile(connection.baseUrl, current.path));
        } catch {
          // 保留原始冲突提示
        }
        showToast(
          t("remote.conflictDetected", {
            defaultValue: "服务器版本已变化，请在下方合并后保存。",
          }),
        );
      } else {
        showToast(getErrorMessage(error));
      }
      return false;
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }, [t]);

  const saveRef = useRef(save);
  saveRef.current = save;

  useEffect(() => {
    if (!autoSave || mode !== "edit" || !dirty || !doc) return;

    const timer = window.setTimeout(() => {
      void saveRef.current();
    }, 900);

    return () => window.clearTimeout(timer);
  }, [autoSave, dirty, doc, mode]);

  const adoptServerBase = useCallback(() => {
    const current = docRef.current;
    if (!current || !latest) return;
    changeDoc(mergeBase(current, latest));
    setLatest(null);
    setStatus("dirty");
  }, [latest]);

  const resetDraft = useCallback(async () => {
    const current = docRef.current;
    if (current && isDirty(current) && current.content.trim()) {
      const accepted = await confirm(
        t("remote.confirm.resetDraft", {
          defaultValue: "放弃当前未保存的更改，恢复为服务器上的版本？",
        }),
        { title: t("remote.title", { defaultValue: "远程笔记" }), kind: "warning" },
      );
      if (!accepted) return;
    }
    if (current?.revision) {
      changeDoc({ ...current, content: current.baseline });
      setStatus("opened");
    } else {
      changeDoc(null);
      setStatus("empty");
    }
    setLatest(null);
  }, [t]);

  const insertImages = useCallback(
    async (selected: File[]) => {
      const current = docRef.current;
      const connection = configRef.current;
      const textarea = contentRef.current;
      if (!current || !connection?.baseUrl || !textarea || busyRef.current) return;
      let cursor = textarea.selectionStart;
      let selectionEnd = textarea.selectionEnd;
      let updated = current;
      let completed = 0;
      try {
        selected.forEach(validateImageUpload);
        await syncDirtyFlag(true);
        for (const file of selected) {
          const result = await uploadRemoteImage(
            connection.baseUrl,
            current.path,
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
            ? `${t("remote.image.partialUpload", { defaultValue: "已插入 {count} 张图片，其余上传失败。", count: completed })}${getErrorMessage(error)}`
            : `${t("remote.image.uploadFailed", { defaultValue: "图片上传失败。" })}${getErrorMessage(error)}`,
        );
      } finally {
        await syncDirtyFlag(isDirty(docRef.current));
      }
      requestAnimationFrame(() => {
        const editor = contentRef.current;
        if (editor && docRef.current?.path === current.path) {
          editor.focus();
          editor.setSelectionRange(cursor, selectionEnd);
        }
      });
    },
    [t],
  );

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

  useEffect(() => {
    let cancelled = false;

    async function bootstrap() {
      try {
        const [appConfig, remote] = await Promise.all([getConfig(), getRemoteConfig()]);
        if (cancelled) return;
        setAutoSave(appConfig.noteSurfaceAutoSave);
        setFontSize(appConfig.surfaceFontSize ?? 14);
        configRef.current = remote;
        if (!remote.baseUrl) {
          showToast(
            t("remote.notice.unconfigured", {
              defaultValue: "请先在应用设置中配置远程笔记 API。",
            }),
          );
          setMode("open");
          return;
        }
        const { files: found } = await listRemoteFiles(remote.baseUrl);
        if (cancelled) return;
        setFiles(found);
        if (remote.defaultFile && found.includes(remote.defaultFile)) {
          await openFile(remote.defaultFile);
        } else {
          setMode("open");
        }
      } catch (error) {
        showToast(getErrorMessage(error));
      }
    }

    void bootstrap();
    return () => {
      cancelled = true;
    };
  }, [openFile, t]);

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
    const unlisten = listen<UpdateInstallPrepareRequest>(
      "update://prepare-install",
      (event) => {
        const respond = async () => {
          const windowLabel = getCurrentWindow().label;
          if (!isDirty(docRef.current)) {
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
              t("remote.notice.busySaving", { defaultValue: "远程笔记保存失败或正在同步，请稍后重试。" }),
            );
          }
        };

        void respond().catch(() => undefined);
      },
    );
    return () => {
      void unlisten.then((fn) => fn());
    };
  }, [t]);

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
      const current = docRef.current;
      if (!connection?.baseUrl) return;
      void (async () => {
        try {
          const { files: found } = await listRemoteFiles(connection.baseUrl);
          setFiles(found);
          if (current && !isDirty(current)) {
            const fresh = await readRemoteFile(connection.baseUrl, current.path);
            if (docRef.current === current) {
              changeDoc(openedSession(current.path, fresh));
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
  }, []);

  useEffect(() => {
    const unlisten = listen<{
      surfaceFontSize?: number;
      noteSurfaceAutoSave?: boolean;
    }>("config-changed", (event) => {
      if (event.payload.surfaceFontSize != null) setFontSize(event.payload.surfaceFontSize);
      if (event.payload.noteSurfaceAutoSave != null)
        setAutoSave(event.payload.noteSurfaceAutoSave);
    });
    return () => {
      void unlisten.then((fn) => fn());
    };
  }, []);

  useEffect(() => {
    if (mode !== "open") return;
    void refreshFiles();
  }, [mode, refreshFiles]);

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
        event.preventDefault();
        void saveRef.current();
      }
      if (event.key === "Escape") handleCloseRef.current();
    }

    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, []);

  const handleDrag = (event: MouseEvent<HTMLElement>) => {
    if ((event.target as HTMLElement).closest("button,input,textarea")) return;
    void startCurrentWindowDrag().catch(() => undefined);
  };

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

  const tabClass = (active: boolean) =>
    `relative px-3.5 py-1.5 text-[13px] rounded-t-lg transition-all duration-200 cursor-pointer ${
      active ? "text-bamboo font-medium" : "text-ink-ghost hover:text-ink-faint"
    }`;

  const fileName = doc?.path.split("/").pop() ?? doc?.path ?? "";
  const enterClass = hasEnteredOnce.current ? "" : "animate-window-enter";
  const surfaceWrapperClassName = `w-full h-screen flex flex-col bg-transparent p-0 ${
    isExiting ? "animate-window-exit" : enterClass
  }`;
  const padSurfaceClassName =
    "app-surface-frame relative noise-bg w-full h-full min-h-0 bg-cloud overflow-hidden flex flex-col flex-1 border border-paper-deep/70 shadow-[0_1px_10px_rgba(26,26,24,0.06)] transition-all duration-200 ease-out";

  return (
    <div className={surfaceWrapperClassName}>
      <div className={padSurfaceClassName} data-surface-mode="remote-pad">
        <div
          className="flex items-center justify-between px-4 pt-3 pb-0 cursor-default"
          onMouseDown={handleDrag}
        >
          <div className="flex items-center gap-0.5 min-w-0">
            <button
              onClick={() => setMode("edit")}
              className={`${tabClass(mode === "edit")} max-w-40 truncate`}
              title={doc?.path ?? ""}
            >
              {fileName || t("notepad.tab.new", { defaultValue: "新建" })}
              {mode === "edit" && (
                <div className="absolute bottom-0 left-3 right-3 h-[2px] bg-bamboo rounded-full" />
              )}
            </button>
            <button
              onClick={() => setMode("open")}
              className={tabClass(mode === "open")}
            >
              {t("notepad.tab.open", { defaultValue: "打开" })}
              {mode === "open" && (
                <div className="absolute bottom-0 left-3 right-3 h-[2px] bg-bamboo rounded-full" />
              )}
            </button>
          </div>

          <div className="ml-auto flex items-center gap-1.5 shrink-0">
            <button
              onClick={() => {
                setPreview(!preview);
                if (!preview) contentRef.current?.blur();
              }}
              disabled={!doc}
              className="group w-7 h-7 flex items-center justify-center rounded-lg transition-all duration-200 cursor-pointer text-ink-ghost hover:text-ink-faint hover:bg-paper-warm disabled:opacity-40 disabled:cursor-default"
              title={
                preview
                  ? t("remote.tooltip.edit", { defaultValue: "返回编辑" })
                  : t("remote.tooltip.preview", { defaultValue: "预览 Markdown" })
              }
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
                <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" />
                <circle cx="12" cy="12" r="3" />
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

        {mode === "edit" ? (
          <div data-pad-editor-body="true" className="px-4 pt-3 pb-2 flex flex-col flex-1 min-h-0">
            {preview ? (
              <div className="flex-1 min-h-0 overflow-y-auto scrollbar-hidden text-ink-soft leading-relaxed">
                <MarkdownPreviewLazy
                  content={doc?.content ?? ""}
                  renderImage={renderRemoteImage}
                />
              </div>
            ) : (
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
                  const images = Array.from(event.clipboardData.files).filter((file) =>
                    file.type.startsWith("image/"),
                  );
                  if (images.length > 0) {
                    event.preventDefault();
                    void insertImages(images);
                  }
                }}
                onKeyDown={(event) => {
                  if (event.key === "Escape") {
                    event.preventDefault();
                    handleCloseRef.current();
                  }
                }}
                placeholder={
                  doc
                    ? t("notepad.placeholder.content", { defaultValue: "写点什么……" })
                    : t("remote.placeholder.pickFile", {
                        defaultValue: "在“打开”里选择或新建远程文件…",
                      })
                }
                className="w-full flex-1 min-h-0 pb-2 leading-relaxed text-ink-soft font-body placeholder:text-ink-ghost/50 bg-transparent outline-none resize-none"
                style={{ fontSize: `${fontSize}px`, tabSize: `var(--tab-indent-size, 2)` }}
              />
            )}

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
                  aria-label={t("remote.conflict.serverVersion", { defaultValue: "服务器当前版本" })}
                  className="min-h-16 max-h-28 flex-1 resize-none rounded-lg bg-cloud/70 p-2 text-[11px] leading-relaxed text-ink-soft"
                />
                <div className="flex items-center gap-2">
                  <button
                    type="button"
                    onClick={adoptServerBase}
                    className="px-3 py-1.5 text-[12px] text-cloud bg-bamboo hover:bg-bamboo-light rounded-lg transition-all duration-200 font-medium cursor-pointer"
                  >
                    {t("remote.conflict.adoptBase", { defaultValue: "已完成合并，采用此基准保存" })}
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
              <span className="text-[11px] text-ink-ghost font-mono tabular-nums truncate max-w-[130px]">
                {`${countNoteChars(doc?.content ?? "")} ${t("common.wordCountUnit", { defaultValue: "字" })} · ${
                  busy
                    ? t("remote.status.syncing", { defaultValue: "同步中" })
                    : statusLabel[status]
                }`}
              </span>
              <div className="flex items-center gap-2">
                <button
                  onClick={() => void resetDraft()}
                  disabled={!doc}
                  className="px-4 py-1.5 text-[12px] text-ink-faint hover:text-ink-soft rounded-lg hover:bg-paper-warm transition-all duration-200 cursor-pointer disabled:opacity-40 disabled:cursor-default"
                >
                  {t("notepad.button.clear", { defaultValue: "清空" })}
                </button>
                <button
                  onClick={() => void saveRef.current()}
                  disabled={!doc}
                  className="px-4 py-1.5 text-[12px] text-cloud bg-bamboo hover:bg-bamboo-light rounded-lg transition-all duration-200 font-medium cursor-pointer disabled:opacity-40 disabled:cursor-default"
                >
                  {t("common.save", { defaultValue: "保存" })}
                </button>
              </div>
            </div>
          </div>
        ) : (
          <RemoteOpenPanel
            files={files}
            currentPath={doc?.path ?? ""}
            defaultNewPath={configRef.current?.defaultFile ?? ""}
            onOpenFile={(path) => void requestOpen(path)}
            onCreateFile={(path) => void requestCreate(path, files.includes(path))}
          />
        )}
        <SurfaceResizeHandles />
      </div>
    </div>
  );
}
