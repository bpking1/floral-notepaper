import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { showToast } from "../../components/Toast";
import { insertTextAtCursor } from "../images/useImagePaste";
import { createNote, getErrorMessage } from "../notes/api";
import { uploadRemoteImage } from "./api";
import {
  REMOTE_CONFLICT_EVENT,
  adoptRemoteFile,
  fetchRemoteFile,
  isRemoteNoteId,
  remoteFileTitle,
  remoteNotePath,
  syncedRemoteContent,
} from "./remoteNotes";

interface RemoteNoteOptions {
  noteId: string | null;
  title: string;
  content: string;
  setContent: (content: string) => void;
  setStatus: (status: "opened" | "dirty") => void;
}

function imageFiles(data: DataTransfer | null): File[] {
  return Array.from(data?.items ?? [])
    .filter((item) => item.kind === "file" && item.type.startsWith("image/"))
    .map((item) => item.getAsFile())
    .filter((file): file is File => file !== null);
}

/**
 * Keeps a pad that edits a `remote:` file in step with the server: reloads it
 * on focus when there are no unsaved edits, and on a save conflict keeps the
 * draft as a local note and loads the server version. Pasted or dropped
 * images are uploaded to the server's attachments directory.
 */
export function useRemoteNote({
  noteId,
  title,
  content,
  setContent,
  setStatus,
}: RemoteNoteOptions) {
  const { t } = useTranslation();
  const latest = useRef({ noteId, title, content });
  latest.current = { noteId, title, content };
  const isRemote = isRemoteNoteId(noteId);

  useEffect(() => {
    if (!isRemote) return undefined;

    const refresh = () => {
      const { noteId: id, content: before } = latest.current;
      // Unsaved edits: leave them alone; a save will detect any conflict.
      if (!isRemoteNoteId(id) || before !== syncedRemoteContent(id)) return;
      void fetchRemoteFile(id)
        .then((doc) => {
          const now = latest.current;
          if (now.noteId !== id || now.content !== before) return;
          adoptRemoteFile(id, doc);
          if (doc.content !== before) {
            setContent(doc.content);
            setStatus("opened");
          }
        })
        .catch(() => undefined);
    };

    const resolveConflict = async (id: string) => {
      const doc = await fetchRemoteFile(id);
      const draft = latest.current;
      if (draft.noteId !== id) return;
      if (doc.content === draft.content) {
        adoptRemoteFile(id, doc);
        setStatus("opened");
        return;
      }
      const copy = await createNote({
        title: `${draft.title || remoteFileTitle(remoteNotePath(id))}（冲突副本）`,
        content: draft.content,
        category: "",
      });
      adoptRemoteFile(id, doc);
      setContent(doc.content);
      setStatus("opened");
      showToast(
        t("remote.conflictResolved", {
          defaultValue:
            "服务器上的文件已被修改，已载入最新版本；你的修改已另存为本地便笺「{{title}}」。",
          title: copy.title,
        }),
        "warning",
      );
    };

    const handleConflict = (event: Event) => {
      const id = (event as CustomEvent<string>).detail;
      if (id !== latest.current.noteId) return;
      void resolveConflict(id).catch((error) => showToast(getErrorMessage(error)));
    };

    const uploadImages = async (
      id: string,
      textarea: HTMLTextAreaElement,
      files: File[],
      named: boolean,
    ) => {
      const links: string[] = [];
      try {
        for (const file of files) {
          const data = new Uint8Array(await file.arrayBuffer());
          // Clipboard images are all called "image.png"; only dropped files have real names.
          const { markdownPath } = await uploadRemoteImage(
            remoteNotePath(id),
            data,
            named ? file.name : "",
          );
          links.push(`![](${markdownPath})`);
        }
      } catch (error) {
        showToast(
          `${t("remote.error.images", { defaultValue: "远程图片上传失败" })}：${getErrorMessage(error)}`,
        );
      }
      // Insert what did upload, unless the pad has moved on to another note.
      if (links.length === 0 || latest.current.noteId !== id) return;
      insertTextAtCursor(textarea, setContent, links.join("\n"));
      setStatus("dirty");
    };

    // Runs before React's handlers, so images never reach the local image store.
    const handleImages = (event: ClipboardEvent | DragEvent) => {
      const files = imageFiles("clipboardData" in event ? event.clipboardData : event.dataTransfer);
      const id = latest.current.noteId;
      if (files.length === 0 || !isRemoteNoteId(id)) return;
      event.preventDefault();
      event.stopPropagation();
      if (event.target instanceof HTMLTextAreaElement) {
        void uploadImages(id, event.target, files, event.type === "drop");
      }
    };

    window.addEventListener("focus", refresh);
    window.addEventListener(REMOTE_CONFLICT_EVENT, handleConflict);
    window.addEventListener("paste", handleImages, true);
    window.addEventListener("drop", handleImages, true);
    return () => {
      window.removeEventListener("focus", refresh);
      window.removeEventListener(REMOTE_CONFLICT_EVENT, handleConflict);
      window.removeEventListener("paste", handleImages, true);
      window.removeEventListener("drop", handleImages, true);
    };
  }, [isRemote, setContent, setStatus, t]);
}
