import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { showToast } from "../../components/Toast";
import { getErrorMessage } from "../notes/api";

// The clipboard reliably accepts only PNG images.
async function toPng(blob: Blob): Promise<Blob> {
  if (blob.type === "image/png") return blob;
  const bitmap = await createImageBitmap(blob);
  const canvas = document.createElement("canvas");
  canvas.width = bitmap.width;
  canvas.height = bitmap.height;
  canvas.getContext("2d")?.drawImage(bitmap, 0, 0);
  bitmap.close();
  return new Promise((resolve, reject) =>
    canvas.toBlob((png) => (png ? resolve(png) : reject(new Error("PNG 转换失败"))), "image/png"),
  );
}

async function copyImage(src: string): Promise<void> {
  const response = await fetch(src);
  if (!response.ok) throw new Error(`HTTP ${response.status}`);
  const png = await toPng(await response.blob());
  await navigator.clipboard.write([new ClipboardItem({ "image/png": png })]);
}

/**
 * Right-clicking an image in a tile copies it. Runs before the app's context
 * menu, which still opens everywhere else (and Ctrl+right-click still closes).
 */
export function useTileImageCopy() {
  const { t } = useTranslation();

  useEffect(() => {
    const handleContextMenu = (event: MouseEvent) => {
      const image = event.target instanceof HTMLImageElement ? event.target : null;
      if (!image || event.ctrlKey || !image.closest('[data-context-menu="tile"]')) return;
      event.preventDefault();
      event.stopPropagation();
      void copyImage(image.currentSrc || image.src)
        .then(() => showToast(t("remote.imageCopied", { defaultValue: "图片已复制" }), "info"))
        .catch((error) =>
          showToast(
            `${t("remote.error.copyImage", { defaultValue: "复制图片失败" })}：${getErrorMessage(error)}`,
          ),
        );
    };
    window.addEventListener("contextmenu", handleContextMenu, true);
    return () => window.removeEventListener("contextmenu", handleContextMenu, true);
  }, [t]);
}
