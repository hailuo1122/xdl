/**
 * Checks for the site-rule engine.
 *
 * The rules decide whether a URL is treated as a stream and whether cookies
 * must be attached, so a mistake here either silently drops real media or
 * hijacks ordinary navigation.
 *
 * Compile & run:
 *   cd extension && ./node_modules/.bin/tsc src/lib/siteRules.ts \
 *     --outDir <tmp> --module esnext --target es2022 --moduleResolution bundler --skipLibCheck
 *   node <tmp>/siteRules.test.mjs
 */

import { ruleHintsFor, matchSiteRules, coveredHostCount } from "./siteRules.js";

let pass = 0;
let fail = 0;
const failures = [];

function eq(label, actual, expected) {
  if (actual === expected) pass++;
  else {
    fail++;
    failures.push(`${label}: expected ${expected}, got ${actual}`);
  }
}

function truthy(label, v) {
  eq(label, !!v, true);
}

/* ---- host matching ---- */
eq("bilibili.com matches", matchSiteRules("https://www.bilibili.com/video/BV1x").length > 0, true);
eq("subdomain matches", matchSiteRules("https://upos-sz-mirrorcos.bilivideo.com/x.m3u8").length > 0, true);
eq("unrelated does not", matchSiteRules("https://example.com/a.mp4").length, 0);

/* ---- noAutoTakeover guards ---- */
truthy("bilibili noAuto", ruleHintsFor("https://www.bilibili.com/video/BV1x").noAuto);
truthy("youtube noAuto", ruleHintsFor("https://www.youtube.com/watch?v=abc").noAuto);
eq("github noAuto", ruleHintsFor("https://github.com/x/y/releases").noAuto, true);
eq("generic cdn not noAuto", ruleHintsFor("https://cdn.example.com/a.zip").noAuto, false);

/* ---- cookie requirements ---- */
truthy("bilibili needs cookies", ruleHintsFor("https://www.bilibili.com/video/BV1x").needCookies);
truthy("twitch needs cookies", ruleHintsFor("https://www.twitch.tv/videos/1").needCookies);
eq("generic cdn no cookies", ruleHintsFor("https://cdn.example.com/a.zip").needCookies, false);

/* ---- extensionless stream recognition (the whole point of streamPattern) ---- */
truthy("youtube videoplayback", ruleHintsFor("https://rr3---sn-x.googlevideo.com/videoplayback?expire=1&itag=22").isStream);
truthy("bilibili playurl", ruleHintsFor("https://api.bilibili.com/x/player/playurl?fnval=16").isStream);
truthy("douyin play", ruleHintsFor("https://aweme.snssdk.com/aweme/v1/play/?video_id=1").isStream);
truthy("spotify audio", ruleHintsFor("https://pods.scdn.co/file/xyz.mp4").isStream);
eq("ordinary url not stream", ruleHintsFor("https://example.com/page/about").isStream, false);
eq("plain file not stream", ruleHintsFor("https://example.com/manual.pdf").isStream, false);

/* ---- preferKind propagation ---- */
eq("youtube prefers video", ruleHintsFor("https://x.googlevideo.com/videoplayback?id=1").preferKind, "video");
eq("twitch prefers hls", ruleHintsFor("https://usher.ttvnw.net/api/channel/hls/x.m3u8").preferKind, "hls");

/* ---- hint matching ---- */
truthy("m3u8 marks hls", ruleHintsFor("https://cdn.example.com/a.m3u8").hls);
truthy("mpd marks dash", ruleHintsFor("https://cdn.example.com/manifest.mpd").dash);
truthy("bilibili dash hint", ruleHintsFor("https://upos.bilivideo.com/dash/abc/manifest.mpd").dash);

/* ---- generic fallback applies to unknown hosts ---- */
truthy("unknown host still detects m3u8", ruleHintsFor("https://random-cdn.example.net/path/master.m3u8").hls);

/* ---- extraExts ---- */
truthy("github extraExts", ruleHintsFor("https://github.com/a/b").extraExts.has("apk"));
truthy("vendor extraExts", ruleHintsFor("https://www.nvidia.com/Download/driver").extraExts.has("exe"));

/* ---- regression: a plain navigation must never look like media ---- */
const nav = ruleHintsFor("https://example.com/products/widget");
eq("navigation not hls", nav.hls, false);
eq("navigation not dash", nav.dash, false);
eq("navigation not stream", nav.isStream, false);

/* ---- coverage size ---- */
truthy("covers many hosts", coveredHostCount() > 40);

console.log(`\n${pass} passed, ${fail} failed`);
if (failures.length) {
  console.log("\nFailures:");
  for (const f of failures) console.log("  - " + f);
}
process.exit(fail === 0 ? 0 : 1);
