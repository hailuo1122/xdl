/**
 * Standalone checks for the download-link classifier and MIME gate.
 *
 * Run with:
 *   cd extension && ./node_modules/.bin/tsc src/lib/context.ts \
 *     --outDir <tmp> --module esnext --target es2022 --moduleResolution bundler --skipLibCheck
 *   node <tmp>/context.test.mjs
 *
 * The classifier decides whether a click is hijacked, so a false positive
 * breaks browsing and a false negative is the "extension does nothing" bug.
 */

import { classifyLink, isHTMLMime } from "./context.js";

const CUR = "https://example.com/page/index.html";
let pass = 0;
let fail = 0;
const failures = [];

function v(href, opts = {}) {
  return classifyLink(href, { currentUrl: CUR, ...opts });
}

function eq(label, actual, expected) {
  if (actual === expected) {
    pass++;
  } else {
    fail++;
    failures.push(`${label}: expected ${expected}, got ${actual} (${v && ""}${classifyLink.name})`);
  }
}

/* 1. Definite file extensions */
for (const ext of [
  "zip", "rar", "7z", "exe", "dmg", "iso", "mp4", "mkv", "mp3", "flac",
  "jpg", "png", "epub", "pdf", "apk", "woff2",
]) {
  eq(`ext .${ext}`, v(`https://cdn.example.com/file.${ext}`).kind, "definite");
}

/* 2. Attachments that isHTMLMime used to swallow */
for (const ext of ["txt", "json", "csv", "xml", "log", "md", "srt", "vtt", "m3u8", "ts"]) {
  eq(`attachment .${ext}`, v(`https://example.com/a.${ext}`).kind, "definite");
}

/* 3. Explicit download attribute wins */
eq(
  "download attr on opaque endpoint",
  v("https://example.com/opaque-endpoint", { hasDownloadAttr: true }).kind,
  "definite",
);

/* 4. Filename query parameters */
eq("?file=", v("https://example.com/get?file=report.pdf").kind, "definite");
eq("?filename=", v("https://example.com/view?filename=x.zip").kind, "definite");

/* 5. The real-world cases the old whitelist dropped */
for (const href of [
  "https://example.com/download?id=8823",
  "https://example.com/dl/abc123",
  "https://example.com/get/file-42",
  "https://example.com/api/export-report",
]) {
  eq(`endpoint ${href}`, v(href).kind, "probe");
}

/* 6. Same-document navigation must never be hijacked */
eq("same page + query", v("https://example.com/page/index.html?q=1").kind, "ignore");
eq("same page + hash", v("https://example.com/page/index.html#anchor").kind, "ignore");

/* 7. Page routes */
eq("root", v("https://example.com/").kind, "ignore");
eq("directory", v("https://example.com/docs/intro/").kind, "ignore");
eq(
  "long route",
  v("https://example.com/some/very/long/route/name/that/is/not/a/file").kind,
  "ignore",
);

/* 8. Server-side page extensions */
eq(".html", v("https://example.com/index.html").kind, "ignore");
eq(".php", v("https://example.com/page.php").kind, "ignore");
eq(".aspx", v("https://example.com/report.aspx").kind, "ignore");

/* 9. Non-http schemes */
eq("blob:", v("blob:https://example.com/abc").kind, "ignore");
eq("javascript:", v("javascript:void(0)").kind, "ignore");
eq("mailto:", v("mailto:a@b.com").kind, "ignore");
eq("data:", v("data:text/plain,hello").kind, "ignore");
eq("empty", v("").kind, "ignore");

/* 10. MIME gate */
eq("text/html", isHTMLMime("text/html"), true);
eq("text/html; charset", isHTMLMime("text/html; charset=utf-8"), true);
eq("xhtml+xml", isHTMLMime("application/xhtml+xml"), true);
eq("text/plain is a file", isHTMLMime("text/plain"), false);
eq("text/plain; charset", isHTMLMime("text/plain; charset=utf-8"), false);
eq("application/json is a file", isHTMLMime("application/json"), false);
eq("text/csv is a file", isHTMLMime("text/csv"), false);
eq("octet-stream is a file", isHTMLMime("application/octet-stream"), false);
eq("undefined mime", isHTMLMime(undefined), false);
eq("empty mime", isHTMLMime(""), false);

/* Report */
console.log(`\n${pass} passed, ${fail} failed`);
if (failures.length) {
  console.log("\nFailures:");
  for (const f of failures) console.log("  - " + f);
}
process.exit(fail === 0 ? 0 : 1);
