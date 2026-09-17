import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";

interface RemoteOpenPanelProps {
  files: string[];
  currentPath: string | null;
  onOpenFile: (path: string) => void;
}

function baseName(path: string): string {
  const index = path.lastIndexOf("/");
  return index === -1 ? path : path.slice(index + 1);
}

export function RemoteOpenPanel({ files, currentPath, onOpenFile }: RemoteOpenPanelProps) {
  const { t } = useTranslation();
  const [searchQuery, setSearchQuery] = useState("");
  const [hoveredFile, setHoveredFile] = useState<string | null>(null);

  const filteredFiles = useMemo(() => {
    const query = searchQuery.trim().toLowerCase();
    if (!query) return files;
    return files.filter((file) => file.toLowerCase().includes(query));
  }, [files, searchQuery]);

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
              onMouseEnter={() => setHoveredFile(file)}
              onMouseLeave={() => setHoveredFile(null)}
              title={file}
              className={`w-full text-left px-3.5 py-3 rounded-xl transition-all duration-200 cursor-pointer group hover:bg-paper-warm/70 ${
                file === currentPath ? "bg-paper-warm/70" : ""
              }`}
            >
              <div className="flex items-center justify-between mb-0.5">
                <span
                  className={`text-[13px] font-display font-medium truncate pr-2 transition-colors ${
                    file === currentPath
                      ? "text-bamboo"
                      : "text-ink-soft group-hover:text-ink"
                  }`}
                >
                  {baseName(file)}
                </span>
              </div>
              <p className="text-[12px] text-ink-ghost leading-relaxed truncate group-hover:text-ink-faint transition-colors">
                {file}
              </p>
              {hoveredFile === file && (
                <div className="mt-1.5 h-px bg-bamboo/10 transition-all duration-300" />
              )}
            </button>
          ))}
          {files.length === 0 && (
            <div className="px-4 py-8 text-center text-[12px] text-ink-ghost">
              {t("remote.emptyState", { defaultValue: "服务器上没有找到 Markdown 文件" })}
            </div>
          )}
          {files.length > 0 && filteredFiles.length === 0 && (
            <div className="px-4 py-8 text-center text-[12px] text-ink-ghost">
              {t("notepad.search.noResults", { defaultValue: "没有匹配的笔记" })}
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
