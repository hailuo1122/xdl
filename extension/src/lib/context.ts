import type { MediaCandidate, MediaKind } from "./types";

export const BINARY_EXTS = new Set([
  "zip",
  "7z",
  "rar",
  "tar",
  "gz",
  "tgz",
  "bz2",
  "xz",
  "zst",
  "exe",
  "msi",
  "msix",
  "appx",
  "dmg",
  "pkg",
  "iso",
  "img",
  "bin",
  "apk",
  "deb",
  "rpm",
  "mp4",
  "mkv",
  "avi",
  "mov",
  "webm",
  "flv",
  "wmv",
  "mp3",
  "flac",
  "wav",
  "m4a",
  "aac",
  "ogg",
  "opus",
  "doc",
  "docx",
  "xls",
  "xlsx",
  "ppt",
  "pptx",
  "epub",
  "mobi",
  "psd",
  "blend",
  "sqlite",
  "db",
]);

export function isHTTPUrl(raw: string): boolean {
  return /^https?:\/\//i.test(raw);
}

export function extOf(url: string): string {
  try {
    const p = new URL(url).pathname;
    const leaf = p.split("/").pop() || "";
    const i = leaf.lastIndexOf(".");
    return i > 0 ? leaf.slice(i + 1).toLowerCase() : "";
  } catch {
    return "";
  }
}

export function baseName(raw: string | undefined): string {
  if (!raw) return "";
  const norm = raw.replace(/\\/g, "/");
  const leaf = norm.split("/").filter(Boolean).pop() || "";
  try {
    return decodeURIComponent(leaf).replace(/[<>:"/\\|?*\x00-\x1f]/g, "_").slice(0, 180);
  } catch {
    return leaf.slice(0, 180);
  }
}

export async function cookieHeaderFor(url: string, pageUrl?: string): Promise<string> {
  const cookies: chrome.cookies.Cookie[] = [];
  const seen = new Set<string>();
  const add = (list: chrome.cookies.Cookie[]) => {
    for (const c of list) {
      const key = `${c.name}|${c.domain}|${c.path}|${c.partitionKey?.topLevelSite || ""}`;
      if (seen.has(key)) continue;
      if (c.expirationDate && c.expirationDate * 1000 < Date.now()) continue;
      seen.add(key);
      cookies.push(c);
    }
  };
  try {
    add(await chrome.cookies.getAll({ url }));
  } catch {
    /* cookie store may be unavailable */
  }
  if (pageUrl) {
    try {
      const top = new URL(pageUrl).origin;
      const partitioned = await chrome.cookies.getAll({
        url,
        partitionKey: { topLevelSite: top },
      } as chrome.cookies.GetAllDetails);
      add(partitioned);
    } catch {
      /* CHIPS unsupported or no partition */
    }
  }
  cookies.sort((a, b) => b.path.length - a.path.length || a.name.localeCompare(b.name));
  const parts: string[] = [];
  let budget = 24 * 1024;
  for (const c of cookies) {
    const part = `${c.name}=${c.value}`;
    if (part.length > budget) break;
    budget -= part.length + 2;
    parts.push(part);
  }
  return parts.join("; ");
}

export interface CapturedHeaders {
  referer?: string;
  origin?: string;
  authorization?: string;
  at: number;
}

const captures = new Map<string, CapturedHeaders>();

function captureKeys(tabId: number, url: string): string[] {
  const keys = [`${tabId}|${url}`];
  try {
    const u = new URL(url);
    keys.push(`${tabId}|${u.origin}${u.pathname}`);
  } catch {
    /* ignore */
  }
  return keys;
}

export function captureRequestHeaders(
  tabId: number,
  url: string,
  headers: { referer?: string; origin?: string; authorization?: string },
) {
  if (!headers.referer && !headers.origin && !headers.authorization) return;
  const entry: CapturedHeaders = { ...headers, at: Date.now() };
  for (const k of captureKeys(tabId, url)) captures.set(k, entry);
  if (captures.size > 240) {
    const cutoff = Date.now() - 3 * 60 * 1000;
    for (const [k, v] of captures) {
      if (v.at < cutoff) captures.delete(k);
    }
    if (captures.size > 300) {
      const it = captures.keys();
      for (let i = 0; i < 60; i++) {
        const k = it.next().value;
        if (k) captures.delete(k);
      }
    }
  }
}

export function lookupCaptured(tabId: number | undefined, url: string): CapturedHeaders | undefined {
  if (tabId == null) return undefined;
  for (const k of captureKeys(tabId, url)) {
    const e = captures.get(k);
    if (e && Date.now() - e.at < 3 * 60 * 1000) return e;
  }
  return undefined;
}

export function mediaKindFor(url: string, mime?: string): MediaKind | null {
  const m = (mime || "").toLowerCase();
  const ext = extOf(url);
  if (ext === "m3u8" || m.includes("mpegurl")) return "hls";
  if (ext === "mpd" || m.includes("dash+xml")) return "dash";
  if (ext === "mp4" || ext === "webm" || ext === "mov" || m.startsWith("video/")) return "video";
  if (ext === "mp3" || ext === "m4a" || ext === "aac" || ext === "flac" || m.startsWith("audio/")) return "audio";
  if (ext === "m4s") return "other";
  return null;
}

export function isMediaURL(url: string): boolean {
  return mediaKindFor(url) !== null;
}

export function mediaCandidate(url: string, opts: { mime?: string; size?: number; pageUrl?: string; kind?: MediaKind } = {}): MediaCandidate | null {
  if (!isHTTPUrl(url)) return null;
  const kind = opts.kind || mediaKindFor(url, opts.mime);
  if (!kind) return null;
  const id = url.split("#")[0] ?? url;
  return {
    id,
    url: id,
    kind,
    mime: opts.mime,
    size: opts.size && opts.size > 0 ? opts.size : undefined,
    pageUrl: opts.pageUrl,
    ts: Date.now(),
  };
}

export function formatBytes(n?: number): string {
  if (!n || n <= 0) return "";
  const units = ["B", "KB", "MB", "GB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? Math.round(v) : v.toFixed(1)} ${units[i]}`;
}

export function isHTMLMime(mime?: string): boolean {
  if (!mime) return false;
  const m = mime.toLowerCase();
  return (
    m.startsWith("text/html") ||
    m.startsWith("application/xhtml") ||
    m === "application/json" ||
    m.startsWith("text/plain")
  );
}
