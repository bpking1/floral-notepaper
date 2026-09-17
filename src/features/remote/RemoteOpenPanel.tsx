import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

interface RemoteOpenPanelProps {
  files: string[];
  currentPath: string;
  defaultNewPath: string;
  onOpenFile: (path: string) => void;
  onCreateFile: (path: string) => void;
}

function isValidNewPath(path: string): boolean {
  return (
    !!path &&
    !path.startsWith("/") &&
    !path.includes("\\") &&
    path.split("/").every((part) => part && part !== "." && part !== "..") &&
    /\.(md|markdown)$/i.test(path)
  );
}

export function RemoteOpenPanel({
  files,
  currentPath,
  defaultNewPath,
  onOpenFile,
  onCreateFile,
}: RemoteOpenPanelProps) {
  const { t } = useTranslation();
  const [searchQuery, setSearchQuery] = useState("");
  const [newPath, setNewPath] = useState(defaultNewPath);
  const newPathTouchedRef = useRef(false);

  useEffect(() => {
    if (!newPathTouchedRef.current && defaultNewPath) setNewPath(defaultNewPath);
  }, [defaultNewPath]);

  const filteredFiles = useMemo(() => {
    const query = searchQuery.trim().toLowerCase();
    if (!query) return files;
    return files.filter((file) => file.toLowerCase().includes(query));
  }, [files, searchQuery]);

  const validNewPath = isValidNewPath(newPath);
  const newFileExists = validNewPath && files.includes(newPath);

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
            value={searchQuery}
            onChange={(event) => setSearchQuery(event.target.value)}
            placeholder={t("remote.search.placeholder", { defaultValue: "搜索远程文件…" })}
            className="flex-1 text-[12px] font-body text-ink placeholder:text-ink-ghost/60 bg-transparent outline-none"
          />
          {searchQuery && (
            <button
              type="button"
              onClick={() => setSearchQuery("")}
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

      <div className="p-2 pt-0 flex-1 min-h-0 overflow-y-auto scrollbar-hidden">
        <div className="space-y-0.5">
          {filteredFiles.map((file) => (
            <button
              key={file}
              type="button"
              onClick={() => onOpenFile(file)}
              title={file}
              className={`w-full text-left px-3.5 py-2.5 rounded-xl transition-all duration-200 cursor-pointer group hover:bg-paper-warm/70 ${
                file === currentPath ? "bg-paper-warm/70" : ""
              }`}
            >
              <span
                className={`text-[13px] font-display font-medium truncate block pr-2 transition-colors ${
                  file === currentPath ? "text-bamboo" : "text-ink-soft group-hover:text-ink"
                }`}
              >
                {file}
              </span>
            </button>
          ))}
          {files.length === 0 && (
            <div className="px-4 py-6 text-center text-[12px] text-ink-ghost">
              {t("remote.emptyState", { defaultValue: "服务器上没有找到 Markdown 文件" })}
            </div>
          )}
          {files.length > 0 && filteredFiles.length === 0 && (
            <div className="px-4 py-6 text-center text-[12px] text-ink-ghost">
              {t("notepad.search.noResults", { defaultValue: "没有匹配的笔记" })}
            </div>
          )}
        </div>
      </div>

      <div className="px-3 pb-3 pt-1 shrink-0 flex items-center gap-2">
        <input
          type="text"
          value={newPath}
          onChange={(event) => {
            newPathTouchedRef.current = true;
            setNewPath(event.target.value);
          }}
          placeholder={t("remote.newFile.placeholder", { defaultValue: "新文件.md" })}
          aria-label={t("remote.newFile.aria", { defaultValue: "新文件路径" })}
          className="flex-1 min-w-0 px-2.5 py-1.5 rounded-lg bg-paper-warm/60 border border-paper-deep/30 text-[12px] font-body text-ink placeholder:text-ink-ghost/60 outline-none focus:border-ink-faint"
        />
        <button
          type="button"
          disabled={!validNewPath}
          onClick={() => {
            if (!validNewPath) return;
            if (newFileExists) onOpenFile(newPath);
            else onCreateFile(newPath);
          }}
          title={
            newPath && !validNewPath
              ? t("remote.newFile.invalid", {
                  defaultValue: "需要笔记库内的相对 Markdown 路径，例如 Inbox.md",
                })
              : newFileExists
                ? t("remote.newFile.exists", { defaultValue: "文件已存在，点击打开" })
                : t("remote.newFile.create", { defaultValue: "新建文件（父目录需已存在）" })
          }
          className="px-3 py-1.5 text-[12px] text-cloud bg-bamboo hover:bg-bamboo-light rounded-lg transition-all duration-200 font-medium cursor-pointer disabled:opacity-40 disabled:cursor-default shrink-0"
        >
          {t("remote.newFile.button", { defaultValue: "新建" })}
        </button>
      </div>
    </div>
  );
}
