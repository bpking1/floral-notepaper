import { useCallback, useEffect, useRef, useState } from "react";
import { getCurrentWindow } from "@tauri-apps/api/window";
import { listen } from "@tauri-apps/api/event";
import { confirm } from "@tauri-apps/plugin-dialog";
import { writeText } from "@tauri-apps/plugin-clipboard-manager";
import { getErrorMessage } from "../notes/api";
import { MarkdownPreviewLazy } from "../markdown/MarkdownPreviewLazy";
import {
  hideCurrentWindow,
  showCurrentWindow,
  startCurrentWindowDrag,
  startCurrentWindowResize,
} from "../windows/controls";
import { reportInstallPreparation } from "../update/api";
import type { UpdateInstallPrepareRequest } from "../update/types";
import {
  getRemoteConfig,
  listRemoteFiles,
  readRemoteFile,
  setRemoteDirty,
  writeRemoteFile,
  uploadRemoteImage,
} from "./api";
import type { RemoteConfig, RemoteDocument } from "./api";
import { isDirty, mergeBase, openedSession, savedSession } from "./session";
import type { RemoteSession } from "./session";
import { RemoteImage } from "./RemoteImage";
import { REMOTE_IMAGE_ACCEPT, insertImageLinks, validateImageUpload } from "./imageUpload";

