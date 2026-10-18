import { useEffect, useMemo, useRef, useState } from "react";
import { api, errorText } from "../api";
import { bytes } from "../format";
import type {
  GrabCandidate,
  GrabResult,
  MediaProbeResult,
  MediaVariant,
  Settings,
  TaskView,
  TorrentProbeResult,
} from "../types";
import { Icon } from "./Icon";
import { Modal } from "./Modal";
import { Select } from "./Select";
import { TextArea, TextField } from "./TextField";

type Mode = "links" | "grab";

interface NewTaskDialogProps {
  open: boolean;
  defaultDir: string;
  defaultConns: number;
  turbo: boolean;
  settings: Settings;
  prefill?: string;
  onClose: () => void;
  onAdded: (tasks: TaskView[]) => void;
  onExisting: (task: TaskView) => void;
  onSettings: (s: Settings) => void;
}

function looksLikeMedia(url: string): boolean {
  const path = url.split("?")[0]?.toLowerCase() || "";
  return path.endsWith(".m3u8") || path.endsWith(".mpd");
}

function looksLikeTorrent(url: string): boolean {
  const s = url.trim();
  return /^magnet:/i.test(s) || /\.torrent$/i.test(s.split("?")[0] || "");
}

function variantLabel(v: MediaVariant): string {
  const bits: string[] = [v.label || v.id];
  if (v.height) bits[0] = `${v.height}p`;
  else if (v.width && v.height) bits[0] = `${v.width}x${v.height}`;
  if (v.bandwidth) bits.push(`${Math.round(v.bandwidth / 1000)} kbps`);
  return bits.filter(Boolean).join(" · ");
}

