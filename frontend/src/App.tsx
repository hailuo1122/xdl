import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events } from "@wailsio/runtime";
import { api, errorText } from "./api";
import { displayName } from "./format";
import type { AppStatus, Filter, Settings, TaskDetail, TaskView } from "./types";
import { DetailPanel } from "./components/DetailPanel";
import { EmptyState } from "./components/EmptyState";
import { Icon } from "./components/Icon";
import { NewTaskDialog } from "./components/NewTaskDialog";
import { Onboarding } from "./components/Onboarding";
import { SettingsDialog } from "./components/SettingsDialog";
import { Sidebar } from "./components/Sidebar";
import { TaskTable } from "./components/TaskTable";
import { Toasts, type Toast, type ToastKind } from "./components/Toasts";

type SettingsSection = "general" | "download" | "browser" | "bt" | "update";

function isDownloading(t: TaskView) {
  return t.state === "running" || t.state === "resolving" || t.state === "verifying";
}

function isWaiting(t: TaskView) {
  return t.state === "queued" || t.state === "waiting_user";
}

function isStopped(t: TaskView) {
  return t.state === "paused" || t.state === "failed" || t.state === "canceled";
}

function upsert(list: TaskView[], t: TaskView): TaskView[] {
  const idx = list.findIndex((x) => x.id === t.id);
  if (idx >= 0) {
    const next = list.slice();
    next[idx] = t;
    return next;
  }
  return [t, ...list];
}