export function RemotePad() {
  const [config, setConfig] = useState<RemoteConfig | null>(null);
  const [files, setFiles] = useState<string[]>([]);
  const [doc, setDoc] = useState<RemoteSession | null>(null);
  const [latest, setLatest] = useState<RemoteDocument | null>(null);
  const [filter, setFilter] = useState("");
  const [newPath, setNewPath] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [preview, setPreview] = useState(false);
  const [sidebar, setSidebar] = useState(true);
  const busyRef = useRef(false);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const pickerOpenRef = useRef(false);
  const docRef = useRef(doc);
  docRef.current = doc;
  const configRef = useRef(config);
  configRef.current = config;
  const dirty = isDirty(doc);
  const renderRemoteImage = useCallback(
    (props: { src?: string; alt?: string; title?: string }) => (
      <RemoteImage
        key={`${config?.baseUrl}/${doc?.path}/${props.src}`}
        baseUrl={config?.baseUrl ?? ""}
        notePath={doc?.path ?? ""}
        {...props}
      />
    ),
    [config?.baseUrl, doc?.path],
  );

  function changeDoc(next: RemoteSession | null) {
    docRef.current = next;
    setDoc(next);
    void setRemoteDirty(isDirty(next)).catch((e) => setNotice(getErrorMessage(e)));
  }
  async function run(action: () => Promise<void>) {
    if (busyRef.current) return;
    busyRef.current = true;
    setBusy(true);
    setNotice("");
    try {
      await action();
    } catch (e) {
      setNotice(getErrorMessage(e));
    } finally {
      busyRef.current = false;
      setBusy(false);
    }
  }
  async function refresh() {
    if (isDirty(docRef.current)) return;
    await run(async () => {
      const next = await getRemoteConfig();
      const previous = configRef.current;
      if (previous?.baseUrl !== next.baseUrl) {
        changeDoc(null);
        setFiles([]);
        setLatest(null);
      }
      setConfig(next);
      configRef.current = next;
      if (!next.baseUrl) {
        setNotice("请在主窗口的应用设置中配置远程笔记 API。");
        return;
      }
      const { files: found } = await listRemoteFiles(next.baseUrl);
      setFiles(found);
      const target =
        previous?.baseUrl === next.baseUrl
          ? docRef.current?.path || next.defaultFile
          : next.defaultFile;
      if (target && found.includes(target)) {
        changeDoc(openedSession(target, await readRemoteFile(next.baseUrl, target)));
        setLatest(null);
      } else {
        changeDoc(null);
        if (target) {
          setNewPath(target);
          setNotice("默认文件不存在，可点击新建；也可以从列表选择文件。");
        }
      }
    });
  }
  async function open(path: string) {
    if (!config || dirty) return;
    await run(async () => {
      changeDoc(openedSession(path, await readRemoteFile(config.baseUrl, path)));
      setLatest(null);
    });
  }
  async function save() {
    const current = docRef.current,
      connection = configRef.current;
    if (!current || !connection || !isDirty(current)) return;
    await run(async () => {
      const result = await writeRemoteFile(
        connection.baseUrl,
        current.path,
        current.content,
        current.revision,
      );
      changeDoc(savedSession(current, current.content, result.revision));
      setLatest(null);
      setNotice("已保存到服务器。");
      setFiles((items) => (items.includes(current.path) ? items : [...items, current.path].sort()));
    });
  }
  async function insertImages(selected: File[]) {
    const current = docRef.current;
    const connection = configRef.current;
    const textarea = textareaRef.current;
    if (!current || !connection || !textarea || busyRef.current || selected.length === 0) return;
    let cursor = textarea.selectionStart;
    let selectionEnd = textarea.selectionEnd;
    let updated = current;
    let completed = 0;
    await run(async () => {
      // Check the whole selection before uploading anything.
      selected.forEach(validateImageUpload);
      // Also protect a clean note while an upload is in progress.
      await setRemoteDirty(true);
      try {
        for (const file of selected) {
          setNotice(`正在上传第 ${completed + 1} / ${selected.length} 张图片…`);
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
          completed++;
        }
        setNotice(`已插入 ${completed} 张图片，请保存笔记以保留图片链接。`);
      } catch (error) {
        throw new Error(
          `${completed ? `已插入 ${completed} 张图片，其余上传失败。` : "图片上传失败。"}${getErrorMessage(error)}`,
        );
      } finally {
        await setRemoteDirty(isDirty(docRef.current));
      }
    });
    requestAnimationFrame(() => {
      const editor = textareaRef.current;
      if (editor && docRef.current?.path === current.path) {
        editor.focus();
        editor.setSelectionRange(cursor, selectionEnd);
      }
    });
  }
  async function inspectServer() {
    if (!config || !doc) return;
    await run(async () => {
      setLatest(await readRemoteFile(config.baseUrl, doc.path));
    });
  }
  function create() {
    if (dirty || busy || !config?.baseUrl) return;
    const path = newPath.trim();
    if (
      !path ||
      path.startsWith("/") ||
      path.includes("\\") ||
      path.split("/").some((s) => !s || s === "." || s === "..") ||
      !/\.(md|markdown)$/i.test(path)
    ) {
      setNotice("请输入笔记库内的相对 Markdown 路径，例如 Inbox.md。");
      return;
    }
    if (files.includes(path)) {
      void open(path);
      return;
    }
    changeDoc({ path, content: "", baseline: "", revision: null });
    setLatest(null);
    setNotice("新文件，保存后写入服务器。父目录需要已存在。");
  }
  async function discard() {
    if (busyRef.current || !isDirty(docRef.current)) return;
    const accepted = await confirm("放弃当前未保存的更改？需要保留的内容请先复制。", {
      title: "远程笔记",
      kind: "warning",
    });
    if (!accepted || busyRef.current) return;
    const current = docRef.current;
    if (current) {
      changeDoc(current.revision ? { ...current, content: current.baseline } : null);
      setLatest(null);
      setNotice("已放弃本次更改。");
    }
  }
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  const saveRef = useRef(save);
  saveRef.current = save;
  useEffect(() => {
    void showCurrentWindow().catch(() => {});
    void refreshRef.current();
    const focus = getCurrentWindow().onFocusChanged(({ payload }) => {
      if (payload && !pickerOpenRef.current) void refreshRef.current();
    });
    const exit = listen("remote-exit-blocked", () =>
      setNotice("还有未保存的远程草稿。请保存，或复制后放弃更改，再退出应用。"),
    );
    const update = listen<UpdateInstallPrepareRequest>("update://prepare-install", (event) => {
      const blocked = isDirty(docRef.current) || busyRef.current;
      void reportInstallPreparation(
        event.payload.requestId,
        getCurrentWindow().label,
        blocked ? "failed" : "ready",
        blocked ? "远程笔记尚未保存或正在操作，请稍后更新。" : undefined,
      );
    });
    const key = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
        event.preventDefault();
        void saveRef.current();
      }
      if (event.key === "Escape") void hideCurrentWindow();
    };
    const picker = fileInputRef.current;
    const cancelPicker = () => {
      pickerOpenRef.current = false;
    };
    picker?.addEventListener("cancel", cancelPicker);
    window.addEventListener("keydown", key);
    return () => {
      for (const subscription of [focus, exit, update]) void subscription.then((off) => off());
      window.removeEventListener("keydown", key);
      picker?.removeEventListener("cancel", cancelPicker);
    };
  }, []);
  const button =
    "rounded-lg px-2.5 py-1.5 text-xs hover:bg-paper-deep/40 disabled:opacity-35 disabled:cursor-default cursor-pointer";
  return (
    <div className="relative flex h-full flex-col rounded-2xl border border-paper-deep/50 bg-paper-warm text-ink shadow-xl overflow-hidden">
      <header
        className="flex items-center gap-2 px-3 h-12 shrink-0 border-b border-paper-deep/30 select-none"
        onMouseDown={(e) => {
          if (e.button === 0 && !(e.target as HTMLElement).closest("button"))
            void startCurrentWindowDrag();
        }}
      >
        <button
          type="button"
          className={button}
          onClick={() => setSidebar(!sidebar)}
          title="显示或隐藏文件列表"
        >
          ☰
        </button>
        <span className="font-display text-sm truncate flex-1">{doc?.path || "远程笔记"}</span>
        <span className="text-[11px] text-ink-faint">
          {busy ? "连接中…" : dirty ? "未保存" : "服务器"}
        </span>
        <input
          ref={fileInputRef}
          type="file"
          accept={REMOTE_IMAGE_ACCEPT}
          multiple
          hidden
          aria-label="选择要上传的图片"
          onChange={(event) => {
            const selected = Array.from(event.currentTarget.files ?? []);
            event.currentTarget.value = "";
            pickerOpenRef.current = false;
            void insertImages(selected);
          }}
        />
        <button
          type="button"
          className={button}
          disabled={busy || !doc || preview}
          title="上传图片并插入相对路径链接，也可以直接粘贴截图"
          onClick={() => {
            pickerOpenRef.current = true;
            fileInputRef.current?.click();
          }}
        >
          插入图片
        </button>
        <button
          type="button"
          className={button}
          disabled={busy}
          onClick={() => setPreview(!preview)}
        >
          {preview ? "编辑" : "预览"}
        </button>
        <button
          type="button"
          className={button}
          disabled={busy || !dirty}
          onClick={() => void save()}
        >
          保存
        </button>
        <button
          type="button"
          className={button}
          title="隐藏窗口，草稿保留在内存中"
          onClick={() => void hideCurrentWindow()}
        >
          ✕
        </button>
      </header>
      <div className="flex flex-1 min-h-0">
        {sidebar && (
          <aside className="w-44 shrink-0 border-r border-paper-deep/30 flex flex-col p-2 gap-2">
            <input
              aria-label="搜索远程文件"
              placeholder="搜索笔记…"
              className="w-full rounded-lg bg-cloud/60 px-2 py-2 text-xs outline-none"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
            />
            <button
              type="button"
              className={button}
              disabled={busy || dirty}
              onClick={() => void refresh()}
            >
              刷新文件列表
            </button>
            <div className="flex-1 overflow-auto space-y-1">
              {files
                .filter((file) => file.toLowerCase().includes(filter.toLowerCase()))
                .map((file) => (
                  <button
                    type="button"
                    key={file}
                    title={file}
                    disabled={busy || dirty}
                    onClick={() => void open(file)}
                    className={`w-full text-left break-words ${button} ${file === doc?.path ? "bg-paper-deep/40" : ""}`}
                  >
                    {file}
                  </button>
                ))}
            </div>
            <input
              aria-label="新文件路径"
              placeholder="新文件.md"
              className="w-full rounded-lg bg-cloud/60 px-2 py-2 text-xs outline-none"
              disabled={busy || dirty}
              value={newPath}
              onChange={(e) => setNewPath(e.target.value)}
            />
            <button
              type="button"
              className={button}
              disabled={busy || dirty || !config?.baseUrl}
              onClick={create}
            >
              新建文件
            </button>
          </aside>
        )}
        <main className="flex-1 min-w-0 flex flex-col">
          {!doc ? (
            <div className="flex-1 flex items-center justify-center p-8 text-sm text-ink-faint text-center">
              选择一份 Markdown 笔记，或新建文件。
              <br />
              内容直接保存到服务器。
            </div>
          ) : preview ? (
            <div className="flex-1 overflow-auto p-5">
              <MarkdownPreviewLazy content={doc.content} renderImage={renderRemoteImage} />
            </div>
          ) : (
            <textarea
              ref={textareaRef}
              aria-label="远程 Markdown 内容"
              onPaste={(event) => {
                const images = Array.from(event.clipboardData.files).filter((file) =>
                  file.type.startsWith("image/"),
                );
                if (images.length > 0) {
                  event.preventDefault();
                  if (!busyRef.current) void insertImages(images);
                }
              }}
              data-tab-indent="true"
              spellCheck={false}
              disabled={busy}
              className="flex-1 min-h-0 w-full resize-none bg-transparent p-5 text-sm leading-relaxed outline-none font-body"
              value={doc.content}
              onChange={(e) => {
                if (!busyRef.current) changeDoc({ ...doc, content: e.target.value });
              }}
            />
          )}
          {latest && (
            <section className="border-t border-paper-deep/40 p-3 space-y-2 max-h-[45%] flex flex-col">
              <p className="text-xs text-ink-faint">
                服务器当前版本（你的草稿仍在上方，可复制此处内容进行合并）
              </p>
              <textarea
                aria-label="服务器当前版本"
                readOnly
                className="min-h-16 flex-1 resize-none rounded-lg bg-cloud/60 p-2 text-xs"
                value={latest.content}
              />
              <button
                type="button"
                disabled={busy}
                className={button}
                onClick={() => {
                  if (doc) {
                    changeDoc(mergeBase(doc, latest));
                    setLatest(null);
                    setNotice("已采用此服务器版本作为保存基准。请检查上方内容，再点击保存。");
                  }
                }}
              >
                已完成合并，采用此版本为保存基准
              </button>
            </section>
          )}
        </main>
      </div>
      {notice && (
        <p
          role="status"
          className="px-4 py-2 text-xs leading-relaxed border-t border-paper-deep/30 max-h-28 overflow-auto break-words"
        >
          {notice}
        </p>
      )}
      <footer className="flex items-center flex-wrap gap-1 px-3 py-2 border-t border-paper-deep/30 text-ink-faint">
        <span className="text-[10px] flex-1 truncate" title={config?.baseUrl}>
          {config?.baseUrl || "尚未配置连接"}
        </span>
        <button
          type="button"
          disabled={busy || !doc}
          className={button}
          onClick={() => void inspectServer()}
        >
          查看服务器版本
        </button>
        <button
          type="button"
          disabled={!doc}
          className={button}
          onClick={() => {
            if (doc)
              void writeText(doc.content)
                .then(() => setNotice("内容已复制。"))
                .catch((e) => setNotice(getErrorMessage(e)));
          }}
        >
          复制内容
        </button>
        <button
          type="button"
          disabled={busy || !dirty}
          className={button}
          onClick={() => void discard().catch((e) => setNotice(getErrorMessage(e)))}
        >
          放弃更改
        </button>
      </footer>
      <div
        className="absolute bottom-0 right-0 w-3 h-3 cursor-nwse-resize"
        onMouseDown={() => void startCurrentWindowResize()}
      />
    </div>
  );
}
