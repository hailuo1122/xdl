import { useEffect, useState } from "react";
import { api, errorText } from "../api";
import type { IntegrationStatus, Settings } from "../types";
import { Icon } from "./Icon";
import { Modal } from "./Modal";
import { Select } from "./Select";
import { Switch } from "./Switch";
import { TextField } from "./TextField";

type Section = "general" | "download" | "browser" | "bt" | "update";

const btUploadLimits = [
  { value: 0, label: "不限速" },
  { value: 64 * 1024, label: "64 KB/s" },
  { value: 128 * 1024, label: "128 KB/s" },
  { value: 512 * 1024, label: "512 KB/s" },
  { value: 1 << 20, label: "1 MB/s" },
];

interface SettingsDialogProps {
  open: boolean;
  section: Section;
  settings: Settings;
  onClose: () => void;
  onSaved: (s: Settings) => void;
  onSection: (s: Section) => void;
  onRefreshApp: () => void;
  onToast: (kind: "info" | "success" | "error", text: string) => void;
}

const limits = [
  { value: 0, label: "不限速" },
  { value: 1 << 20, label: "1 MB/s" },
  { value: 3 << 20, label: "3 MB/s" },
  { value: 5 << 20, label: "5 MB/s" },
  { value: 10 << 20, label: "10 MB/s" },
];

