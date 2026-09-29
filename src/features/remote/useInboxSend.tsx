import { useCallback, useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { showToast } from "../../components/Toast";
import { getErrorMessage } from "../notes/api";
import type { Note } from "../notes/types";
import { sendNoteToInbox } from "./inbox";
import { useRemoteEnabled } from "./useRemoteEnabled";

interface InboxSendOptions {
  /** Only a note being written in the pad can be sent. */
  canSend: boolean;
  title: string;
  content: string;
  saveNote: () => Promise<Note>;
  onSent: () => void;
}

/**
 * "Send to remote inbox" for the quick-note pad: `Ctrl+Enter` or the returned
 * `send`. Kept outside NotePad so upstream changes there merge cleanly.
 */
export function useInboxSend(options: InboxSendOptions) {
  const enabled = useRemoteEnabled();
  const sendingRef = useRef(false);
  const optionsRef = useRef(options);
  optionsRef.current = options;

  // Keeps a local copy first, so a failed send leaves the note in place.
  const send = useCallback(async () => {
    const { canSend, title, content, saveNote, onSent } = optionsRef.current;
    if (sendingRef.current || !enabled || !canSend) return;
    if (!title.trim() && !content.trim()) return;
    sendingRef.current = true;
    try {
      await sendNoteToInbox(await saveNote());
      onSent();
    } catch (error) {
      showToast(getErrorMessage(error));
    } finally {
      sendingRef.current = false;
    }
  }, [enabled]);

  const sendRef = useRef(send);
  sendRef.current = send;

  useEffect(() => {
    function handleKeyDown(event: KeyboardEvent) {
      if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
        event.preventDefault();
        void sendRef.current();
      }
    }

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, []);

  return { enabled, active: enabled && options.canSend, send };
}

export function InboxSendButton({ onSend }: { onSend: () => void }) {
  const { t } = useTranslation();
  return (
    <button
      onClick={onSend}
      title={t("notepad.tooltip.send", { defaultValue: "发送到远程 Inbox（Ctrl+Enter）" })}
      className="px-3 py-1.5 text-[12px] text-bamboo border border-bamboo/60 hover:bg-bamboo hover:text-cloud rounded-lg transition-all duration-200 font-medium cursor-pointer"
    >
      {t("notepad.button.send", { defaultValue: "发送" })}
    </button>
  );
}
