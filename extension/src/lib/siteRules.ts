import type { MediaKind } from "./types";

/**
 * Per-site sniff / takeover hints bundled with the extension.
 *
 * This started as six hardcoded entries. Real-world coverage needs two things
 * the flat list could not express: rules that key off URL *shape* rather than
 * host (CDN playlists have no recognisable domain), and rules that recognise a
 * stream endpoint whose path has no file extension at all. Hence the
 * streamPattern field.
 */
export interface SiteRule {
  id: string;
  /** Human-readable label, shown by the options page coverage list. */
  label?: string;
  /** Hostname suffix match (example.com matches a.example.com). */
  hosts: string[];
  /** Prefer these kinds when the URL is ambiguous. */
  preferKinds?: MediaKind[];
  /** Extra path/query tokens that mark HLS. */
  hlsHints?: string[];
  /** Extra path/query tokens that mark DASH. */
  dashHints?: string[];
  /** Treat these path extensions as downloadable media. */
  extraExts?: string[];
  /** Always attach document cookies when submitting. */
  needCookies?: boolean;
  /** Skip automatic takeover on this host (manual still works). */
  noAutoTakeover?: boolean;
  /**
   * Regex over the full URL recognising a stream endpoint whose path carries no
   * file extension — the common shape for signed CDN URLs.
   */
  streamPattern?: RegExp;
}

/**
 * Shared hints applied when no site rule matched. This is what makes an
 * unlisted CDN still behave: any URL that looks like a playlist is treated as
 * HLS even though its domain means nothing to us.
 */
const GENERIC_RULE: SiteRule = {
  id: "generic-stream",
  hosts: [],
  hlsHints: ["m3u8", "manifest", "playlist", "master", "chunklist"],
  dashHints: ["manifest.mpd", ".mpd"],
};

