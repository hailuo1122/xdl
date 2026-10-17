import { useEffect, useState } from "react";
import type { AppState, ContentState, ExtensionSettings, TakeoverMode } from "../lib/types";
import { DEFAULT_SETTINGS } from "../lib/types";

function bg<T>(msg: unknown): Promise<T | null> {
  return new Promise((resolve) => {
    try {
      chrome.runtime.sendMessage(msg, (resp) => {
        void chrome.runtime.lastError;
        resolve((resp as T) ?? null);
      });
    } catch {
      resolve(null);
    }
  });
}

function hostStatus(app: AppState): { label: string; tone: string; detail: string } {
  if (app.hostPresent === false) {
    return {
      label: "未安装浏览器组件",
      tone: "idle",
      detail: "在 XDL 桌面端「设置 → 浏览器接管」里安装组件，然后回到这里重新检测。",
    };
  }
  if (app.appOnline) {
    return {
      label: "已连接",
      tone: "live",
      detail: `XDL ${app.version || ""} 正在运行，下载会直接进入任务列表。`,
    };
  }
  return {
    label: "XDL 未运行",
    tone: "ready",
    detail: "浏览器组件已注册。点击「启动 XDL」或直接开始下载时会自动拉起应用。",
  };
}

export default function OptionsApp() {
  const [draft, setDraft] = useState<ExtensionSettings>({ ...DEFAULT_SETTINGS });
  const [app, setApp] = useState<AppState>({ hostPresent: null, appOnline: false, checkedAt: 0 });
  const [extensionId, setExtensionId] = useState(chrome.runtime.id);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [toast, setToast] = useState<{ kind: string; text: string } | null>(null);
  const [saved, setSaved] = useState(false);
  const [excludeText, setExcludeText] = useState("");

  const notify = (kind: string, text: string) => {
    setToast({ kind, text });
    window.setTimeout(() => setToast(null), 3400);
  };

  const load = async (refresh = false) => {
    const res = await bg<{ ok: boolean; state?: ContentState }>(
      refresh ? { type: "state.refresh" } : { type: "state.get" },
    );
    if (res?.state) {
      setDraft(res.state.settings);
      setExcludeText(res.state.settings.exclude.join("\n"));
      setApp(res.state.app);
      if (res.state.app.extensionId) setExtensionId(res.state.app.extensionId);
    }
    setLoading(false);
  };

  useEffect(() => {
    void load(false);
    void load(true);
  }, []);

  const patch = (p: Partial<ExtensionSettings>) => {
    setDraft((d) => ({ ...d, ...p }));
    setSaved(false);
  };

  const save = async () => {
    setBusy("save");
    const next: ExtensionSettings = {
      ...draft,
      exclude: excludeText
        .split(/\r?\n/)
        .map((x) => x.trim())
        .filter(Boolean),
    };
    const res = await bg<{ ok: boolean; settings?: ExtensionSettings; error?: string }>({
      type: "settings.set",
      settings: next,
    });
    setBusy("");
    if (res?.ok && res.settings) {
      setDraft(res.settings);
      setSaved(true);
      notify("success", "设置已保存");
    } else {
      notify("error", res?.error || "保存失败");
    }
  };

  const start = async () => {
    setBusy("start");
    const res = await bg<{ ok: boolean; app?: AppState }>({ type: "app.start" });
    setBusy("");
    if (res?.app) setApp(res.app);
    if (res?.ok) notify("success", "XDL 已启动");
    else notify("error", "无法启动 XDL，请手动打开桌面端");
    void load(false);
  };

  const repair = async () => {
    setBusy("repair");
    const res = await bg<{ ok: boolean; error?: string }>({ type: "repair-integration" });
    setBusy("");
    if (res?.ok) {
      notify("success", "浏览器集成已重新注册");
      void load(true);
    } else {
      notify("error", res?.error || "修复失败");
    }
  };

  const copyDiagnostics = async () => {
    const text = [
      "XDL 浏览器扩展诊断",
      `extensionId: ${chrome.runtime.id}`,
      `extensionVersion: ${chrome.runtime.getManifest().version}`,
      `hostPresent: ${String(app.hostPresent)}`,
      `appOnline: ${String(app.appOnline)}`,
      `appVersion: ${app.version || "-"}`,
      `errorCode: ${app.errorCode || "-"}`,
      `lastError: ${app.lastError || "-"}`,
      `checkedAt: ${new Date(app.checkedAt || 0).toISOString()}`,
      `mode: ${draft.mode}`,
      `minSize: ${draft.minSize}`,
      `exclude: ${excludeText.split(/\r?\n/).filter(Boolean).join(", ")}`,
      `ua: ${navigator.userAgent}`,
    ].join("\n");
    try {
      await navigator.clipboard.writeText(text);
      notify("success", "诊断信息已复制");
    } catch {
      notify("error", "复制失败");
    }
  };

  const host = hostStatus(app);

  return (
    <div className="options">
      <header className="o-head">
        <div className="o-brand">
          <span className="brand-mark">
            <svg viewBox="0 0 32 32" width="30" height="30">
              <rect x="2" y="2" width="28" height="28" rx="7" fill="none" stroke="currentColor" strokeWidth="1.6" />
              <path d="M16 8v10M11 14l5 5 5-5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
              <path d="M10 23h12" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            </svg>
          </span>
          <div>
            <h1>XDL 下载接管</h1>
            <p>Chrome / Edge 扩展设置，本地转交，不上传浏览数据</p>
          </div>
        </div>
        <div className="head-actions">
          <button className="btn ghost" onClick={() => void load(true)}>
            重新检测
          </button>
          <button className="btn" onClick={save} disabled={busy === "save"}>
            {saved ? "已保存" : busy === "save" ? "保存中…" : "保存设置"}
          </button>
        </div>
      </header>

      <main className="o-main">
        <section className="panel">
          <h2>接管策略</h2>
          <div className="mode-grid">
            {(
              [
                ["smart", "智能接管", "文件链接与浏览器下载自动交给 XDL，失败回退浏览器。"],
                ["ask", "每次询问", "弹出确认：选 XDL 或浏览器，并可更改保存位置。"],
                ["off", "关闭", "完全不碰浏览器下载；仍可右键手动使用 XDL。"],
              ] as [TakeoverMode, string, string][]
            ).map(([mode, title, desc]) => (
              <button
                key={mode}
                className={`mode-card ${draft.mode === mode ? "selected" : ""}`}
                onClick={() => patch({ mode })}
                type="button"
              >
                <span className="mode-title">{title}</span>
                <span className="mode-desc">{desc}</span>
              </button>
            ))}
          </div>

          <div className="field-row">
            <label className="field">
              <span>接管大小阈值</span>
              <div className="inline-input">
                <input
                  type="number"
                  min={0}
                  step={0.5}
                  value={Math.round((draft.minSize / 1024 / 1024) * 10) / 10}
                  onChange={(e) => patch({ minSize: Math.max(0, Number(e.target.value) || 0) * 1024 * 1024 })}
                />
                <em>MB</em>
              </div>
              <small>自动点击会先探测大小，小于该值交给浏览器；大小未知时接管。右键/媒体面板等手动操作不受限制。</small>
            </label>
            <label className="field">
              <span>仅接管 HTTPS</span>
              <button
                type="button"
                className={`switch ${draft.httpsOnly ? "on" : ""}`}
                onClick={() => patch({ httpsOnly: !draft.httpsOnly })}
              >
                <i />
              </button>
              <small>关闭后 HTTP 下载也会接管。</small>
            </label>
          </div>

          <label className="field wide">
            <span>排除的站点</span>
            <textarea
              rows={4}
              spellCheck={false}
              placeholder={"one.example.com\npayments.example.com"}
              value={excludeText}
              onChange={(e) => {
                setExcludeText(e.target.value);
                setSaved(false);
              }}
            />
            <small>每行一个域名；子域名自动包含。右键「用 XDL 下载」仍可手动接管。</small>
          </label>
        </section>

        <section className="panel">
          <h2>媒体与通知</h2>
          <div className="toggle-row">
            <div>
              <span>嗅探页面媒体</span>
              <small>识别 m3u8 / mpd / mp4 / webm 等资源，在页面右下角显示面板入口。</small>
            </div>
            <button
              type="button"
              className={`switch ${draft.mediaSniff ? "on" : ""}`}
              onClick={() => patch({ mediaSniff: !draft.mediaSniff })}
            >
              <i />
            </button>
          </div>
          <div className="toggle-row">
            <div>
              <span>接管失败时通知</span>
              <small>XDL 未响应或拒绝任务时，用系统通知告知并继续浏览器下载。</small>
            </div>
            <button
              type="button"
              className={`switch ${draft.notifyFallback ? "on" : ""}`}
              onClick={() => patch({ notifyFallback: !draft.notifyFallback })}
            >
              <i />
            </button>
          </div>
          <div className="note">
            支持直链 MP4 / WebM / 音频，以及 HLS / DASH VOD（AES-128 可解密；DRM / 直播会明确拒绝）。
          </div>
        </section>

        <section className="panel integration">
          <h2>桌面端连接</h2>
          <div className="integration-state">
            <span className={`orb ${host.tone}`}>
              <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round">
                <rect x="3" y="4" width="18" height="16" rx="2" />
                <path d="M3 9h18" />
              </svg>
            </span>
            <div>
              <strong>{host.label}</strong>
              <p>{host.detail}</p>
              <div className="diag-line">
                <span>宿主：{app.hostPresent === false ? "未注册" : "已注册"}</span>
                <span>应用：{app.appOnline ? app.version || "在线" : "离线"}</span>
                <span>扩展：{extensionId}</span>
              </div>
            </div>
            <div className="integration-actions">
              {app.hostPresent && !app.appOnline && (
                <button className="btn" onClick={start} disabled={busy === "start"}>
                  {busy === "start" ? "启动中…" : "启动 XDL"}
                </button>
              )}
              <button className="btn ghost" onClick={repair} disabled={busy === "repair"}>
                {busy === "repair" ? "注册中…" : "修复集成"}
              </button>
            </div>
          </div>
          <div className="steps">
            <h3>首次使用</h3>
            <ol>
              <li>在 XDL 桌面端「设置 → 浏览器接管」点击「安装组件」，注册原生消息宿主。</li>
              <li>在扩展管理页加载本扩展（开发版为 <code>extension/.output/chrome-mv3</code>）。</li>
              <li>刷新网页；点击下载链接，或右键选择「用 XDL 下载链接」。</li>
            </ol>
          </div>
          <button className="text-btn" onClick={copyDiagnostics}>
            复制诊断信息
          </button>
        </section>
      </main>

      <footer className="o-foot">
        <span>XDL internal alpha · Cookie / Referer 仅在本机转交</span>
        <span className="foot-ver">extension v{chrome.runtime.getManifest().version}</span>
      </footer>

      {toast && <div className={`toast ${toast.kind}`}>{toast.text}</div>}
      {loading && <div className="loading">正在读取扩展状态…</div>}
    </div>
  );
}
