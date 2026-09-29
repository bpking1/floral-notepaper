import { useCallback, useEffect, useRef } from "react";

// A task list item: optional blockquote markers, a list marker, then `[ ]` or `[x]`.
const TASK_LINE = /^((?:[ \t]*>)*[ \t]*(?:[-*+]|\d+[.)])[ \t]+\[)([ xX])\]/;

/** Flips the task checkbox on the 1-based `line`, or returns null if that line is not a task. */
export function toggleTaskLine(content: string, line: number): string | null {
  const lines = content.split("\n");
  const match = TASK_LINE.exec(lines[line - 1] ?? "");
  if (!match) return null;
  const [, before, mark] = match;
  lines[line - 1] =
    `${before}${mark === " " ? "x" : " "}${lines[line - 1].slice(before.length + 1)}`;
  return lines.join("\n");
}

interface TaskToggleOptions {
  setContent: (update: (content: string) => string) => void;
  setStatus: (status: "dirty") => void;
  /** Saves the current content; called once the toggled content has rendered. */
  save: () => Promise<unknown>;
}

/**
 * Returns a handler for clicks on rendered task checkboxes. A toggle is saved
 * right away, whatever the auto-save setting: it is a deliberate click, and a
 * tile has no save button.
 */
export function useTaskToggle({ setContent, setStatus, save }: TaskToggleOptions) {
  const saveRef = useRef(save);
  saveRef.current = save;
  const pendingSave = useRef(false);

  useEffect(() => {
    if (!pendingSave.current) return;
    pendingSave.current = false;
    void saveRef.current();
  });

  return useCallback(
    (line: number) => {
      setContent((content) => {
        const next = toggleTaskLine(content, line);
        if (next === null) return content;
        pendingSave.current = true;
        return next;
      });
      setStatus("dirty");
    },
    [setContent, setStatus],
  );
}