export function SettingsDialog({
  open,
  section,
  settings,
  onClose,
  onSaved,
  onSection,
  onRefreshApp,
  onToast,
}: SettingsDialogProps) {
  const [draft, setDraft] = useState<Settings>(settings);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [integration, setIntegration] = useState<IntegrationStatus | null>(null);
  const [extPath, setExtPath] = useState("");
  const [repairing, setRepairing] = useState(false);

  useEffect(() => {
    if (!open) return;
    setDraft(settings);
    setError("");
    setBusy(false);
    api.integration().then(setIntegration).catch(() => {});
    api.extensionPath().then(setExtPath).catch(() => {});
  }, [open, settings]);

  const patch = (p: Partial<Settings>) => setDraft((d) => ({ ...d, ...p }));

  const chooseDir = async () => {
    try {
      const p = await api.chooseDirectory();
      if (p) patch({ downloadDir: p });
    } catch (err) {
      setError(errorText(err));
    }
  };

  const save = async () => {
    setBusy(true);
    setError("");
    try {
      const out = await api.saveSettings(draft);
      onSaved(out);
      onToast("success", "设置已保存");
      onClose();
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  const repair = async () => {
    setRepairing(true);
    try {
      const st = await api.repairIntegration();
      setIntegration(st);
      onRefreshApp();
      onToast("success", "浏览器集成已修复");
    } catch (err) {
      onToast("error", `修复失败：${errorText(err)}`);
    } finally {
      setRepairing(false);
    }
  };

  const copy = async (text: string, label: string) => {
    try {
      await navigator.clipboard.writeText(text);
      onToast("success", `${label}已复制`);
    } catch {
      onToast("error", "复制失败");
    }
  };

  const copyDiagnostics = async () => {
    try {
      const text = await api.diagnostics();
      await copy(text, "诊断信息");
    } catch (err) {
      onToast("error", `读取诊断失败：${errorText(err)}`);
    }
  };

  const host = integration?.host;
  const live = integration?.extensionLive;

  return (
    <Modal
      open={open}
      title="设置"
      subtitle="更改会立即生效；连接数等参数应用于新任务。"
      onClose={onClose}
      width={860}
      className="settings-modal"
      footer={
        <>
          <span className="foot-hint">{error ? <span className="danger-text">{error}</span> : "XDL internal alpha"}</span>
          <button className="btn ghost" onClick={onClose} disabled={busy}>
            取消
          </button>
          <button className="btn" onClick={save} disabled={busy}>
            {busy && <span className="spinner" />}
            保存设置
          </button>
        </>
      }
    >
      <div className="settings-layout">
        <nav className="settings-nav">
          <button className={section === "general" ? "active" : ""} onClick={() => onSection("general")}>
            <Icon name="settings" size={16} />常规
          </button>
          <button className={section === "download" ? "active" : ""} onClick={() => onSection("download")}>
            <Icon name="download" size={16} />下载与带宽
          </button>
          <button className={section === "browser" ? "active" : ""} onClick={() => onSection("browser")}>
            <Icon name="browser" size={16} />浏览器接管
          </button>
          <button className={section === "bt" ? "active" : ""} onClick={() => onSection("bt")}>
            <Icon name="download" size={16} />BT / 磁力
          </button>
          <button className={section === "update" ? "active" : ""} onClick={() => onSection("update")}>
            <Icon name="sliders" size={16} />更新
          </button>
        </nav>

        <div className="settings-content">
          {section === "general" && (
            <>
              <div className="settings-group">
                <h3>启动与窗口</h3>
                <div className="setting-row">
                  <div>
                    <span>开机时启动 XDL</span>
                    <small>写入当前用户的启动项，可随时关闭。</small>
                  </div>
                  <Switch checked={draft.launchAtLogin} onChange={(v) => patch({ launchAtLogin: v })} />
                </div>
                <div className="setting-row">
                  <div>
                    <span>启动时最小化到托盘</span>
                    <small>开机自启时不在桌面上弹窗。</small>
                  </div>
                  <Switch checked={draft.startMinimized} onChange={(v) => patch({ startMinimized: v })} />
                </div>
                <div className="setting-row">
                  <div>
                    <span>关闭窗口时留在托盘</span>
                    <small>下载会继续进行。从托盘菜单退出才会停止应用。</small>
                  </div>
                  <Switch checked={draft.closeToTray} onChange={(v) => patch({ closeToTray: v })} />
                </div>
              </div>
              <div className="settings-group">
                <h3>通知与外观</h3>
                <div className="setting-row">
                  <div>
                    <span>下载完成 / 失败时通知</span>
                    <small>使用 Windows 通知中心。</small>
                  </div>
                  <Switch checked={draft.notifications} onChange={(v) => patch({ notifications: v })} />
                </div>
                <div className="setting-row">
                  <div>
                    <span>主题</span>
                    <small>默认浅色，也可以切换深色或跟随系统。</small>
                  </div>
                  <Select
                    aria-label="主题"
                    value={draft.theme}
                    onChange={(v) => patch({ theme: v })}
                    options={[
                      { value: "dark", label: "深色" },
                      { value: "light", label: "浅色" },
                      { value: "system", label: "跟随系统" },
                    ]}
                  />
                </div>
              </div>
            </>
          )}

          {section === "download" && (
            <>
              <div className="settings-group">
                <h3>保存位置</h3>
                <div className="setting-row">
                  <div>
                    <span>默认下载目录</span>
                    <small>{draft.downloadDir || "未设置"}</small>
                  </div>
                  <button className="btn ghost small" onClick={chooseDir}>
                    <Icon name="folder" size={14} />更改
                  </button>
                </div>
              </div>
              <div className="settings-group">
                <h3>aria2 接口</h3>
                <div className="setting-row">
                  <div>
                    <span>接受 aria2 扩展发送</span>
                    <small>只监听 127.0.0.1。AriaNg、浏览器「发送到 aria2」把地址填成这个端口即可。</small>
                  </div>
                  <Switch checked={!!draft.aria2Enabled} onChange={(v) => patch({ aria2Enabled: v })} />
                </div>
                <div className="setting-row">
                  <div>
                    <span>端口</span>
                    <small>JSON-RPC 地址 http://127.0.0.1:{draft.aria2Port || 6800}/jsonrpc</small>
                  </div>
                  <TextField
                    style={{ width: 96 }}
                    inputMode="numeric"
                    aria-label="端口"
                    value={draft.aria2Port || 6800}
                    onChange={(e) => patch({ aria2Port: Math.max(1, Math.min(65535, Number(e.target.value) || 6800)) })}
                  />
                </div>
                <div className="setting-row">
                  <div>
                    <span>RPC 密钥</span>
                    <small>留空则本机调用不需要密钥。填写后，扩展里的密钥要一致，参数形式为 token:密钥。</small>
                  </div>
                  <TextField
                    style={{ width: 180 }}
                    aria-label="RPC 密钥"
                    value={draft.aria2Secret || ""}
                    placeholder="可留空"
                    onChange={(e) => patch({ aria2Secret: e.target.value })}
                  />
                </div>
              </div>
              <div className="settings-group">
                <h3>并发与连接</h3>
                <div className="setting-row">
                  <div>
                    <span>同时下载任务数</span>
                    <small>超出数量的任务会排队。</small>
                  </div>
                  <Select
                    aria-label="同时下载任务数"
                    value={draft.maxConcurrent}
                    onChange={(v) => patch({ maxConcurrent: v })}
                    options={[1, 2, 3, 4, 5, 6, 8].map((n) => ({ value: n, label: String(n) }))}
                  />
                </div>
                <div className="setting-row">
                  <div>
                    <span>每个任务连接数</span>
                    <small>默认 8。服务器不支持 Range 时自动降为单连接。</small>
                  </div>
                  <Select
                    aria-label="每个任务连接数"
                    value={draft.defaultConns}
                    onChange={(v) => patch({ defaultConns: v })}
                    options={[4, 8, 16, ...(draft.turbo ? [32] : [])].map((n) => ({
                      value: n,
                      label: String(n),
                    }))}
                  />
                </div>
                <div className="setting-row">
                  <div>
                    <span>Turbo 模式</span>
                    <small>允许最多 32 个连接。部分站点会限流，默认关闭。</small>
                  </div>
                  <Switch
                    checked={draft.turbo}
                    onChange={(v) =>
                      patch({
                        turbo: v,
                        defaultConns: !v && draft.defaultConns > 16 ? 16 : draft.defaultConns,
                      })
                    }
                  />
                </div>
              </div>
              <div className="settings-group">
                <h3>视频 / ffmpeg</h3>
                <div className="setting-row">
                  <div>
                    <span>ffmpeg 路径（可选）</span>
                    <small>用于 TS→MP4 与音视频合并。留空则自动查 PATH 与 %LOCALAPPDATA%\XDL\bin。</small>
                  </div>
                  <TextField
                    style={{ width: 260 }}
                    value={draft.ffmpegPath || ""}
                    placeholder="例如 C:\ffmpeg\bin\ffmpeg.exe"
                    onChange={(e) => patch({ ffmpegPath: e.target.value })}
                    spellCheck={false}
                    aria-label="ffmpeg 路径"
                  />
                </div>
              </div>
              <div className="settings-group">
                <h3>带宽</h3>
                <div className="setting-row">
                  <div>
                    <span>全局限速</span>
                    <small>对所有任务生效，修改后立即生效。单任务还可在新建/详情里单独限速。</small>
                  </div>
                  <Select
                    aria-label="全局限速"
                    value={draft.globalLimitBps}
                    onChange={(v) => patch({ globalLimitBps: v })}
                    options={limits}
                  />
                </div>
              </div>
              <div className="settings-group">
                <h3>队列规则</h3>
                <div className="setting-row">
                  <div>
                    <span>同域名并发上限</span>
                    <small>限制同一主机同时下载的任务数，减轻限流。0 表示不额外限制。</small>
                  </div>
                  <Select
                    aria-label="同域名并发上限"
                    value={draft.domainConcurrency ?? 2}
                    onChange={(v) => patch({ domainConcurrency: v })}
                    options={[0, 1, 2, 3, 4].map((n) => ({
                      value: n,
                      label: n === 0 ? "不限制" : String(n),
                    }))}
                  />
                </div>
                <div className="setting-row">
                  <div>
                    <span>剪贴板监听</span>
                    <small>检测到磁力链接、.torrent 路径或 http(s) 链接时自动打开新建对话框。</small>
                  </div>
                  <Switch
                    checked={!!draft.watchClipboard}
                    onChange={(v) => patch({ watchClipboard: v })}
                  />
                </div>
              </div>
              <div className="settings-group">
                <h3>自动分类</h3>
                <div className="setting-row">
                  <div>
                    <span>按扩展名分到子目录</span>
                    <small>视频→Videos、压缩包→Archives 等；手动指定目录时不覆盖。</small>
                  </div>
                  <Switch
                    checked={!!draft.autoCategory}
                    onChange={(v) => patch({ autoCategory: v })}
                  />
                </div>
              </div>
              <div className="settings-group">
                <h3>计划暂停</h3>
                <div className="setting-row">
                  <div>
                    <span>启用时段暂停</span>
                    <small>到点自动暂停全部任务并停止新任务开工；离开时段后恢复。</small>
                  </div>
                  <Switch
                    checked={!!draft.scheduleEnabled}
                    onChange={(v) => patch({ scheduleEnabled: v })}
                  />
                </div>
                {draft.scheduleEnabled && (
                  <div className="setting-row">
                    <div>
                      <span>暂停时段（本地时间）</span>
                      <small>支持跨午夜，例如 22 → 7。</small>
                    </div>
                    <span className="schedule-hours">
                      <Select
                        aria-label="暂停开始时刻"
                        value={draft.schedulePauseStart ?? 0}
                        onChange={(v) => patch({ schedulePauseStart: v })}
                        options={Array.from({ length: 24 }, (_, h) => ({
                          value: h,
                          label: `${String(h).padStart(2, "0")}:00`,
                        }))}
                      />
                      <span>→</span>
                      <Select
                        aria-label="暂停结束时刻"
                        value={draft.schedulePauseEnd ?? 0}
                        onChange={(v) => patch({ schedulePauseEnd: v })}
                        options={Array.from({ length: 24 }, (_, h) => ({
                          value: h,
                          label: `${String(h).padStart(2, "0")}:00`,
                        }))}
                      />
                    </span>
                  </div>
                )}
              </div>
              <div className="settings-note">
                <Icon name="shield" size={16} />
                <p>
                  数据连接固定使用 HTTP/1.1，让每条连接拥有独立拥塞窗口。遇到 429 时会自动降低并发并尊重
                  Retry-After，不会绕过站点风控。
                </p>
              </div>
            </>
          )}

          {section === "browser" && (
            <>
              <div className={`integration-hero ${host?.installed ? "ok" : ""}`}>
                <span className={`integration-orb ${live ? "live" : host?.installed ? "ready" : "idle"}`}>
                  <Icon name="browser" size={22} />
                </span>
                <div>
                  <h3>
                    {live
                      ? "扩展已连接，可以接管下载"
                      : host?.installed
                        ? "已安装浏览器组件，等待扩展连接"
                        : "还没有安装浏览器组件"}
                  </h3>
                  <p>
                    {live
                      ? "在 Chrome / Edge 里点击下载链接，任务会自动出现在 XDL。"
                      : "安装组件后，在浏览器里加载扩展即可接管下载。"}
                  </p>
                </div>
                <button className="btn" onClick={repair} disabled={repairing}>
                  {repairing ? <span className="spinner" /> : <Icon name="shield" size={15} />}
                  {host?.installed ? "重新注册" : "安装组件"}
                </button>
              </div>

              <div className="settings-group">
                <h3>浏览器注册状态</h3>
                {(host?.browsers || []).map((b) => (
                  <div className="browser-row" key={b.browser}>
                    <span>{b.browser}</span>
                    <span className="browser-state">
                      <i className={`dot ${b.installed ? "live" : "idle"}`} />
                      {b.installed ? "已注册" : "未注册"}
                    </span>
                  </div>
                ))}
                <div className="browser-row">
                  <span>扩展 ID</span>
                  <span className="browser-state mono">
                    {integration?.extensionId || "—"}
                    <button
                      className="icon-btn"
                      title="复制扩展 ID"
                      onClick={() => integration && copy(integration.extensionId, "扩展 ID")}
                    >
                      <Icon name="copy" size={13} />
                    </button>
                  </span>
                </div>
                {host?.hostPath && (
                  <div className="browser-row">
                    <span>宿主程序</span>
                    <span className="browser-state mono truncate" title={host.hostPath}>
                      {host.hostPath}
                    </span>
                  </div>
                )}
              </div>

              <div className="settings-group">
                <h3>加载开发版扩展</h3>
                <ol className="steps">
                  <li>点击上方「安装组件 / 重新注册」，让浏览器能找到 XDL。</li>
                  <li>
                    在地址栏打开 <code>chrome://extensions</code> 或 <code>edge://extensions</code>，开启「开发者模式」。
                  </li>
                  <li>
                    选择「加载已解压的扩展程序」，选中扩展目录
                    {extPath ? (
                      <>
                        ：<code className="path-code">{extPath}</code>
                        <button className="text-btn small" onClick={() => copy(extPath, "扩展路径")}>
                          复制路径
                        </button>
                        <button
                          className="text-btn small"
                          onClick={() => api.openExtensionFolder().catch((err) => onToast("error", errorText(err)))}
                        >
                          打开文件夹
                        </button>
                      </>
                    ) : (
                      "（尚未找到扩展文件，请先运行 scripts/build-alpha.ps1）"
                    )}
                  </li>
                  <li>刷新已打开的网页，然后点击任意下载链接测试接管。</li>
                </ol>
                <p className="settings-hint">
                  接管设置（智能 / 询问 / 关闭）在扩展的「选项」页里调整。失败时自动回退浏览器下载。
                </p>
              </div>

              <div className="settings-group">
                <h3>诊断</h3>
                <div className="setting-row">
                  <div>
                    <span>运行日志</span>
                    <small>日志位于数据目录的 logs 文件夹，不记录 Cookie 或 Authorization。</small>
                  </div>
                  <div className="duplicate-actions">
                    <button className="text-btn small" onClick={copyDiagnostics}>
                      复制诊断
                    </button>
                    <button
                      className="text-btn small"
                      onClick={() => api.openLogFolder().catch((err) => onToast("error", errorText(err)))}
                    >
                      打开日志目录
                    </button>
                  </div>
                </div>
              </div>

              <div className="settings-note">
                <Icon name="shield" size={16} />
                <p>
                  Cookie 和 Referer 只在提交任务时从浏览器本地转交，不经过任何服务器；XDL 不上传浏览数据。
                </p>
              </div>
            </>
          )}

          {section === "bt" && (
            <>
              <div className="settings-group">
                <h3>做种与连接</h3>
                <div className="setting-row">
                  <div>
                    <span>完成后继续做种</span>
                    <small>默认关闭。开启后仍受上传限速约束。</small>
                  </div>
                  <Switch
                    checked={!!draft.btSeedAfterDone}
                    onChange={(v) => patch({ btSeedAfterDone: v })}
                  />
                </div>
                <div className="setting-row">
                  <div>
                    <span>上传限速</span>
                    <small>全局 BT 上传上限，默认 128 KB/s。</small>
                  </div>
                  <Select
                    aria-label="上传限速"
                    value={draft.btUploadLimitBps ?? 128 * 1024}
                    onChange={(v) => patch({ btUploadLimitBps: v })}
                    options={btUploadLimits}
                  />
                </div>
                <div className="setting-row">
                  <div>
                    <span>每任务最大 Peers</span>
                    <small>默认 50，过高可能占用带宽与端口。</small>
                  </div>
                  <Select
                    aria-label="每任务最大 Peers"
                    value={draft.btMaxPeers || 50}
                    onChange={(v) => patch({ btMaxPeers: v })}
                    options={[20, 50, 80, 100, 150].map((n) => ({ value: n, label: String(n) }))}
                  />
                </div>
              </div>
              <div className="settings-group">
                <h3>法律提示</h3>
                <div className="setting-row">
                  <div>
                    <span>已确认使用条款</span>
                    <small>仅下载有权使用的内容；无内置搜索/索引。</small>
                  </div>
                  <Switch
                    checked={!!draft.btLegalAccepted}
                    onChange={(v) => patch({ btLegalAccepted: v })}
                  />
                </div>
              </div>
              <div className="settings-note">
                <Icon name="shield" size={16} />
                <p>
                  BT 使用 anacrolix/torrent（MPL-2.0）。XDL 不修改其源码；不提供种子搜索、私有 Tracker
                  自动化或 DRM 绕过。
                </p>
              </div>
            </>
          )}

          {section === "update" && (
            <>
              <div className="settings-group">
                <h3>自动更新</h3>
                <div className="setting-row">
                  <div>
                    <span>更新清单 URL（HTTPS）</span>
                    <small>JSON：version / url / sha256 / signature。未配置时仅显示当前版本。</small>
                  </div>
                  <TextField
                    style={{ width: 280 }}
                    value={draft.updateManifestUrl || ""}
                    placeholder="https://example.com/xdl/latest.json"
                    onChange={(e) => patch({ updateManifestUrl: e.target.value })}
                    spellCheck={false}
                    aria-label="更新清单地址"
                  />
                </div>
                <div className="setting-row">
                  <div>
                    <span>检查更新</span>
                    <small>校验 HTTPS 清单与 SHA-256；正式版还会验 Ed25519 与 Authenticode。</small>
                  </div>
                  <button
                    className="btn ghost small"
                    type="button"
                    disabled={busy}
                    onClick={async () => {
                      setBusy(true);
                      try {
                        const r = await api.checkUpdate();
                        if (r.error) {
                          onToast("error", r.error);
                        } else if (r.updateAvailable) {
                          onToast("success", `发现新版本 ${r.latest}`);
                          if (r.url) await api.openURL(r.url);
                        } else {
                          onToast("info", `已是最新（${r.current}）`);
                        }
                      } catch (err) {
                        onToast("error", errorText(err));
                      } finally {
                        setBusy(false);
                      }
                    }}
                  >
                    立即检查
                  </button>
                </div>
              </div>
              <div className="settings-note">
                <Icon name="shield" size={16} />
                <p>
                  Beta 未配置签名公钥时跳过清单签名校验。发行版安装包需代码签名；更新失败会保留旧版本可回滚。
                </p>
              </div>
            </>
          )}
        </div>
      </div>
    </Modal>
  );
}
