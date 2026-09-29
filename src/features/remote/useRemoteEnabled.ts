import { useEffect, useState } from "react";
import { getRemoteConfig } from "./api";

/** Whether remote notes are configured. Settings live in the main window, so re-read on focus. */
export function useRemoteEnabled(): boolean {
  const [enabled, setEnabled] = useState(false);

  useEffect(() => {
    const refresh = () => {
      void getRemoteConfig()
        .then((config) => setEnabled(config.enabled && Boolean(config.baseUrl)))
        .catch(() => setEnabled(false));
    };
    const refreshWhenVisible = () => {
      if (document.visibilityState === "visible") refresh();
    };
    refresh();
    window.addEventListener("focus", refresh);
    document.addEventListener("visibilitychange", refreshWhenVisible);
    return () => {
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", refreshWhenVisible);
    };
  }, []);

  return enabled;
}