export function NewTaskDialog({
  open,
  defaultDir,
  defaultConns,
  turbo,
  settings,
  prefill,
  onClose,
  onAdded,
  onExisting,
  onSettings,
}: NewTaskDialogProps) {
  const [urls, setUrls] = useState("");
  const [name, setName] = useState("");
  const [dir, setDir] = useState(defaultDir);
  const [conns, setConns] = useState(defaultConns);
  const [priority, setPriority] = useState(0);
  const [limitBps, setLimitBps] = useState(0);
  const [expectedSha256, setExpectedSha256] = useState("");
  const [mirrors, setMirrors] = useState("");
  const [proxy, setProxy] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [busy, setBusy] = useState(false);
  const [probing, setProbing] = useState(false);
  const [error, setError] = useState("");
  const [duplicate, setDuplicate] = useState<TaskView | null>(null);
  const [probe, setProbe] = useState<MediaProbeResult | null>(null);
  const [variantId, setVariantId] = useState("");
  const [torrentProbe, setTorrentProbe] = useState<TorrentProbeResult | null>(null);
  const [selectedFiles, setSelectedFiles] = useState<Set<number>>(new Set());
  const [legalChecked, setLegalChecked] = useState(false);
  const [mode, setMode] = useState<Mode>("links");
  const [grabDepth, setGrabDepth] = useState(0);
  const [grabResult, setGrabResult] = useState<GrabResult | null>(null);
  const [grabSelected, setGrabSelected] = useState<Set<string>>(new Set());
  const areaRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => {
    if (!open) return;
    setUrls(prefill || "");
    setName("");
    setDir(defaultDir);
    setConns(defaultConns);
    setPriority(0);
    setLimitBps(0);
    setExpectedSha256("");
    setMirrors("");
    setProxy("");
    setAdvanced(false);
    setBusy(false);
    setProbing(false);
    setError("");
    setDuplicate(null);
    setProbe(null);
    setVariantId("");
    setTorrentProbe(null);
    setSelectedFiles(new Set());
    setLegalChecked(settings.btLegalAccepted);
    setMode(prefill && !/^https?:\/\//i.test(prefill.trim()) ? "links" : "links");
    setGrabDepth(0);
    setGrabResult(null);
    setGrabSelected(new Set());
    const timer = setTimeout(() => areaRef.current?.focus(), 40);
    return () => clearTimeout(timer);
  }, [open, prefill, defaultDir, defaultConns, settings.btLegalAccepted]);

  const lines = useMemo(() => urls.split(/\r?\n/).map((s) => s.trim()).filter(Boolean), [urls]);
  const singleMedia = lines.length === 1 && looksLikeMedia(lines[0]);
  const singleTorrent = lines.length === 1 && looksLikeTorrent(lines[0]);
  const needsLegal = singleTorrent && !settings.btLegalAccepted;

  useEffect(() => {
    if (!open || !singleMedia) {
      setProbe(null);
      setVariantId("");
      return;
    }
    let cancelled = false;
    const url = lines[0];
    const timer = setTimeout(() => {
      void (async () => {
        try {
          const out = await api.mediaProbe({ url });
          if (cancelled) return;
          setProbe(out);
          setVariantId(out.variants[0]?.id || "");
          setError("");
        } catch (err) {
          if (cancelled) return;
          setProbe(null);
          setVariantId("");
          setError(errorText(err));
        }
      })();
    }, 280);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [open, singleMedia, lines]);

  useEffect(() => {
    if (!open || !singleTorrent) {
      setTorrentProbe(null);
      setSelectedFiles(new Set());
      return;
    }
    if (needsLegal && !legalChecked) {
      setTorrentProbe(null);
      return;
    }
    let cancelled = false;
    const source = lines[0];
    const timer = setTimeout(() => {
      void (async () => {
        setProbing(true);
        try {
          const out = await api.torrentProbe({
            url: source,
            torrentPath: /\.torrent$/i.test(source) ? source : undefined,
            acceptLegal: legalChecked || settings.btLegalAccepted,
          });
          if (cancelled) return;
          setTorrentProbe(out);
          setSelectedFiles(new Set(out.files.map((f) => f.index)));
          setName((n) => (n.trim() ? n : out.name));
          setError("");
        } catch (err) {
          if (cancelled) return;
          setTorrentProbe(null);
          setSelectedFiles(new Set());
          setError(errorText(err));
        } finally {
          if (!cancelled) setProbing(false);
        }
      })();
    }, 280);
    return () => {
      cancelled = true;
      clearTimeout(timer);
    };
  }, [open, singleTorrent, lines, legalChecked, needsLegal, settings.btLegalAccepted]);

  const toggleFile = (index: number) => {
    setSelectedFiles((prev) => {
      const next = new Set(prev);
      if (next.has(index)) next.delete(index);
      else next.add(index);
      return next;
    });
  };

  const selectAllFiles = (all: boolean) => {
    if (!torrentProbe) return;
    setSelectedFiles(all ? new Set(torrentProbe.files.map((f) => f.index)) : new Set());
  };

  const add = async (skipDuplicate = false) => {
    if (!lines.length) {
      setError("粘贴 http(s) / magnet 链接，或选择 .torrent 文件");
      return;
    }
    if (lines.length > 1 && lines.some((u) => looksLikeTorrent(u))) {
      setError("磁力 / 种子请单独添加（一次一个）");
      return;
    }
    if (singleTorrent) {
      if (needsLegal && !legalChecked) {
        setError("请先确认 BT 使用条款");
        return;
      }
      if (!torrentProbe || selectedFiles.size === 0) {
        setError(probing ? "正在解析种子元数据…" : "请等待文件列表并至少勾选一个文件");
        return;
      }
      setBusy(true);
      setError("");
      try {
        if (needsLegal && legalChecked) {
          const s = await api.acceptBTLegal();
          onSettings(s);
        }
        const t = await api.torrentStart({
          url: lines[0],
          torrentPath: /\.torrent$/i.test(lines[0]) ? lines[0] : undefined,
          dir: dir || undefined,
          name: name.trim() || torrentProbe.name,
          infoHash: torrentProbe.infoHash,
          selected: Array.from(selectedFiles).sort((a, b) => a - b),
          seedAfter: settings.btSeedAfterDone,
          acceptLegal: true,
        });
        onAdded([t]);
        onClose();
      } catch (err) {
        setError(errorText(err));
      } finally {
        setBusy(false);
      }
      return;
    }

    const singleName = lines.length === 1 ? name.trim() : "";
    if (!skipDuplicate && lines.length === 1) {
      setBusy(true);
      try {
        const found = await api.findDuplicate(lines[0], dir || "", singleName);
        if (found) {
          setDuplicate(found);
          setError("");
          setBusy(false);
          return;
        }
      } catch {
        /* duplicate check is advisory */
      }
      setBusy(false);
    }
    setDuplicate(null);
    setBusy(true);
    setError("");
    const added: TaskView[] = [];
    const failures: string[] = [];
    for (const url of lines) {
      try {
        let t: TaskView;
        if (looksLikeMedia(url)) {
          const picked =
            lines.length === 1 && probe
              ? probe.variants.find((v) => v.id === variantId) || probe.variants[0]
              : undefined;
          t = await api.mediaStart({
            url,
            name: lines.length === 1 ? name.trim() || undefined : undefined,
            dir: dir || undefined,
            conns,
            kind: probe?.kind || (url.toLowerCase().includes(".mpd") ? "dash" : "hls"),
            variantId: picked?.id,
            label: picked?.label,
            priority: priority || undefined,
            limitBps: limitBps || undefined,
            expectedSha256: expectedSha256.trim() || undefined,
            proxy: proxy.trim() || undefined,
          });
        } else {
          const mirrorList =
            lines.length === 1
              ? mirrors
                  .split(/\r?\n/)
                  .map((s) => s.trim())
                  .filter(Boolean)
              : undefined;
          t = await api.add({
            url,
            name: lines.length === 1 ? name.trim() : undefined,
            dir: dir || undefined,
            conns,
            priority: priority || undefined,
            limitBps: limitBps || undefined,
            expectedSha256: expectedSha256.trim() || undefined,
            mirrors: mirrorList,
            proxy: proxy.trim() || undefined,
          });
        }
        added.push(t);
      } catch (err) {
        failures.push(`${url}: ${errorText(err)}`);
      }
    }
    setBusy(false);
    if (added.length) onAdded(added);
    if (failures.length) {
      setError(failures.slice(0, 3).join("\n"));
    } else {
      onClose();
    }
  };

  const chooseDir = async () => {
    try {
      const p = await api.chooseDirectory();
      if (p) setDir(p);
    } catch (err) {
      setError(errorText(err));
    }
  };

  const pickTorrent = async () => {
    try {
      const p = await api.chooseTorrentFile();
      if (p) {
        setUrls(p);
        setDuplicate(null);
        setTorrentProbe(null);
      }
    } catch (err) {
      setError(errorText(err));
    }
  };

  const importList = async () => {
    try {
      const p = await api.chooseURLListFile();
      if (!p) return;
      const text = await api.readTextFile(p);
      setMode("links");
      setUrls((prev) => (prev.trim() ? prev.trim() + "\n" + text : text));
      setError("");
    } catch (err) {
      setError(errorText(err));
    }
  };

  const runGrab = async () => {
    const page = urls.split(/\r?\n/).map((s) => s.trim()).filter(Boolean)[0];
    if (!page) {
      setError("输入要扫描的页面 URL");
      return;
    }
    setBusy(true);
    setError("");
    setGrabResult(null);
    try {
      const res = await api.grabProbe({
        url: page,
        maxDepth: grabDepth,
        maxPages: grabDepth === 0 ? 1 : 50,
        sameOrigin: true,
      });
      setGrabResult(res);
      setGrabSelected(new Set(res.candidates.map((c) => c.url)));
      if (!res.candidates.length) {
        setError("未找到可下载资源（可调整深度或换页面）");
      }
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const enqueueGrab = async () => {
    if (!grabSelected.size) {
      setError("请至少勾选一个资源");
      return;
    }
    setBusy(true);
    setError("");
    try {
      const res = await api.grabEnqueue({
        urls: Array.from(grabSelected),
        dir: dir || undefined,
        conns,
      });
      if (res.added.length) onAdded(res.added);
      if (res.failures?.length) {
        setError(`已添加 ${res.added.length} 个；失败 ${res.failures.length}\n` + res.failures.slice(0, 5).join("\n"));
      } else {
        onClose();
      }
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const toggleGrab = (url: string) => {
    setGrabSelected((prev) => {
      const next = new Set(prev);
      if (next.has(url)) next.delete(url);
      else next.add(url);
      return next;
    });
  };

  const selectGrabAll = (all: boolean) => {
    if (!grabResult) return;
    setGrabSelected(all ? new Set(grabResult.candidates.map((c: GrabCandidate) => c.url)) : new Set());
  };

  const canSubmit =
    mode === "grab"
      ? grabSelected.size > 0 && !busy
      : !!urls.trim() &&
        !probing &&
        (!singleTorrent || (torrentProbe != null && selectedFiles.size > 0 && (!needsLegal || legalChecked)));

  return (
    <Modal
      open={open}
      title="新建下载"
      subtitle="链接批量 / 网站抓取 / HLS·DASH / magnet·torrent"
      onClose={onClose}
      width={640}
      footer={
        <>
          <span className="foot-hint">
            {mode === "grab"
              ? grabResult
                ? `已选 ${grabSelected.size} / ${grabResult.candidates.length} · 扫过 ${grabResult.pages} 页`
                : "先扫描页面再勾选资源入队"
              : singleTorrent
                ? probing
                  ? "正在获取元数据…"
                  : torrentProbe
                    ? `已选 ${selectedFiles.size} / ${torrentProbe.files.length} 个文件`
                    : "磁力链接解析可能需要数十秒"
                : lines.length > 1
                  ? `${lines.length} 个链接`
                  : "回车换行可批量添加"}
          </span>
          <button className="btn ghost" onClick={onClose} disabled={busy}>
            取消
          </button>
          {mode === "grab" ? (
            <>
              <button className="btn ghost" onClick={() => void runGrab()} disabled={busy || !urls.trim()}>
                {busy && !grabResult ? <span className="spinner" /> : <Icon name="search" size={15} />}
                扫描
              </button>
              <button className="btn" onClick={() => void enqueueGrab()} disabled={!canSubmit}>
                {busy ? <span className="spinner" /> : <Icon name="download" size={15} />}
                下载所选
              </button>
            </>
          ) : (
            <button className="btn" onClick={() => add()} disabled={busy || !canSubmit}>
              {busy || probing ? <span className="spinner" /> : <Icon name="download" size={15} />}
              {busy ? "正在添加…" : probing ? "解析中…" : "开始下载"}
            </button>
          )}
        </>
      }
    >
      <div className="new-form">
        <div className="mode-tabs">
          <button
            type="button"
            className={mode === "links" ? "active" : ""}
            onClick={() => setMode("links")}
          >
            链接 / 批量
          </button>
          <button
            type="button"
            className={mode === "grab" ? "active" : ""}
            onClick={() => setMode("grab")}
          >
            网站抓取
          </button>
        </div>

        <label className="field">
          <span className="field-label">{mode === "grab" ? "页面地址" : "下载链接"}</span>
          <TextArea
            ref={areaRef}
            className="url-input"
            rows={mode === "grab" ? 2 : 4}
            spellCheck={false}
            placeholder={
              mode === "grab"
                ? "https://example.com/files/"
                : "https://example.com/file.zip\nmagnet:?xt=urn:btih:…\n或 .m3u8 / .mpd / .torrent"
            }
            value={urls}
            onChange={(e) => {
              setUrls(e.target.value);
              setDuplicate(null);
              setTorrentProbe(null);
              setGrabResult(null);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
                e.preventDefault();
                if (mode === "grab") void runGrab();
                else add();
              }
            }}
          />
        </label>

        {mode === "links" && (
          <div className="form-row" style={{ marginTop: -4, gap: 8 }}>
            <button className="btn ghost small" type="button" onClick={() => void pickTorrent()}>
              <Icon name="folder" size={14} />
              选择 .torrent
            </button>
            <button className="btn ghost small" type="button" onClick={() => void importList()}>
              <Icon name="paste" size={14} />
              导入 txt 列表
            </button>
          </div>
        )}

        {mode === "grab" && (
          <>
            <div className="form-row">
              <label className="field">
                <span className="field-label">抓取深度</span>
                <Select
                  aria-label="抓取深度"
                  value={grabDepth}
                  onChange={setGrabDepth}
                  options={[
                    { value: 0, label: "仅当前页" },
                    { value: 1, label: "同源深度 1" },
                    { value: 2, label: "同源深度 2（最多 50 页）" },
                  ]}
                />
              </label>
            </div>
            {grabResult && grabResult.candidates.length > 0 && (
              <div className="field">
                <div className="field-label row-between">
                  <span>发现 {grabResult.candidates.length} 个资源</span>
                  <span className="torrent-select-actions">
                    <button className="text-btn small" type="button" onClick={() => selectGrabAll(true)}>
                      全选
                    </button>
                    <button className="text-btn small" type="button" onClick={() => selectGrabAll(false)}>
                      清空
                    </button>
                  </span>
                </div>
                <div className="torrent-file-list grab-cand-list">
                  {grabResult.candidates.map((c) => (
                    <label key={c.url} className="torrent-file-row">
                      <input
                        type="checkbox"
                        checked={grabSelected.has(c.url)}
                        onChange={() => toggleGrab(c.url)}
                      />
                      <span className="torrent-file-path" title={c.url}>
                        {c.ext ? `[${c.ext}] ` : ""}
                        {c.text || c.url}
                      </span>
                      <span className="torrent-file-size">{c.source}</span>
                    </label>
                  ))}
                </div>
              </div>
            )}
          </>
        )}

        {mode === "links" && needsLegal && (
          <label className="field legal-check">
            <input
              type="checkbox"
              checked={legalChecked}
              onChange={(e) => setLegalChecked(e.target.checked)}
            />
            <span>
              我确认仅下载有权使用的内容。XDL 不提供种子搜索/索引；默认完成后停止做种。首次使用需勾选同意。
            </span>
          </label>
        )}

        {mode === "links" && singleTorrent && torrentProbe && (
          <div className="field">
            <div className="field-label row-between">
              <span>
                文件列表 · {bytes(torrentProbe.totalSize)} · {torrentProbe.infoHash.slice(0, 8)}…
              </span>
              <span className="torrent-select-actions">
                <button className="text-btn small" type="button" onClick={() => selectAllFiles(true)}>
                  全选
                </button>
                <button className="text-btn small" type="button" onClick={() => selectAllFiles(false)}>
                  清空
                </button>
              </span>
            </div>
            <div className="torrent-file-list">
              {torrentProbe.files.map((f) => (
                <label key={f.index} className="torrent-file-row">
                  <input
                    type="checkbox"
                    checked={selectedFiles.has(f.index)}
                    onChange={() => toggleFile(f.index)}
                  />
                  <span className="torrent-file-path" title={f.path}>
                    {f.path}
                  </span>
                  <span className="torrent-file-size">{bytes(f.length)}</span>
                </label>
              ))}
            </div>
          </div>
        )}

        {mode === "links" && singleMedia && probe && probe.variants.length > 0 && (
          <label className="field">
            <span className="field-label">清晰度（{probe.kind.toUpperCase()}）</span>
            <Select
              aria-label="清晰度"
              value={variantId}
              onChange={setVariantId}
              options={probe.variants.map((v) => ({ value: v.id, label: variantLabel(v) }))}
            />
          </label>
        )}

        <div className="form-row">
          <label className="field">
            <span className="field-label">保存到</span>
            <span className="input-with-button">
              <TextField value={dir} onChange={(e) => setDir(e.target.value)} spellCheck={false} aria-label="保存位置" />
              <button className="icon-btn bordered" title="选择文件夹" onClick={chooseDir} type="button">
                <Icon name="folder" size={15} />
              </button>
            </span>
          </label>
        </div>

        <button className="advanced-toggle" onClick={() => setAdvanced(!advanced)} type="button">
          <Icon name="chevron" size={13} className={advanced ? "rotated" : ""} />
          高级选项
        </button>

        {advanced && (
          <div className="advanced-grid">
            <label className="field">
              <span className="field-label">文件名（可选）</span>
              <TextField
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="留空则使用服务器/种子名称"
                disabled={lines.length > 1}
                aria-label="文件名"
              />
            </label>
            {!singleTorrent && (
              <label className="field">
                <span className="field-label">连接数</span>
                <Select
                  aria-label="连接数"
                  value={conns}
                  onChange={setConns}
                  options={[4, 8, 16, ...(turbo ? [32] : [])].map((n) => ({
                    value: n,
                    label: `${n} 连接${n === 8 ? "（推荐）" : ""}`,
                  }))}
                />
              </label>
            )}
            <label className="field">
              <span className="field-label">优先级</span>
              <Select
                aria-label="优先级"
                value={priority}
                onChange={setPriority}
                options={[
                  { value: 2, label: "高" },
                  { value: 0, label: "普通" },
                  { value: -1, label: "低" },
                ]}
              />
            </label>
            {!singleTorrent && (
              <label className="field">
                <span className="field-label">单任务限速</span>
                <Select
                  aria-label="单任务限速"
                  value={limitBps}
                  onChange={setLimitBps}
                  options={[
                    { value: 0, label: "不限速" },
                    { value: 512 * 1024, label: "512 KB/s" },
                    { value: 1 << 20, label: "1 MB/s" },
                    { value: 3 << 20, label: "3 MB/s" },
                    { value: 5 << 20, label: "5 MB/s" },
                  ]}
                />
              </label>
            )}
            {lines.length === 1 && !singleTorrent && !singleMedia && (
              <label className="field" style={{ gridColumn: "1 / -1" }}>
                <span className="field-label">镜像地址（可选，一行一个）</span>
                <TextArea
                  value={mirrors}
                  onChange={(e) => setMirrors(e.target.value)}
                  placeholder={"同一文件的其他 http(s) 或 ftp 地址"}
                  spellCheck={false}
                  rows={3}
                  aria-label="镜像地址"
                />
              </label>
            )}
            {!singleTorrent && (
              <label className="field" style={{ gridColumn: "1 / -1" }}>
                <span className="field-label">任务代理（可选）</span>
                <TextField
                  value={proxy}
                  onChange={(e) => setProxy(e.target.value)}
                  placeholder="留空用环境代理；direct 为直连；或 http(s):// / socks5://"
                  spellCheck={false}
                  aria-label="任务代理"
                />
              </label>
            )}
            {!singleTorrent && !singleMedia && (
              <label className="field" style={{ gridColumn: "1 / -1" }}>
                <span className="field-label">期望 SHA-256（可选）</span>
                <TextField
                  value={expectedSha256}
                  onChange={(e) => setExpectedSha256(e.target.value)}
                  placeholder="网页声明的哈希，下载完成后自动校验"
                  spellCheck={false}
                  aria-label="期望 SHA-256"
                />
              </label>
            )}
          </div>
        )}

        {duplicate && (
          <div className="duplicate-hint">
            <div className="duplicate-copy">
              <strong>已存在相同链接的任务</strong>
              <span title={duplicate.name || duplicate.url}>{duplicate.name || duplicate.url}</span>
            </div>
            <div className="duplicate-actions">
              <button className="text-btn small" onClick={() => onExisting(duplicate)}>
                查看已有任务
              </button>
              <button className="btn ghost small" onClick={() => void add(true)} disabled={busy}>
                仍要新建
              </button>
            </div>
          </div>
        )}
        {error && <pre className="form-error">{error}</pre>}
        <p className="form-hint">
          {mode === "grab"
            ? "抓取遵守 robots.txt，默认同源、限速访问；预览勾选后再入队。不会绕过登录墙或 DRM。"
            : "多行 / txt 导入会去重后批量入队。.meta4 / .metalink 会展开成多镜像任务。FTP 为单连接续传。SFTP 未接入。"}
        </p>
      </div>
    </Modal>
  );
}