export const BUILTIN_SITE_RULES: SiteRule[] = [
  {
    id: "generic-cdn-hls",
    label: "通用 CDN",
    hosts: [
      "cloudfront.net",
      "akamaihd.net",
      "akamaized.net",
      "akamaized.net",
      "fastly.net",
      "fastlylb.net",
      "llnwd.net",
      "cdn.jsdelivr.net",
      "cdnjs.cloudflare.com",
      "azureedge.net",
      "b-cdn.net",
      "cdn77.org",
      "cachefly.net",
      "swiftserve.com",
      "bunnycdn.com",
      "imgix.net",
      "qiniudn.com",
      "qbox.me",
    ],
    hlsHints: ["m3u8", "playlist", "index.m3u8", "master.m3u8", "chunklist"],
    dashHints: ["manifest.mpd", ".mpd"],
    streamPattern: /\/(hls|stream|video|audio)\/[^/?]+\.(m3u8|mpd)/i,
    needCookies: false,
  },

  /* ---- 国内主流视频站 ---- */
  {
    id: "bilibili",
    label: "哔哩哔哩",
    hosts: ["bilibili.com", "hdslb.com", "bilivideo.com", "bilivideo.cn", "biliapi.net"],
    preferKinds: ["hls", "dash", "video"],
    hlsHints: ["m3u8", "playlist", "/dash/"],
    dashHints: [".mpd", "dash"],
    streamPattern: /\/bapi\/video\/.*\/url|\/playurl/i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "iqiyi",
    label: "爱奇艺",
    hosts: ["iqiyi.com", "iq.com", "qiyipic.com"],
    preferKinds: ["hls", "dash", "video"],
    hlsHints: ["m3u8", "playlist"],
    dashHints: [".mpd"],
    streamPattern: /\/dash\/|\/vrsplay|\/video\/play/i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "qq-video",
    label: "腾讯视频",
    hosts: ["qq.com", "v.qq.com", "gtimg.cn", "gtimg.com", "qcloudcdn.com"],
    preferKinds: ["hls", "dash", "video"],
    hlsHints: ["m3u8", "playlist"],
    dashHints: [".mpd"],
    streamPattern: /\/vip\/play|\/video\/play|\/apivideo/i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "youku",
    label: "优酷",
    hosts: ["youku.com", "ykimg.com", "tudou.com"],
    preferKinds: ["hls", "video"],
    hlsHints: ["m3u8", "playlist"],
    streamPattern: /\/play\.json|\/v4\/player|\/mp4\/|videoId/i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "mgtv",
    label: "芒果 TV",
    hosts: ["mgtv.com", "hunantv.com"],
    preferKinds: ["hls", "dash", "video"],
    hlsHints: ["m3u8", "playlist"],
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "acfun",
    label: "AcFun",
    hosts: ["acfun.cn", "acfunvideo.com"],
    preferKinds: ["hls", "dash", "video"],
    hlsHints: ["m3u8", "playlist"],
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "douyin",
    label: "抖音",
    hosts: [
      "douyin.com",
      "douyinvod.com",
      "bytecdn.cn",
      "ibytedtos.com",
      "snssdk.com",
      "douyinpic.com",
      "amemv.com",
      "zjcdn.com",
    ],
    preferKinds: ["video"],
    streamPattern: /\/aweme\/v1\/play|\/video\/tos/i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "kuaishou",
    label: "快手",
    hosts: ["kuaishou.com", "kwimgs.com", "yximgs.com"],
    preferKinds: ["video"],
    streamPattern: /\/rest\/n\/stream|\/photo\/video/i,
    needCookies: true,
    noAutoTakeover: true,
  },

  /* ---- 国际站点 ---- */
  {
    id: "youtube",
    label: "YouTube",
    hosts: [
      "youtube.com",
      "youtu.be",
      "googlevideo.com",
      "ytimg.com",
      "youtube-nocookie.com",
    ],
    preferKinds: ["video", "audio"],
    streamPattern: /\/videoplayback|\/manifest\/dash/i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "vimeo",
    label: "Vimeo",
    hosts: ["vimeo.com", "vimeocdn.com"],
    hlsHints: ["playlist", "m3u8"],
    streamPattern: /\/config|\/playlist|\/segment/i,
    needCookies: true,
  },
  {
    id: "twitch",
    label: "Twitch",
    hosts: ["twitch.tv", "ttvnw.net", "tvcdn.com", "jtvnw.net"],
    preferKinds: ["hls"],
    hlsHints: ["m3u8", "playlist", "chunked"],
    streamPattern: /\.m3u8|\/usher\//i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "dailymotion",
    label: "Dailymotion",
    hosts: ["dailymotion.com", "dai.ly", "dmcdn.net"],
    hlsHints: ["m3u8", "manifest"],
    streamPattern: /\/manifest|\/m3u8|cdn.*\/token/i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "netflix",
    label: "Netflix",
    hosts: ["netflix.com", "nflxvideo.net", "nflximg.net"],
    preferKinds: ["video"],
    streamPattern: /\/nq\.|\/dash|\/range\//i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "hulu",
    label: "Hulu",
    hosts: ["hulu.com", "huluim.com", "disney-plus.net", "dssott.com"],
    streamPattern: /\/playlist|\/manifest|hd\.nt\.lld/i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "spotify",
    label: "Spotify",
    hosts: ["spotify.com", "scdn.co", "spotifycdn.com"],
    preferKinds: ["audio"],
    streamPattern: /audio\/|audio-|\/file\/|\.mp4$/i,
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "soundcloud",
    label: "SoundCloud",
    hosts: ["soundcloud.com", "sndcdn.com"],
    preferKinds: ["audio"],
    hlsHints: ["hls", "m3u8", "media"],
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "apple-media",
    label: "Apple 媒体",
    hosts: ["apple.com", "cdn-apple.com", "mzstatic.com", "mzstatic.cn"],
    preferKinds: ["hls"],
    hlsHints: ["m3u8", "playlist.m3u8"],
    needCookies: true,
  },
  {
    id: "ted",
    label: "TED",
    hosts: ["ted.com"],
    preferKinds: ["hls", "video"],
    hlsHints: ["playlist", "m3u8"],
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "twitcasting",
    label: "TwitCasting",
    hosts: ["twitcasting.tv", "twimg.com"],
    preferKinds: ["hls"],
    hlsHints: ["m3u8"],
    needCookies: true,
    noAutoTakeover: true,
  },
  {
    id: "nicovideo",
    label: "niconico",
    hosts: ["nicovideo.jp", "nicovideo.cn", "nimg.jp"],
    preferKinds: ["hls", "dash"],
    hlsHints: ["playlist", "m3u8"],
    needCookies: true,
    noAutoTakeover: true,
  },

  /* ---- 附件 / 分发站 ---- */
  {
    id: "onedrive",
    label: "OneDrive",
    hosts: ["onedrive.live.com", "1drv.ms", "sharepoint.com"],
    hlsHints: ["download", "m3u8"],
    noAutoTakeover: true,
  },
  {
    id: "mega",
    label: "MEGA",
    hosts: ["mega.nz", "mega.co.nz", "megacloud.tv"],
    noAutoTakeover: true,
  },
  {
    id: "github-raw",
    label: "GitHub 附件",
    hosts: ["github.com", "githubusercontent.com", "objects.githubusercontent.com"],
    extraExts: ["zip", "tar", "gz", "7z", "pdf", "apk", "jar", "exe", "dmg", "iso", "deb", "rpm"],
    noAutoTakeover: true,
  },
  {
    id: "sourceforge",
    label: "SourceForge",
    hosts: ["sourceforge.net", "master.dl.sourceforge.net"],
    noAutoTakeover: true,
  },
  {
    id: "software-vendor",
    label: "软件厂商下载",
    hosts: ["nvidia.com", "nvidia.cn", "amd.com", "amdgpu.com", "intel.com", "adobe.com"],
    extraExts: ["exe", "zip", "7z", "dmg", "pkg", "deb", "rpm", "msi"],
  },
  {
    id: "steam",
    label: "Steam",
    hosts: ["steampowered.com", "steamstatic.com"],
    extraExts: ["zip", "exe", "msi", "dmg", "deb", "rpm"],
    noAutoTakeover: true,
  },
];

