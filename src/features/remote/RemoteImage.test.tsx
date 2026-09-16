import { renderToStaticMarkup } from "react-dom/server";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { readRemoteImage } from "./api";
import { loadRemoteImage, RemoteImage } from "./RemoteImage";

vi.mock("./api", () => ({ readRemoteImage: vi.fn() }));

const png = () => new Uint8Array([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]).buffer;
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
const cleanups: Array<() => void> = [];

beforeEach(() => {
  vi.mocked(readRemoteImage).mockReset();
  vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:remote-image");
  vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
});
afterEach(async () => {
  cleanups.splice(0).forEach((cleanup) => cleanup());
  await flush();
  vi.restoreAllMocks();
});

describe("remote image resource lifecycle", () => {
  test("uses the authenticated reader and revokes its blob URL when disposed", async () => {
    vi.mocked(readRemoteImage).mockResolvedValue(png());
    const loaded = vi.fn(),
      failed = vi.fn();
    const dispose = loadRemoteImage("https://notes.test", "images/photo.png", loaded, failed);
    cleanups.push(dispose);
    await flush();

    expect(readRemoteImage).toHaveBeenCalledWith("https://notes.test", "images/photo.png");
    expect(loaded).toHaveBeenCalledWith("blob:remote-image");
    expect(failed).not.toHaveBeenCalled();
    expect(vi.mocked(URL.createObjectURL).mock.calls[0][0]).toMatchObject({ type: "image/png" });
    dispose();
    expect(URL.revokeObjectURL).toHaveBeenCalledExactlyOnceWith("blob:remote-image");
  });

  test("ignores an in-flight response after a source change or unmount", async () => {
    let finish!: (value: ArrayBuffer) => void;
    vi.mocked(readRemoteImage).mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    const loaded = vi.fn(),
      failed = vi.fn();
    const dispose = loadRemoteImage("https://notes.test", "old.png", loaded, failed);
    cleanups.push(dispose);
    await flush();
    dispose();
    finish(png());
    await flush();

    expect(loaded).not.toHaveBeenCalled();
    expect(failed).not.toHaveBeenCalled();
    expect(URL.createObjectURL).not.toHaveBeenCalled();
  });

  test("bounds requests to four and removes cancelled queued images", async () => {
    const finishes: Array<(value: ArrayBuffer) => void> = [];
    vi.mocked(readRemoteImage).mockImplementation(
      () =>
        new Promise((resolve) => {
          finishes.push(resolve);
        }),
    );
    const loaded = vi.fn(),
      failed = vi.fn();
    const disposers = Array.from({ length: 6 }, (_, index) =>
      loadRemoteImage("https://notes.test", `${index}.png`, loaded, failed),
    );
    cleanups.push(...disposers);
    await flush();
    expect(readRemoteImage).toHaveBeenCalledTimes(4);
    disposers[4]();
    finishes[0](png());
    await flush();
    expect(readRemoteImage).toHaveBeenCalledTimes(5);
    expect(readRemoteImage).toHaveBeenLastCalledWith("https://notes.test", "5.png");
    finishes.slice(1).forEach((finish) => finish(png()));
    await flush();
    expect(loaded).toHaveBeenCalledTimes(5);
    expect(failed).not.toHaveBeenCalled();
  });

  test("reports failed responses and rejects non-raster bodies", async () => {
    const loaded = vi.fn(),
      failed = vi.fn();
    vi.mocked(readRemoteImage).mockRejectedValueOnce(new Error("图片不存在"));
    cleanups.push(loadRemoteImage("https://notes.test", "missing.png", loaded, failed));
    await flush();
    expect(failed).toHaveBeenLastCalledWith("图片不存在");
    vi.mocked(readRemoteImage).mockResolvedValueOnce(new TextEncoder().encode("<svg/>").buffer);
    cleanups.push(loadRemoteImage("https://notes.test", "fake.png", loaded, failed));
    await flush();
    expect(failed).toHaveBeenLastCalledWith(expect.stringContaining("不支持的图片格式"));
    expect(loaded).not.toHaveBeenCalled();
    expect(URL.createObjectURL).not.toHaveBeenCalled();
  });
});

describe("RemoteImage rendering", () => {
  test("keeps external images direct and never puts the API URL in their source", () => {
    const html = renderToStaticMarkup(
      <RemoteImage
        baseUrl="https://private-api.test"
        notePath="notes/a.md"
        src="https://public.test/picture.png"
        alt="花"
        title="花园"
      />,
    );
    expect(html).toContain('src="https://public.test/picture.png"');
    expect(html).toContain('alt="花"');
    expect(html).toContain('title="花园"');
    expect(html).not.toContain("private-api");
    expect(readRemoteImage).not.toHaveBeenCalled();
  });

  test("renders a readable placeholder for an unsupported image URL", () => {
    const html = renderToStaticMarkup(
      <RemoteImage baseUrl="https://private-api.test" notePath="a.md" src="file:///private.png" />,
    );
    expect(html).toContain("图片加载失败");
    expect(html).not.toContain("<img");
    expect(readRemoteImage).not.toHaveBeenCalled();
  });
});
