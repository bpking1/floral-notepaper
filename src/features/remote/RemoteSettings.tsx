import { useEffect, useState } from "react";
import { getErrorMessage } from "../notes/api";
import {
  emptyRemoteConfig,
  getRemoteConfig,
  listRemoteFiles,
  openRemoteWindow,
  saveRemoteConfig,
} from "./api";

export function RemoteSettings() {
  const [config, setConfig] = useState(emptyRemoteConfig);
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [ready, setReady] = useState(false);
  const [notice, setNotice] = useState("");
  useEffect(() => {
    void getRemoteConfig()
      .then((value) => {
        setConfig(value);
        setReady(true);
      })
      .catch((e) => setNotice(getErrorMessage(e)));
  }, []);
  async function save(test: boolean) {
    setBusy(true);
    setNotice("");
    try {
      const saved = await saveRemoteConfig(config, token);
      setConfig(saved);
      setToken("");
      if (test) {
        const { files } = await listRemoteFiles(saved.baseUrl);
        setNotice(`连接成功，找到 ${files.length} 个 Markdown 文件。`);
      } else setNotice("远程笔记设置已保存。");
    } catch (e) {
      setNotice(getErrorMessage(e));
    } finally {
      setBusy(false);
    }
  }
  const inputClass =
    "w-full mt-1 rounded-lg border border-paper-deep/40 bg-paper-warm/50 px-2 py-1.5 text-xs text-ink outline-none focus:border-ink-faint";
  return (
    <section className="space-y-3 border-t border-paper-deep/30 pt-4">
      <h3 className="text-sm font-display text-ink-soft">远程 Markdown 笔记库</h3>
      <fieldset disabled={busy || !ready} className="space-y-3 disabled:opacity-60">
        <label className="flex gap-2 items-center text-xs text-ink-soft">
          <input
            type="checkbox"
            checked={config.enabled}
            onChange={(e) => setConfig({ ...config, enabled: e.target.checked })}
          />
          快捷键和托盘新建入口打开远程便笺
        </label>
        <label className="block text-xs text-ink-faint">
          API 地址
          <input
            className={inputClass}
            placeholder="https://notes.example.com/"
            value={config.baseUrl}
            onChange={(e) => setConfig({ ...config, baseUrl: e.target.value })}
          />
        </label>
        <label className="block text-xs text-ink-faint">
          Token（留空保留此地址已保存的凭据）
          <input
            type="password"
            autoComplete="new-password"
            className={inputClass}
            value={token}
            onChange={(e) => setToken(e.target.value)}
          />
        </label>
        <label className="block text-xs text-ink-faint">
          默认文件（笔记库内相对路径）
          <input
            className={inputClass}
            placeholder="Inbox.md"
            value={config.defaultFile}
            onChange={(e) => setConfig({ ...config, defaultFile: e.target.value })}
          />
        </label>
        <p className="text-[11px] text-ink-faint leading-relaxed">
          Token 保存在系统凭据库。公网连接使用 HTTPS；HTTP 仅用于本机或可信私有网络。
        </p>
        <div className="flex gap-2 text-xs">
          <button
            type="button"
            className="rounded-lg bg-paper-deep/50 px-3 py-2 cursor-pointer"
            onClick={() => void save(false)}
          >
            保存设置
          </button>
          <button
            type="button"
            className="rounded-lg bg-paper-deep/50 px-3 py-2 cursor-pointer"
            onClick={() => void save(true)}
          >
            保存并测试连接
          </button>
        </div>
      </fieldset>
      <button
        type="button"
        disabled={busy || !ready}
        className="text-xs underline text-ink-soft cursor-pointer"
        onClick={() => void openRemoteWindow().catch((e) => setNotice(getErrorMessage(e)))}
      >
        打开远程便笺
      </button>
      {notice && (
        <p role="status" className="text-xs text-ink-soft break-words leading-relaxed">
          {notice}
        </p>
      )}
    </section>
  );
}