export default function App() {
  const [status, setStatus] = useState<AppStatus | null>(null);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [tasks, setTasks] = useState<TaskView[]>([]);
  const [filter, setFilter] = useState<Filter>("downloading");
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<string | null>(null);
  const [detail, setDetail] = useState<TaskDetail | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [showNew, setShowNew] = useState(false);
  const [prefill, setPrefill] = useState("");
  const [showSettings, setShowSettings] = useState(false);
  const [settingsSection, setSettingsSection] = useState<SettingsSection>("general");
  const [showOnboarding, setShowOnboarding] = useState(false);
  const [toasts, setToasts] = useState<Toast[]>([]);
  const [dragOver, setDragOver] = useState(false);
  const [bootError, setBootError] = useState("");
  const searchRef = useRef<HTMLInputElement>(null);
  const toastSeq = useRef(1);

  const pushToast = useCallback((kind: ToastKind, text: string) => {
    const id = toastSeq.current++;
    setToasts((prev) => [...prev.slice(-3), { id, kind, text }]);
    window.setTimeout(() => {
      setToasts((prev) => prev.filter((t) => t.id !== id));
    }, 4200);
  }, []);

  const applyStatus = useCallback((st: AppStatus) => {
    setStatus(st);
    setSettings(st.settings);
    if (!st.settings.firstRunDone) setShowOnboarding(true);
  }, []);

  const loadAll = useCallback(
    async (silent = false) => {
      try {
        const [st, list] = await Promise.all([api.status(), api.list()]);
        applyStatus(st);
        setTasks(list);
        setBootError("");
      } catch (err) {
        // Browser Vite preview: show sample tasks so the shell can be reviewed without Wails.
        if (typeof window !== "undefined" && !(window as { wails?: unknown }).wails) {
          const now = new Date().toISOString();
          const demoSettings: Settings = {
            downloadDir: "D:\\Downloads",
            maxConcurrent: 3,
            defaultConns: 8,
            turbo: false,
            globalLimitBps: 0,
            ffmpegPath: "",
            launchAtLogin: false,
            startMinimized: false,
            closeToTray: true,
            notifications: true,
            firstRunDone: true,
            theme: "light",
            language: "zh-CN",
            btLegalAccepted: false,
            btSeedAfterDone: false,
            btUploadLimitBps: 128 * 1024,
            btMaxPeers: 50,
            autoCategory: true,
            scheduleEnabled: false,
            schedulePauseStart: 0,
            schedulePauseEnd: 0,
            aria2Enabled: true,
            aria2Port: 6800,
            aria2Secret: "",
            watchClipboard: true,
            domainConcurrency: 2,
          };
          applyStatus({
            version: "0.2.0-preview",
            ready: true,
            tasks: 4,
            running: 2,
            speed: 1.2 * 1024 * 1024,
            limit: 0,
            downloadDir: demoSettings.downloadDir,
            settings: demoSettings,
            connected: false,
            integration: {
              host: {
                hostExists: true,
                manifestExists: true,
                extensionId: "preview",
                origins: [],
                browsers: [],
                installed: true,
              },
              extensionId: "preview",
              extensionSeen: { lastSeen: now },
              extensionLive: false,
            },
          });
          setTasks([
            {
              id: "1",
              kind: "http",
              url: "https://cdn.example.com/7.webp",
              name: "7.webp",
              state: "running",
              size: 12_000_000,
              done: 3_600_000,
              speed: 820_000,
              conns: 8,
              validator: { acceptRanges: true },
              createdAt: now,
              updatedAt: now,
            },
            {
              id: "2",
              kind: "http",
              url: "https://cdn.example.com/3.22",
              name: "3.22",
              state: "running",
              size: 48_000_000,
              done: 9_600_000,
              speed: 410_000,
              conns: 8,
              validator: { acceptRanges: true },
              createdAt: now,
              updatedAt: now,
            },
            {
              id: "3",
              kind: "http",
              url: "https://files.example.com/Setup_XDL_0.2.exe",
              name: "Setup_XDL_0.2.0_x64.exe",
              state: "queued",
              size: 64_000_000,
              done: 0,
              speed: 0,
              conns: 0,
              validator: { acceptRanges: true },
              createdAt: now,
              updatedAt: now,
            },
            {
              id: "4",
              kind: "http",
              url: "https://files.example.com/old.zip",
              name: "archive-backup.zip",
              state: "paused",
              size: 200_000_000,
              done: 80_000_000,
              speed: 0,
              conns: 0,
              validator: { acceptRanges: true },
              createdAt: now,
              updatedAt: now,
            },
          ]);
          setBootError("");
          return;
        }
        if (!silent) setBootError(errorText(err));
      }
    },
    [applyStatus],
  );

  const refreshStatus = useCallback(async () => {
    try {
      const st = await api.status();
      setStatus(st);
    } catch {
      /* transient */
    }
  }, []);

  const refreshList = useCallback(async () => {
    try {
      setTasks(await api.list());
    } catch {
      /* transient */
    }
  }, []);

  useEffect(() => {
    loadAll();
    const statusTimer = window.setInterval(refreshStatus, 6000);
    const listTimer = window.setInterval(refreshList, 15000);
    return () => {
      window.clearInterval(statusTimer);
      window.clearInterval(listTimer);
    };
  }, [loadAll, refreshStatus, refreshList]);

  useEffect(() => {
    const offs: (() => void)[] = [];
    offs.push(
      Events.On("task.updated", (ev) => {
        const t = ev.data as TaskView;
        if (!t || !t.id) return;
        setTasks((prev) => upsert(prev, t));
        setDetail((prev) => (prev && prev.task.id === t.id ? { ...prev, task: t } : prev));
        refreshStatus();
      }),
    );
    offs.push(
      Events.On("task.progress", (ev) => {
        const p = ev.data as { id: string; done: number; total: number; speed: number };
        if (!p || !p.id) return;
        setTasks((prev) =>
          prev.map((t) =>
            t.id === p.id
              ? { ...t, done: p.done, size: p.total || t.size, speed: p.speed, state: t.state === "queued" || t.state === "resolving" ? "running" : t.state }
              : t,
          ),
        );
        setDetail((prev) =>
          prev && prev.task.id === p.id
            ? { ...prev, task: { ...prev.task, done: p.done, size: p.total || prev.task.size, speed: p.speed } }
            : prev,
        );
      }),
    );
    offs.push(
      Events.On("settings.changed", (ev) => {
        const s = ev.data as Settings;
        if (s) {
          setSettings(s);
          setStatus((prev) => (prev ? { ...prev, settings: s, limit: s.globalLimitBps } : prev));
        }
      }),
    );
    offs.push(
      Events.On("ui.open-settings", (ev) => {
        const payload = ev.data as { section?: string } | undefined;
        if (
          payload?.section === "browser" ||
          payload?.section === "download" ||
          payload?.section === "general" ||
          payload?.section === "bt" ||
          payload?.section === "update"
        ) {
          setSettingsSection(payload.section);
        }
        setShowOnboarding(false);
        setShowSettings(true);
      }),
    );
    offs.push(
      Events.On("ui.toast", (ev) => {
        const payload = ev.data as { kind?: ToastKind; text?: string } | undefined;
        if (payload?.text) pushToast(payload.kind || "info", payload.text);
      }),
    );
    offs.push(
      Events.On("ui.open-new", (ev) => {
        const payload = ev.data as { prefill?: string } | undefined;
        setPrefill(payload?.prefill || "");
        setShowNew(true);
      }),
    );
    offs.push(
      Events.On("external.added", (ev) => {
        const t = ev.data as TaskView;
        if (t?.id) {
          setTasks((prev) => upsert(prev, t));
          pushToast("success", `已添加：${displayName(t)}`);
        }
      }),
    );
    offs.push(Events.On("app.ready", () => loadAll(true)));
    return () => offs.forEach((off) => off());
  }, [refreshStatus, pushToast, loadAll]);

  useEffect(() => {
    if (!selected) {
      setDetail(null);
      return;
    }
    let cancelled = false;
    const pull = async () => {
      try {
        const d = await api.detail(selected);
        if (!cancelled) setDetail(d);
      } catch {
        if (!cancelled) setDetail(null);
      } finally {
        if (!cancelled) setDetailLoading(false);
      }
    };
    setDetailLoading(true);
    pull();
    const timer = window.setInterval(pull, 1400);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [selected]);

  useEffect(() => {
    const theme = settings?.theme || "light";
    const actual =
      theme === "system"
        ? window.matchMedia("(prefers-color-scheme: dark)").matches
          ? "dark"
          : "light"
        : theme;
    document.documentElement.dataset.theme = actual;
  }, [settings?.theme]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    return tasks.filter((t) => {
      if (filter === "downloading" && !isDownloading(t)) return false;
      if (filter === "waiting" && !isWaiting(t)) return false;
      if (filter === "stopped" && !isStopped(t)) return false;
      if (filter === "done" && t.state !== "done") return false;
      if (!q) return true;
      return (
        displayName(t).toLowerCase().includes(q) ||
        t.url.toLowerCase().includes(q) ||
        (t.err?.message || "").toLowerCase().includes(q)
      );
    });
  }, [tasks, filter, query]);

  const totalSpeed = useMemo(() => tasks.reduce((n, t) => n + (t.speed || 0), 0), [tasks]);
  const pausableCount = useMemo(
    () => tasks.filter((t) => ["running", "resolving", "queued", "verifying", "waiting_user"].includes(t.state)).length,
    [tasks],
  );
  const pausedCount = useMemo(() => tasks.filter((t) => t.state === "paused").length, [tasks]);
  const integration = status?.integration;
  const integrationReady = !!integration?.host.installed;
  const extensionLive = !!integration?.extensionLive;

  const openNew = useCallback((text = "") => {
    setPrefill(text);
    setShowNew(true);
  }, []);

  const extractDownloadText = useCallback((raw: string): string => {
    const text = raw.trim();
    if (!text) return "";
    const magnet = text.match(/magnet:\?[^\s"'<>]+/i);
    if (magnet) return magnet[0];
    const torrentPath = text.match(/(?:[A-Za-z]:\\|\\\\|\/).+\.torrent\b/i);
    if (torrentPath) return torrentPath[0].replace(/^["']|["']$/g, "");
    const url = text.match(/https?:\/\/[^\s"'<>]+/i);
    if (url) return url[0];
    if (/^magnet:/i.test(text) || /\.torrent$/i.test(text) || /^https?:\/\//i.test(text)) return text;
    return "";
  }, []);

  const onPaste = useCallback(async () => {
    try {
      const text = await navigator.clipboard.readText();
      const hit = extractDownloadText(text || "");
      if (hit) openNew(hit);
      else if (text?.trim()) openNew(text.trim());
      else pushToast("info", "剪贴板里没有链接");
    } catch {
      openNew();
    }
  }, [extractDownloadText, openNew, pushToast]);

  useEffect(() => {
    if (!settings?.watchClipboard) return;
    let last = "";
    let alive = true;
    const tick = async () => {
      if (!alive || showNew || showSettings || showOnboarding || document.hidden) return;
      try {
        const text = await navigator.clipboard.readText();
        const hit = extractDownloadText(text || "");
        if (!hit || hit === last) return;
        last = hit;
        openNew(hit);
        pushToast("info", "已从剪贴板识别下载链接");
      } catch {
        /* clipboard permission / empty */
      }
    };
    const id = window.setInterval(() => void tick(), 1600);
    return () => {
      alive = false;
      window.clearInterval(id);
    };
  }, [settings?.watchClipboard, showNew, showSettings, showOnboarding, extractDownloadText, openNew, pushToast]);

  const addTasks = useCallback(
    (added: TaskView[]) => {
      setTasks((prev) => {
        let next = prev;
        for (const t of added) next = upsert(next, t);
        return next;
      });
      if (added.length) {
        setSelected(added[0].id);
        pushToast("success", added.length === 1 ? `已开始：${displayName(added[0])}` : `已添加 ${added.length} 个任务`);
      }
    },
    [pushToast],
  );

  const quickAdd = useCallback(
    async (url: string) => {
      try {
        const target = url.trim();
        const dup = await api.findDuplicate(target, settings?.downloadDir || "", "").catch(() => null);
        if (dup) {
          setFilter(dup.state === "done" ? "done" : dup.state === "paused" || dup.state === "failed" ? "stopped" : "downloading");
          setSelected(dup.id);
          pushToast("info", "该链接已有任务，已为你打开");
          return;
        }
        const t = await api.add({ url: target });
        addTasks([t]);
      } catch (err) {
        pushToast("error", errorText(err));
      }
    },
    [addTasks, pushToast, settings?.downloadDir],
  );

  const pause = useCallback(
    async (t: TaskView) => {
      try {
        await api.pause(t.id);
      } catch (err) {
        pushToast("error", errorText(err));
      }
    },
    [pushToast],
  );

  const resume = useCallback(
    async (t: TaskView) => {
      try {
        await api.resume(t.id);
      } catch (err) {
        pushToast("error", errorText(err));
      }
    },
    [pushToast],
  );

  const openFile = useCallback(
    async (t: TaskView) => {
      if (t.state !== "done") {
        setSelected(t.id);
        return;
      }
      try {
        await api.openFile(t.id);
      } catch (err) {
        pushToast("error", errorText(err));
      }
    },
    [pushToast],
  );

  const reveal = useCallback(
    async (t: TaskView) => {
      try {
        await api.revealFile(t.id);
      } catch (err) {
        pushToast("error", errorText(err));
      }
    },
    [pushToast],
  );

  const remove = useCallback(
    async (t: TaskView) => {
      if (!window.confirm(`移除任务「${displayName(t)}」？已下载的文件不会被删除。`)) return;
      try {
        await api.remove(t.id);
        setTasks((prev) => prev.filter((x) => x.id !== t.id));
        if (selected === t.id) setSelected(null);
        pushToast("success", "任务已移除");
      } catch (err) {
        pushToast("error", errorText(err));
      }
    },
    [selected, pushToast],
  );

  const toggleSelected = useCallback(() => {
    if (!selected) return;
    const t = tasks.find((x) => x.id === selected);
    if (!t) return;
    if (t.state === "paused" || t.state === "failed") resume(t);
    else if (t.state === "running" || t.state === "queued" || t.state === "resolving") pause(t);
  }, [selected, tasks, pause, resume]);

  const pauseAll = useCallback(async () => {
    try {
      const n = await api.pauseAll();
      pushToast("info", n ? `已暂停 ${n} 个任务` : "没有正在运行的任务");
    } catch (err) {
      pushToast("error", errorText(err));
    }
  }, [pushToast]);

  const resumeAll = useCallback(async () => {
    try {
      const n = await api.resumeAll();
      pushToast("info", n ? `已继续 ${n} 个任务` : "没有已暂停的任务");
    } catch (err) {
      pushToast("error", errorText(err));
    }
  }, [pushToast]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null;
      const typing =
        !!el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable);
      const mod = e.ctrlKey || e.metaKey;
      if (mod && e.key.toLowerCase() === "n") {
        e.preventDefault();
        openNew();
        return;
      }
      if (mod && e.key === ",") {
        e.preventDefault();
        setSettingsSection("general");
        setShowSettings(true);
        return;
      }
      if (mod && e.key.toLowerCase() === "f") {
        e.preventDefault();
        searchRef.current?.focus();
        searchRef.current?.select();
        return;
      }
      if (!typing && e.key === "Escape") {
        if (showNew || showSettings) return;
        setSelected(null);
      }
      if (!typing && e.code === "Space" && !e.repeat) {
        if (showNew || showSettings || showOnboarding) return;
        if (selected) {
          e.preventDefault();
          toggleSelected();
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [openNew, selected, showNew, showSettings, showOnboarding, toggleSelected]);

  const onDrop = (e: React.DragEvent) => {
    e.preventDefault();
    setDragOver(false);
    const text = e.dataTransfer.getData("text/uri-list") || e.dataTransfer.getData("text/plain");
    const files = Array.from(e.dataTransfer.files || []);
    const torrent = files.find((f) => f.name.toLowerCase().endsWith(".torrent"));
    if (torrent) {
      const path = (torrent as File & { path?: string }).path;
      if (path) {
        openNew(path);
        pushToast("success", "已识别 .torrent 文件");
        return;
      }
      pushToast("info", "请使用「新建」里的选择 .torrent 文件");
      openNew();
      return;
    }
    const hit = extractDownloadText(text);
    if (hit) {
      openNew(hit);
      if (/^magnet:/i.test(hit)) pushToast("success", "已识别磁力链接");
      return;
    }
    if (text.trim()) openNew(text.trim());
  };

  const onSavedSettings = (s: Settings) => {
    setSettings(s);
    setStatus((prev) => (prev ? { ...prev, settings: s, limit: s.globalLimitBps } : prev));
    refreshStatus();
  };

  const finishOnboarding = (s: Settings) => {
    onSavedSettings(s);
    setShowOnboarding(false);
    pushToast("success", "设置完成，开始下载吧");
  };

  const openExistingTask = useCallback((t: TaskView) => {
    setFilter(t.state === "done" ? "done" : t.state === "paused" || t.state === "failed" ? "stopped" : "downloading");
    setSelected(t.id);
    setShowNew(false);
  }, []);

  const filterTitle: Record<Filter, string> = {
    downloading: "下载中",
    waiting: "等待中",
    stopped: "已停止",
    done: "已完成",
  };

  const copyUrl = useCallback(
    async (t: TaskView) => {
      try {
        await navigator.clipboard.writeText(t.finalUrl || t.url);
        pushToast("success", "链接已复制");
      } catch {
        pushToast("error", "无法复制到剪贴板");
      }
    },
    [pushToast],
  );

  return (
    <div
      className={`app ${dragOver ? "dragging" : ""} ${selected ? "detail-open" : ""}`}
      onDragOver={(e) => {
        e.preventDefault();
        setDragOver(true);
      }}
      onDragLeave={(e) => {
        if (e.currentTarget === e.target) setDragOver(false);
      }}
      onDrop={onDrop}
    >
      <Sidebar
        filter={filter}
        integrationReady={integrationReady}
        extensionLive={extensionLive}
        onFilter={setFilter}
        onNew={() => openNew()}
        onSettings={() => {
          setSettingsSection("general");
          setShowSettings(true);
        }}
        onBrowser={() => {
          setSettingsSection("browser");
          setShowSettings(true);
        }}
        onHelp={() => pushToast("info", `XDL v${status?.version || "0.2.0"} · Ctrl+N 新建 · Ctrl+, 设置`)}
      />

      <main className="workspace">
        <header className="topbar">
          <div className="title-block">
            <h1>{filterTitle[filter]}</h1>
          </div>
          <div className="top-actions">
            <div className="search">
              <Icon name="search" size={14} />
              <input
                ref={searchRef}
                className="xdl-field bare"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                placeholder="搜索任务"
                spellCheck={false}
                aria-label="搜索任务"
              />
              {query && (
                <button className="search-clear" onClick={() => setQuery("")} aria-label="清空搜索">
                  <Icon name="x" size={12} />
                </button>
              )}
            </div>
            {selected && (
              <button className="icon-btn top-icon" onClick={() => setSelected(null)} title="关闭详情">
                <Icon name="x" size={15} />
              </button>
            )}
            <button className="icon-btn top-icon" onClick={() => loadAll(true)} title="刷新列表">
              <Icon name="refresh" size={15} />
            </button>
            <button
              className="icon-btn top-icon"
              onClick={resumeAll}
              title="继续全部"
              disabled={pausedCount === 0}
            >
              <Icon name="play" size={14} />
            </button>
            <button
              className="icon-btn top-icon"
              onClick={pauseAll}
              title="暂停全部"
              disabled={pausableCount === 0}
            >
              <Icon name="pause" size={15} />
            </button>
          </div>
        </header>

        <div className="content">
          {bootError && (
            <div className="boot-error">
              <Icon name="failed" size={17} />
              <span>无法连接下载引擎：{bootError}</span>
              <button className="text-btn small" onClick={() => loadAll()}>
                重试
              </button>
            </div>
          )}
          {!status && !bootError ? (
            <div className="boot-screen">
              <span className="spinner big" />
              <p>正在连接 XDL 引擎…</p>
            </div>
          ) : filtered.length === 0 && tasks.length === 0 ? (
            <EmptyState
              integrationReady={integrationReady}
              extensionLive={extensionLive}
              onNew={() => openNew()}
              onPaste={onPaste}
              onQuickAdd={quickAdd}
            />
          ) : filtered.length === 0 ? (
            <div className="filtered-empty">
              <Icon name="filter" size={22} />
              <p>{query ? `没有匹配「${query}」的任务` : `没有${filterTitle[filter]}的任务`}</p>
              <button
                className="text-btn"
                onClick={() => {
                  setQuery("");
                  setFilter("downloading");
                }}
              >
                查看下载中
              </button>
            </div>
          ) : (
            <TaskTable
              tasks={filtered}
              selected={selected}
              onSelect={setSelected}
              onPause={pause}
              onResume={resume}
              onOpen={openFile}
              onReveal={reveal}
              onCopy={copyUrl}
              onRemove={remove}
              onNew={() => openNew()}
            />
          )}
        </div>

        <button
          className="speed-fab"
          onClick={() => {
            setSettingsSection("download");
            setShowSettings(true);
          }}
          title={
            settings?.globalLimitBps
              ? `限速 ${(settings.globalLimitBps / 1024 / 1024).toFixed(0)} MB/s · 点击设置`
              : `当前 ${totalSpeed > 0 ? `${(totalSpeed / 1024 / 1024).toFixed(2)} MB/s` : "0 MB/s"} · 点击限速`
          }
          aria-label="速度与限速"
        >
          <Icon name="gauge" size={22} />
          {totalSpeed > 0 && <i className="speed-fab-pulse" />}
        </button>
      </main>

      {selected && (
        <DetailPanel
          detail={detail}
          loading={detailLoading}
          onClose={() => setSelected(null)}
          onPause={pause}
          onResume={resume}
          onOpen={openFile}
          onReveal={reveal}
          onRemove={remove}
          onToast={pushToast}
        />
      )}

      {dragOver && (
        <div className="drop-overlay">
          <div className="drop-card">
            <Icon name="download" size={28} />
            <strong>松开以新建下载</strong>
            <span>支持 http(s) / magnet / .torrent</span>
          </div>
        </div>
      )}

      {settings && (
        <NewTaskDialog
          open={showNew}
          defaultDir={settings.downloadDir || ""}
          defaultConns={settings.defaultConns || 8}
          turbo={settings.turbo || false}
          settings={settings}
          prefill={prefill}
          onClose={() => setShowNew(false)}
          onAdded={addTasks}
          onExisting={openExistingTask}
          onSettings={onSavedSettings}
        />
      )}

      {settings && (
        <SettingsDialog
          open={showSettings}
          section={settingsSection}
          settings={settings}
          onClose={() => setShowSettings(false)}
          onSaved={onSavedSettings}
          onSection={setSettingsSection}
          onRefreshApp={refreshStatus}
          onToast={pushToast}
        />
      )}

      {settings && (
        <Onboarding
          open={showOnboarding}
          settings={settings}
          onDone={finishOnboarding}
          onToast={pushToast}
        />
      )}

      <Toasts items={toasts} onDismiss={(id) => setToasts((prev) => prev.filter((t) => t.id !== id))} />
    </div>
  );
}
