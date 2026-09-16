import { useEffect, useMemo, useRef, useState } from "react";
import { getErrorMessage } from "../notes/api";
import { readRemoteImage } from "./api";
import { rasterImageMime, resolveRemoteImageSource } from "./imagePaths";

const pending: Array<() => void> = [];
let activeRequests = 0;

function drainQueue() {
  while (activeRequests < 4 && pending.length) pending.shift()!();
}

/** Owns one image's request and blob URL, including cancellation while queued or in flight. */
export function loadRemoteImage(
  baseUrl: string,
  path: string,
  onLoad: (url: string) => void,
  onError: (message: string) => void,
): () => void {
  let cancelled = false;
  let objectUrl: string | undefined;
  const start = () => {
    activeRequests += 1;
    void Promise.resolve()
      .then(async () => {
        if (cancelled) return;
        const data: ArrayBuffer | number[] = await readRemoteImage(baseUrl, path);
        if (cancelled) return;
        const bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : new Uint8Array(data);
        const mime = rasterImageMime(bytes);
        if (!mime) throw new Error("不支持的图片格式。支持 PNG、JPEG、GIF、WebP 和 BMP。");
        objectUrl = URL.createObjectURL(new Blob([bytes], { type: mime }));
        onLoad(objectUrl);
      })
      .catch((error) => {
        if (!cancelled) onError(getErrorMessage(error));
      })
      .finally(() => {
        activeRequests -= 1;
        drainQueue();
      });
  };
  pending.push(start);
  drainQueue();
  return () => {
    cancelled = true;
    const index = pending.indexOf(start);
    if (index !== -1) pending.splice(index, 1);
    if (objectUrl) {
      URL.revokeObjectURL(objectUrl);
      objectUrl = undefined;
    }
  };
}

interface RemoteImageProps {
  baseUrl: string;
  notePath: string;
  src?: string;
  alt?: string;
  title?: string;
}

interface ImageState {
  key: string;
  url?: string;
  error?: string;
}

export function RemoteImage({ baseUrl, notePath, src, alt = "", title }: RemoteImageProps) {
  const source = useMemo(() => resolveRemoteImageSource(notePath, src), [notePath, src]);
  const path = source.kind === "remote" ? source.path : undefined;
  const key = JSON.stringify([baseUrl, source]);
  const [image, setImage] = useState<ImageState | null>(null);
  const [attempt, setAttempt] = useState(0);
  const placeholder = useRef<HTMLSpanElement>(null);
  const [visible, setVisible] = useState(() => typeof IntersectionObserver === "undefined");

  useEffect(() => {
    if (!path || visible || !placeholder.current) return;
    let cancelled = false;
    const observer = new IntersectionObserver(
      (entries) => {
        if (!cancelled && entries.some((entry) => entry.isIntersecting)) {
          setVisible(true);
          observer.disconnect();
        }
      },
      { rootMargin: "240px" },
    );
    observer.observe(placeholder.current);
    return () => {
      cancelled = true;
      observer.disconnect();
    };
  }, [path, visible]);

  useEffect(() => {
    if (!path || !visible) return;
    setImage({ key });
    return loadRemoteImage(
      baseUrl,
      path,
      (url) => setImage({ key, url }),
      (error) => setImage({ key, error }),
    );
  }, [baseUrl, path, key, attempt, visible]);

  const current = image?.key === key ? image : null;
  const error = source.kind === "invalid" ? source.reason : current?.error;
  const url = source.kind === "external" ? source.url : current?.url;
  if (error) {
    return (
      <span className="block my-2 rounded-lg border border-paper-deep/50 p-3 text-xs text-ink-faint">
        <span role="status">
          {alt ? `${alt}：` : "图片加载失败："}
          {error}
        </span>
        {source.kind !== "invalid" && (
          <button
            type="button"
            className="ml-2 text-bamboo underline cursor-pointer"
            onClick={() => {
              setImage({ key });
              setAttempt((value) => value + 1);
            }}
          >
            重试
          </button>
        )}
      </span>
    );
  }
  if (!url) {
    return (
      <span ref={placeholder} role="status" className="block my-2 text-xs text-ink-faint">
        正在加载图片{alt ? `：${alt}` : "…"}
      </span>
    );
  }
  return (
    <img
      key={`${key}:${attempt}`}
      src={url}
      alt={alt}
      title={title}
      loading="lazy"
      className="w-[50%] rounded my-2 mx-auto block"
      onError={() => setImage({ key, error: "无法显示图片，请检查文件是否完整。" })}
    />
  );
}
