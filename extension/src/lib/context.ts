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

// Header capture lives in swstate so it survives MV3 worker termination —
// an Authorization header captured a minute before the download must still be
// there when the worker wakes up to hand the job to XDL.
export { putCapture as captureRequestHeaders, getCapture as lookupCaptured } from "./swstate";

export function mediaKindFor(url: string, mime?: string): MediaKind | null {
  const m = (mime || "").toLowerCase();
  const ext = extOf(url);
  const lower = url.toLowerCase();
  // Query / path tokens used by many CDNs instead of a file extension.
  if (
    ext === "m3u8" ||
    ext === "m3u" ||
    m.includes("mpegurl") ||
    m.includes("x-mpegurl") ||
    /(?:[?&](?:format|type|ext)=m3u8?\b)|\/(?:playlist|master|index)\.m3u8?\b/i.test(lower)
  ) {
    return "hls";
  }
  if (ext === "mpd" || m.includes("dash+xml") || /\/manifest\.mpd\b|[?&](?:format|type)=mpd\b/i.test(lower)) {
    return "dash";
  }
  if (m === "application/vnd.apple.mpegurl") return "hls";
  if (ext === "ts") {
    // Standalone Transport Stream vs tiny HLS chunks.
    if (/\/(?:seg|chunk|media|fragment)[^/]*\.ts(?:\?|$)/i.test(lower) || /[?&](?:seg|sequence)=/i.test(lower)) {
      return "other";
    }
    return "video";
  }
  if (
    ext === "mp4" ||
    ext === "webm" ||
    ext === "mov" ||
    ext === "mkv" ||
    ext === "flv" ||
    ext === "m4v" ||
    m.startsWith("video/")
  ) {
    return "video";
  }
  if (
    ext === "mp3" ||
    ext === "m4a" ||
    ext === "aac" ||
    ext === "flac" ||
    ext === "opus" ||
    ext === "ogg" ||
    ext === "wav" ||
    m.startsWith("audio/")
  ) {
    return "audio";
  }
  if (ext === "m4s" || ext === "cmfv" || ext === "cmfa" || ext === "cmft") return "other";
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

/* ------------------------------------------------------------------ */
/* download-link heuristics (IDM-style "download all" detection)        */
/* ------------------------------------------------------------------ */

const CRAWLER_UA = /bot|crawl|spider|slurp|bot\b|preview|monitor|uptime|feed|rss|sitemap|whatsapp|telegrambot|discordbot|slackbot|facebookexternalhit|ia_archiver|semrush|ahrefs|mj12|dotbot|petalbot|bytespider|gptbot|claude|ccbot|perplexity/i;

/**
 * Path/query fragments that mark a link as a file endpoint rather than a page.
 * Mirrors what real download managers treat as "this is a file".
 */
const DOWNLOAD_PATH_HINTS = [
  "download", "down", "dl", "attachment", "attach", "getfile", "get", "fetch",
  "export", "file", "files", "asset", "assets", "resource", "media",
  "upload", "uploads", "share", "source", "src", "raw", "view", "serve",
];

/** Query parameters that commonly carry a filename or a file identity. */
const FILE_QUERY_KEYS = [
  "filename", "file", "filepath", "path", "name", "f", "fn", "fnm",
  "attachment", "download", "dl", "src", "source", "target", "title",
  "doc", "document", "media", "resource", "saveas", "save",
];

/** Extensions worth treating as files even if not in BINARY_EXTS. */
const EXTRA_FILE_EXTS = new Set([
  "txt", "json", "csv", "tsv", "xml", "yaml", "yml", "md", "log", "conf",
  "ini", "bat", "cmd", "sh", "ps1", "reg", "inf", "crt", "pem", "key",
  "woff", "woff2", "ttf", "otf", "eot", "m3u", "m3u8", "mpd", "m4s", "ts",
  "vtt", "srt", "ass", "cue", "nfo", "sfv", "url", "lnk", "ics", "vcf",
  "heic", "heif", "avif", "jxl", "bmp", "tiff", "tif", "svg", "ico",
]);

/**
 * Same-document navigation. Following these means "browsing", and hijacking
 * them is the single most damaging false positive.
 *
 * `currentUrl` is passed in because this module is also loaded by the service
 * worker, where there is no `location`.
 */
function isSameDocumentNavigation(href: string, currentUrl: string): boolean {
  try {
    const a = new URL(href);
    const b = new URL(currentUrl);
    if (a.origin !== b.origin) return false;
    const strip = (u: URL) => `${u.origin}${u.pathname}${u.search}`;
    return strip(a) === strip(b);
  } catch {
    return true; // unparseable → treat as unsafe, do not hijack
  }
}

/** A path that looks like a page route rather than a file endpoint. */
function looksLikePageRoute(u: URL): boolean {
  const path = u.pathname;
  // "/" and "/index.html" are pages.
  if (path === "/" || path === "") return true;
  // Trailing slash = directory listing / route.
  if (path.endsWith("/")) return true;
  // Real file endpoints are shallow ("/dl/abc", "/get/file-42"). A deep
  // extensionless path with no filename query is a route, even when it happens
  // to contain a keyword like "file" somewhere along the way.
  const segments = path.split("/").filter(Boolean);
  if (segments.length >= 5 && !hasFileQuery(u)) return true;
  // Long extensionless leaf with no file-ish query → almost certainly a route.
  const leaf = segments[segments.length - 1] || "";
  if (leaf.length > 24 && !hasFileQuery(u)) return true;
  return false;
}

function hasFileQuery(u: URL): boolean {
  for (const k of u.searchParams.keys()) {
    if (FILE_QUERY_KEYS.includes(k.toLowerCase())) return true;
  }
  return false;
}

/** Extensions that are always server-rendered pages, never downloadable files. */
const PAGE_EXTS = new Set([
  "html", "htm", "xhtml", "php", "php3", "php4", "php5", "phtml", "asp",
  "aspx", "jsp", "jspx", "cgi", "pl", "py", "rb", "do", "action", "cfm",
]);

function hasPathHint(u: URL): boolean {
  const lower = u.pathname.toLowerCase();
  for (const h of DOWNLOAD_PATH_HINTS) {
    // Segment-exact ("/download/" or "/download"), or clearly delimited
    // ("/download-1", "/dl_2"). The bare-endpoint case matters: real download
    // links are frequently "/download?id=8823", where the path *is* the hint.
    if (
      lower.includes(`/${h}/`) ||
      lower.includes(`/${h}-`) ||
      lower.includes(`/${h}_`) ||
      lower.includes(`-${h}/`) ||
      lower.endsWith(`/${h}`)
    ) {
      return true;
    }
  }
  return false;
}

export interface LinkVerdict {
  /**
   * "definite"  — file extension, download attribute, or strong endpoint hint.
   * "probe"     — looks like a file endpoint but unproven; caller may verify
   *               with a cheap request before committing.
   * "ignore"    — page navigation, in-page anchor, or non-http scheme.
   */
  kind: "definite" | "probe" | "ignore";
  reason: string;
  ext: string;
}

/**
 * Classifies a candidate download link the way a download manager does.
 *
 * The previous implementation required an exact extension whitelist or a
 * `download` attribute, which meant real endpoints like `/download?id=8823`,
 * `/api/export`, or `/get/abc` fell through untouched and users saw the
 * extension simply "not working". This trades a little extra probing for
 * coverage; `probe` verdicts let the caller confirm cheaply before hijacking.
 */
export function classifyLink(
  rawHref: string,
  opts: { hasDownloadAttr?: boolean; currentUrl?: string } = {},
): LinkVerdict {
  const href = rawHref.split("#")[0] ?? "";
  if (!href) return { kind: "ignore", reason: "empty", ext: "" };
  if (!isHTTPUrl(href)) return { kind: "ignore", reason: "non-http scheme", ext: "" };
  if (opts.currentUrl && isSameDocumentNavigation(href, opts.currentUrl)) {
    return { kind: "ignore", reason: "same page", ext: "" };
  }

  let u: URL;
  try {
    u = new URL(href);
  } catch {
    return { kind: "ignore", reason: "unparseable", ext: "" };
  }

  const ext = extOf(href);

  // 1. Explicit user intent.
  if (opts.hasDownloadAttr) {
    return { kind: "definite", reason: "download attribute", ext };
  }

  // 2. Extensions that are always a page render, checked BEFORE the generic
  //    extension rules — otherwise the "unknown extension" branch below would
  //    happily classify /index.html as a definite file.
  if (PAGE_EXTS.has(ext)) {
    return { kind: "ignore", reason: "page extension", ext };
  }

  // 3. Known file extension. Covers BINARY_EXTS plus text/asset types.
  if (ext && (BINARY_EXTS.has(ext) || EXTRA_FILE_EXTS.has(ext))) {
    return { kind: "definite", reason: `file extension .${ext}`, ext };
  }

  // 4. Unrecognised extension (e.g. .xyz, .dmg42) — lean definite.
  if (ext && ext.length >= 2 && ext.length <= 8 && /^[a-z0-9]+$/.test(ext)) {
    return { kind: "definite", reason: `unknown extension .${ext}`, ext };
  }

  // 5. No usable extension: fall back to path/query shape.
  if (hasFileQuery(u)) {
    return { kind: "definite", reason: "filename query parameter", ext };
  }
  // The route check must precede the path-hint check: a long route like
  // /a/long/path/that/mentions/file would otherwise be hijacked by the "file"
  // keyword and take the user off the site.
  if (looksLikePageRoute(u)) {
    return { kind: "ignore", reason: "page route", ext };
  }
  if (hasPathHint(u)) {
    return { kind: "probe", reason: "download endpoint path", ext };
  }

  return { kind: "ignore", reason: "no download signal", ext };
}

/** True when the current page looks like a crawler; used to stay out of the way. */
export function isCrawlerContext(): boolean {
  try {
    return CRAWLER_UA.test(navigator.userAgent || "");
  } catch {
    return false;
  }
}

/**
 * Suffix extensions useful for bulk link collection (content scan).
 * Broader than BINARY_EXTS on purpose — the caller filters the rest.
 */
export function isCollectableExt(ext: string): boolean {
  if (!ext) return false;
  return BINARY_EXTS.has(ext) || EXTRA_FILE_EXTS.has(ext);
}

/**
 * True only for MIMEs that are really a rendered document. A browser navigating
 * to `text/html` or `application/xhtml+xml` is browsing, not downloading, so
 * those must not be hijacked.
 *
 * `text/plain` and `application/json` are deliberately NOT included: plenty of
 * sites serve .txt / .json / .csv attachments with those types, and treating
 * them as pages silently killed every one of those downloads.
 */
export function isHTMLMime(mime?: string): boolean {
  if (!mime) return false;
  const m = (mime.toLowerCase().split(";")[0] ?? "").trim();
  return m === "text/html" || m === "application/xhtml+xml" || m === "application/xhtml";
}