/* ------------------------------------------------------------------ */
/* Matching                                                            */
/* ------------------------------------------------------------------ */

export function hostOf(url: string): string {
  try {
    return new URL(url).hostname.toLowerCase();
  } catch {
    return "";
  }
}

export function matchSiteRules(hostOrUrl: string, rules: SiteRule[] = BUILTIN_SITE_RULES): SiteRule[] {
  const host = hostOrUrl.includes("://") ? hostOf(hostOrUrl) : hostOrUrl.toLowerCase();
  if (!host) return [];
  return rules.filter((r) =>
    r.hosts.some((h) => {
      const needle = h.toLowerCase().replace(/^\.+/, "");
      return host === needle || host.endsWith("." + needle);
    }),
  );
}

/** Label of the first matching rule, for display in the panel and options. */
export function ruleLabelFor(url: string): string | undefined {
  return matchSiteRules(url)[0]?.label;
}

/** Distinct hosts covered by the built-in rules, for the options coverage list. */
export function coveredHostCount(): number {
  const set = new Set<string>();
  for (const r of BUILTIN_SITE_RULES) {
    for (const h of r.hosts) set.add(h);
  }
  return set.size;
}

export interface RuleHints {
  hls: boolean;
  dash: boolean;
  noAuto: boolean;
  needCookies: boolean;
  /** Best-guess kind when the URL has no distinguishing extension. */
  preferKind?: MediaKind;
  /** Extensions this host treats as downloadable media. */
  extraExts: Set<string>;
  /** True when a streamPattern matched the URL. */
  isStream: boolean;
  /** Rules that applied, most specific first. */
  matched: SiteRule[];
}

export function ruleHintsFor(url: string): RuleHints {
  const matched = matchSiteRules(url);
  // Generics run only when nothing specific matched, so a site rule always
  // wins — but an unlisted CDN still gets playlist detection.
  const effective = matched.length ? matched : [GENERIC_RULE];
  const lower = url.toLowerCase();

  let hls = false;
  let dash = false;
  let noAuto = false;
  let needCookies = false;
  let preferKind: MediaKind | undefined;
  let isStream = false;
  const extraExts = new Set<string>();

  for (const r of effective) {
    if (r.noAutoTakeover) noAuto = true;
    if (r.needCookies) needCookies = true;
    for (const e of r.extraExts || []) extraExts.add(e.toLowerCase());
    for (const h of r.hlsHints || []) {
      if (lower.includes(h.toLowerCase())) hls = true;
    }
    for (const d of r.dashHints || []) {
      if (lower.includes(d.toLowerCase())) dash = true;
    }
    if (r.streamPattern?.test(url)) {
      isStream = true;
      // A recognised stream endpoint implies the site's own preferred kind
      // even when the URL has no extension.
      if (!preferKind && r.preferKinds?.length) preferKind = r.preferKinds[0];
      if (r.preferKinds?.includes("hls")) hls = true;
    }
    if (!preferKind && r.preferKinds?.length) preferKind = r.preferKinds[0];
  }

  return { hls, dash, noAuto, needCookies, preferKind, extraExts, isStream, matched };
}
