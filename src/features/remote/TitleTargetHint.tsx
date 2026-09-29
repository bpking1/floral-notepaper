import { useCallback, useEffect, useMemo, useState } from "react";
import type { RefObject } from "react";
import { useTranslation } from "react-i18next";
import type { RemoteFileEntry } from "./api";
import { cachedListing, loadServerConfig, refreshListing } from "./remoteListing";
import { pathSuggestions, titleTarget } from "./titleTarget";

const inputValueSetter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set;

interface TitleTargetHintProps {
  /** Whether this note would be sent (remote enabled, a local note being written). */
  active: boolean;
  title: string;
  titleRef: RefObject<HTMLInputElement | null>;
}

/**
 * Shows under the title where Send puts the note (relative titles go under
 * the server's NOTES_NEW_DIR), and for titles starting with `/` suggests
 * remote directories and notes (↑↓ to choose, Tab to fill,
 * Esc to hide). It hooks the title input directly, so NotePad only renders it.
 */
export function TitleTargetHint({ active, title, titleRef }: TitleTargetHintProps) {
  const { t } = useTranslation();
  const [files, setFiles] = useState<RemoteFileEntry[]>(() => cachedListing()?.files ?? []);
  const [focused, setFocused] = useState(false);
  const [highlight, setHighlight] = useState(-1);
  const [dismissedFor, setDismissedFor] = useState<string | null>(null);
  const [newDir, setNewDir] = useState("");
  const titled = active && title.trim() !== "";

  useEffect(() => {
    if (!titled) return undefined;
    let cancelled = false;
    void loadServerConfig()
      .then((config) => {
        if (!cancelled) setNewDir(config.newDir);
      })
      .catch(() => undefined);
    void refreshListing()
      .then((listing) => {
        if (!cancelled) setFiles(listing.files);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [titled]);

  useEffect(() => {
    const input = titleRef.current;
    if (!input || !active) return undefined;
    const onFocus = () => setFocused(true);
    const onBlur = () => setFocused(false);
    setFocused(document.activeElement === input);
    input.addEventListener("focus", onFocus);
    input.addEventListener("blur", onBlur);
    return () => {
      input.removeEventListener("focus", onFocus);
      input.removeEventListener("blur", onBlur);
    };
  }, [active, titleRef]);

  const suggestions = useMemo(
    () => (active && focused && dismissedFor !== title ? pathSuggestions(title, files) : []),
    [active, dismissedFor, files, focused, title],
  );
  useEffect(() => setHighlight(-1), [title]);

  // Goes through the input's own change handling, as if typed.
  const pick = useCallback(
    (value: string) => {
      const input = titleRef.current;
      if (!input) return;
      inputValueSetter?.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
      input.focus();
      input.setSelectionRange(value.length, value.length);
    },
    [titleRef],
  );

  useEffect(() => {
    const input = titleRef.current;
    if (!input || suggestions.length === 0) return undefined;
    // Registered on the input itself, so it runs before NotePad's React
    // handler (Enter/↓ jump to the body) and can take over these keys.
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.isComposing) return;
      const take = () => {
        event.preventDefault();
        event.stopPropagation();
      };
      const count = suggestions.length;
      if (event.key === "ArrowDown") {
        take();
        setHighlight((current) => (current + 1) % count);
      } else if (event.key === "ArrowUp") {
        take();
        setHighlight((current) => (current <= 0 ? count - 1 : current - 1));
      } else if (
        (event.key === "Tab" && !event.shiftKey) ||
        (event.key === "Enter" && highlight >= 0)
      ) {
        take();
        pick(suggestions[Math.max(highlight, 0)].value);
      } else if (event.key === "Escape") {
        take();
        setDismissedFor(title);
      }
    };
    input.addEventListener("keydown", onKeyDown);
    return () => input.removeEventListener("keydown", onKeyDown);
  }, [highlight, pick, suggestions, title, titleRef]);

  const target = active ? titleTarget(title, newDir) : null;
  if (!target) return null;
  const exists = "path" in target && files.some((file) => file.path === target.path);

  return (
    <div className="relative shrink-0 -mt-1.5 mb-1.5">
      <p
        className={`text-[11px] truncate ${"error" in target ? "text-red-400" : "text-ink-ghost"}`}
        title={"error" in target ? target.error : target.path}
      >
        {"error" in target
          ? target.error
          : `→ ${target.path} · ${
              exists
                ? t("remote.target.append", { defaultValue: "追加到已有笔记" })
                : t("remote.target.create", { defaultValue: "新建笔记" })
            }`}
      </p>
      {suggestions.length > 0 && (
        <ul className="absolute left-0 right-0 top-full z-20 mt-1 py-1 max-h-48 overflow-y-auto bg-cloud/95 backdrop-blur-sm border border-paper-deep/50 rounded-lg shadow-[0_4px_16px_rgba(26,26,24,0.08)]">
          {suggestions.map((item, index) => (
            <li key={item.value}>
              <button
                type="button"
                // mousedown keeps focus in the title, which a click would take away.
                onMouseDown={(event) => {
                  event.preventDefault();
                  pick(item.value);
                }}
                className={`w-full text-left px-3 py-1 text-[12px] truncate cursor-pointer ${
                  index === highlight
                    ? "bg-bamboo-mist/60 text-bamboo"
                    : item.isDirectory
                      ? "text-ink-soft hover:bg-paper-warm/70"
                      : "text-ink-faint hover:bg-paper-warm/70"
                }`}
              >
                {item.label}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
