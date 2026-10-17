import { useEffect, useMemo, useState } from "react";
import type { AppState, ContentState, ExtensionSettings, MediaCandidate, TakeoverMode, TaskInfo } from "../lib/types";
import { DEFAULT_SETTINGS } from "../lib/types";
import { formatBytes, baseName } from "../lib/context";

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

const STATE_LABEL: Record<string, string> = {
  queued: "排队",
  resolving: "解析中",
  running: "下载中",
  paused: "已暂停",
  waiting_user: "等待操作",
  verifying: "校验中",
  done: "已完成",
  failed: "失败",
  canceled: "已取消",
};

function pct(t: TaskInfo): number {
  if (t.state === "done") return 100;
  if (!t.size) return 0;
  return Math.min(100, Math.max(0, (t.done / t.size) * 100));
}

function hostText(app: AppState): { label: string; tone: string } {
  if (app.hostPresent === false) return { label: "未安装浏览器组件", tone: "idle" };
  if (app.appOnline) return { label: "XDL 已连接", tone: "live" };
  return { label: "XDL 未运行", tone: "ready" };
}

export default function PopupApp() {
  const [settings, setSettings] = useState<ExtensionSettings>({ ...DEFAULT_SETTINGS });
  const [app, setApp] = useState<AppState>({ hostPresent: null, appOnline: false, checkedAt: 0 });
  const [tasks, setTasks] = useState<TaskInfo[]>([]);
  const [media, setMedia] = useState<MediaCandidate[]>([]);
  const [tabId, setTabId] = useState<number | undefined>();
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState("");
  const [toast, setToast] = useState<{ kind: string; text: string } | null>(null);

  const notify = (kind: string, text: string) => {
    setToast({ kind, text });
    window.setTimeout(() => setToast(null), 3200);
  };

  const load = async (includeState = true) => {
    const tabs = await chrome.tabs.query({ active: true, currentWindow: true });
    const id = tabs[0]?.id;
    setTabId(id);
    if (includeState) {
      const st = await bg<{ ok: boolean; state?: ContentState }>({ type: "state.refresh" });
      if (st?.state) {
        setSettings(st.state.settings);
        setApp(st.state.app);
      }
    }
    const res = await bg<{ ok: boolean; tasks?: TaskInfo[]; app?: AppState }>({ type: "tasks.list" });
    if (res) {
      setTasks(res.tasks || []);
      if (res.app) setApp(res.app);
    }
    if (id != null) {
      const m = await bg<{ ok: boolean; candidates?: MediaCandidate[] }>({ type: "media.list", tabId: id });
      if (m?.candidates) setMedia(m.candidates);
    }
    setLoading(false);
  };

  useEffect(() => {
    void load(true);
    const timer = window.setInterval(() => {
      void load(false);
    }, 2600);
    return () => window.clearInterval(timer);
  }, []);

  const setMode = async (mode: TakeoverMode) => {
    const next = { ...settings, mode };
    setSettings(next);
    setBusy("mode");
    const res = await bg<{ ok: boolean; settings?: ExtensionSettings }>({ type: "settings.set", settings: next });
    setBusy("");
    if (res?.settings) {
      setSettings(res.settings);
      notify("success", `接管模式：${res.settings.mode === "smart" ? "智能" : res.settings.mode === "ask" ? "询问" : "关闭"}`);
    }
  };

  const startApp = async () => {
    setBusy("start");
    const res = await bg<{ ok: boolean; app?: AppState }>({ type: "app.start" });
    setBusy("");
    if (res?.app) setApp(res.app);
    if (res?.ok) {
      notify("success", "XDL 已启动");
      void load(false);
    } else {
      notify("error", "无法启动 XDL，请从开始菜单打开");
    }
  };

  const sendMedia = async (c: MediaCandidate) => {
    setBusy(c.id);
    const res = await bg<{ ok: boolean; error?: string; task?: TaskInfo }>({
      type: "media.send",
      url: c.url,
      pageUrl: c.pageUrl,
      suggestedName: baseName(c.url),
    });
    setBusy("");
    if (res?.ok) {
      notify("success", `已发送：${res.task?.name || baseName(c.url)}`);
      void load(false);
    } else {
      notify("error", `未接管：${res?.error || "未知错误"}`);
    }
  };

  const taskAction = async (t: TaskInfo) => {
    const action = t.state === "running" || t.state === "queued" || t.state === "resolving" ? "pause" : "resume";
    setBusy(t.id);
    const res = await bg<{ ok: boolean; error?: string }>({ type: "task.action", action, id: t.id });
    setBusy("");
    if (!res?.ok) notify("error", res?.error || "操作失败");
    void load(false);
  };

  const host = hostText(app);
  const visibleMedia = useMemo(() => media.filter((c) => c.kind !== "other").slice(0, 4), [media]);
  const visibleTasks = tasks.slice(0, 4);

  return (
    <div className="popup">
      <header className="p-head">
        <div className="brand">
          <span className="brand-mark">
            <svg viewBox="0 0 32 32" width="24" height="24">
              <rect x="2" y="2" width="28" height="28" rx="7" fill="none" stroke="currentColor" strokeWidth="1.6" />
              <path d="M16 8v10M11 14l5 5 5-5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
              <path d="M10 23h12" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            </svg>
          </span>
          <div className="brand-text">
            <strong>XDL</strong>
            <span>下载接管</span>
          </div>
        </div>
        <button className="icon" title="刷新" onClick={() => void load(true)}>
          <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round">
            <path d="M20 7v5h-5" />
            <path d="M20 12a8 8 0 1 1-2.3-5.7L20 8" />
          </svg>
        </button>
      </header>

      <div className="status">
        <span className={`dot ${host.tone}`} />
        <span className="status-text">{host.label}</span>
        {app.version && app.appOnline && <span className="ver">v{app.version}</span>}
        {app.hostPresent && !app.appOnline && (
          <button className="mini-btn" onClick={startApp} disabled={busy === "start"}>
            {busy === "start" ? "启动中…" : "启动 XDL"}
          </button>
        )}
        {app.hostPresent === false && (
          <button className="mini-btn" onClick={() => chrome.runtime.openOptionsPage()}>
            查看安装
          </button>
        )}
      </div>

      <section className="section">
        <div className="section-head">
          <span>接管模式</span>
          <span className="muted">
            {settings.mode === "smart" ? "点击下载自动交给 XDL" : settings.mode === "ask" ? "每次先询问" : "不接管浏览器下载"}
          </span>
        </div>
        <div className="segmented">
          {(
            [
              ["smart", "智能"],
              ["ask", "询问"],
              ["off", "关闭"],
            ] as [TakeoverMode, string][]
          ).map(([mode, label]) => (
            <button
              key={mode}
              className={settings.mode === mode ? "active" : ""}
              onClick={() => setMode(mode)}
              disabled={busy === "mode"}
            >
              {label}
            </button>
          ))}
        </div>
      </section>

      <section className="section">
        <div className="section-head">
          <span>本页媒体</span>
          <button
            className="text"
            onClick={() => {
              if (tabId != null) void bg({ type: "panel.open" });
              window.close();
            }}
          >
            页面面板
          </button>
        </div>
        {loading ? (
          <div className="empty">正在扫描…</div>
        ) : visibleMedia.length === 0 ? (
          <div className="empty">播放视频或打开含媒体的页面后，这里会列出来。</div>
        ) : (
          <div className="media-list">
            {visibleMedia.map((c) => (
              <div className="media-row" key={c.id}>
                <span className={`kind ${c.kind}`}>
                  {c.kind === "hls" ? "HLS" : c.kind === "dash" ? "DASH" : c.kind === "audio" ? "音频" : "视频"}
                </span>
                <span className="media-name" title={c.url}>
                  {baseName(c.url) || c.url}
                </span>
                <span className="media-size">{formatBytes(c.size)}</span>
                {c.kind === "video" || c.kind === "audio" ? (
                  <button className="tiny primary" onClick={() => sendMedia(c)} disabled={busy === c.id}>
                    {busy === c.id ? "…" : "接收"}
                  </button>
                ) : (
                  <span className="tiny disabled">下一版</span>
                )}
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="section">
        <div className="section-head">
          <span>最近任务</span>
          <span className="muted">{app.appOnline ? `${tasks.length} 个` : "XDL 离线"}</span>
        </div>
        {visibleTasks.length === 0 ? (
          <div className="empty">{app.appOnline ? "还没有任务。" : "启动 XDL 后可查看任务进度。"}</div>
        ) : (
          <div className="tasks">
            {visibleTasks.map((t) => (
              <div className={`task state-${t.state}`} key={t.id}>
                <div className="task-top">
                  <span className={`tdot ${t.state}`} />
                  <span className="task-name" title={t.name || t.url}>
                    {t.name || baseName(t.url) || t.url}
                  </span>
                  <span className="task-state">{STATE_LABEL[t.state] || t.state}</span>
                  {(t.state === "running" || t.state === "paused" || t.state === "queued") && (
                    <button className="tiny" onClick={() => taskAction(t)} disabled={busy === t.id}>
                      {t.state === "paused" ? "继续" : "暂停"}
                    </button>
                  )}
                </div>
                <div className="tbar">
                  <i style={{ width: `${pct(t)}%` }} />
                </div>
                <div className="task-meta">
                  <span>{t.size ? `${formatBytes(t.done)} / ${formatBytes(t.size)}` : formatBytes(t.done)}</span>
                  <span>{t.speed ? `${formatBytes(t.speed)}/s` : pct(t).toFixed(0) + "%"}</span>
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      <footer className="p-foot">
        <button className="text" onClick={() => chrome.runtime.openOptionsPage()}>
          设置
        </button>
        <span className="sep" />
        <button
          className="text"
          onClick={async () => {
            setBusy("repair");
            const res = await bg<{ ok: boolean; error?: string }>({ type: "repair-integration" });
            setBusy("");
            if (res?.ok) {
              notify("success", "浏览器集成已注册");
              void load(true);
            } else {
              notify("error", res?.error || "修复失败");
            }
          }}
          disabled={busy === "repair"}
        >
          修复集成
        </button>
        <span className="foot-space" />
        <span className="foot-ver">v{chrome.runtime.getManifest().version}</span>
      </footer>

      {toast && <div className={`toast ${toast.kind}`}>{toast.text}</div>}
    </div>
  );
}
