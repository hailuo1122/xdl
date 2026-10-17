import { useEffect, useState } from "react";
import { api, errorText } from "../api";
import type { IntegrationStatus, Settings } from "../types";
import { Icon } from "./Icon";
import { Switch } from "./Switch";

interface OnboardingProps {
  open: boolean;
  settings: Settings;
  onDone: (s: Settings) => void;
  onToast: (kind: "info" | "success" | "error", text: string) => void;
}

export function Onboarding({ open, settings, onDone, onToast }: OnboardingProps) {
  const [step, setStep] = useState(0);
  const [draft, setDraft] = useState(settings);
  const [integration, setIntegration] = useState<IntegrationStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [extPath, setExtPath] = useState("");

  useEffect(() => {
    if (!open) return;
    setDraft(settings);
    setStep(0);
    setError("");
    api.integration().then(setIntegration).catch(() => {});
    api.extensionPath().then(setExtPath).catch(() => {});
  }, [open, settings]);

  if (!open) return null;

  const chooseDir = async () => {
    try {
      const p = await api.chooseDirectory();
      if (p) setDraft({ ...draft, downloadDir: p });
    } catch (err) {
      setError(errorText(err));
    }
  };

  const repair = async () => {
    setBusy(true);
    try {
      const st = await api.repairIntegration();
      setIntegration(st);
      onToast("success", "浏览器组件已注册");
    } catch (err) {
      onToast("error", `注册失败：${errorText(err)}`);
    } finally {
      setBusy(false);
    }
  };

  const finish = async (skip = false) => {
    setBusy(true);
    try {
      const next: Settings = { ...draft, firstRunDone: true };
      const out = await api.saveSettings(next);
      onDone(out);
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
    if (skip) {
      // nothing else; save errors surface above
    }
  };

  const steps = [
    {
      kicker: "01 / 存储",
      title: "下载放在哪里",
      el: (
        <>
          <p className="ob-lead">XDL 会先写入临时文件，完成后再原子改名，避免留下半个文件。</p>
          <div className="ob-dir">
            <span className="ob-dir-icon"><Icon name="folder" size={19} /></span>
            <span className="ob-dir-path" title={draft.downloadDir}>{draft.downloadDir}</span>
            <button className="btn ghost small" onClick={chooseDir}>更改</button>
          </div>
          <div className="ob-facts">
            <span><b>8</b> 默认连接</span>
            <span><b>1 MiB</b> 浏览器接管阈值</span>
            <span><b>3</b> 任务并发</span>
          </div>
        </>
      ),
    },
    {
      kicker: "02 / 浏览器",
      title: "让浏览器把下载交给 XDL",
      el: (
        <>
          <p className="ob-lead">
            注册原生消息宿主后，Chrome / Edge 的下载会多一步：在 XDL 里继续，速度更快、可断点续传。
          </p>
          <div className={`ob-integration ${integration?.host.installed ? "ok" : ""}`}>
            <span className={`integration-orb ${integration?.extensionLive ? "live" : integration?.host.installed ? "ready" : "idle"}`}>
              <Icon name="browser" size={22} />
            </span>
            <div>
              <strong>
                {integration?.extensionLive
                  ? "扩展已连接"
                  : integration?.host.installed
                    ? "组件已安装"
                    : "尚未安装浏览器组件"}
              </strong>
              <p>之后可在「设置 → 浏览器接管」重新注册或查看扩展 ID。</p>
            </div>
            <button className="btn" onClick={repair} disabled={busy}>
              {busy ? <span className="spinner" /> : <Icon name="shield" size={15} />}
              {integration?.host.installed ? "重新注册" : "安装组件"}
            </button>
          </div>
          <div className="ob-code">
            <span>扩展 ID</span>
            <code>{integration?.extensionId || "…"}</code>
          </div>
          <div className="ob-code">
            <span>扩展目录</span>
            <code title={extPath}>{extPath || "尚未找到，请先运行构建脚本"}</code>
            {extPath && (
              <>
                <button
                  className="text-btn small"
                  onClick={() => {
                    navigator.clipboard.writeText(extPath).then(
                      () => onToast("success", "扩展路径已复制"),
                      () => onToast("error", "复制失败"),
                    );
                  }}
                >
                  复制
                </button>
                <button
                  className="text-btn small"
                  onClick={() => api.openExtensionFolder().catch((err) => onToast("error", errorText(err)))}
                >
                  打开
                </button>
              </>
            )}
          </div>
          <p className="ob-note">
            首次内测需要手动加载扩展：打开浏览器扩展页 → 开发者模式 → 加载已解压的扩展程序。失败时自动回退浏览器下载。
          </p>
        </>
      ),
    },
    {
      kicker: "03 / 性能",
      title: "选择默认性能模式",
      el: (
        <>
          <p className="ob-lead">之后可以在设置里随时切换。标准模式更温和，Turbo 在允许的站点上更快。</p>
          <div className="mode-grid">
            <button
              className={`mode-card ${!draft.turbo ? "selected" : ""}`}
              onClick={() => setDraft({ ...draft, turbo: false, defaultConns: 8 })}
            >
              <span className="mode-title">标准</span>
              <span className="mode-conns">8 连接</span>
              <span className="mode-desc">默认，遇到限流更少。</span>
            </button>
            <button
              className={`mode-card ${draft.turbo ? "selected" : ""}`}
              onClick={() => setDraft({ ...draft, turbo: true, defaultConns: 16 })}
            >
              <span className="mode-title">Turbo</span>
              <span className="mode-conns">16–32 连接</span>
              <span className="mode-desc">大文件更快，可能触发站点限流。</span>
            </button>
          </div>
          <div className="ob-row">
            <div>
              <span>启动时最小化到托盘</span>
              <small>开机自启后不打扰桌面。</small>
            </div>
            <Switch
              checked={draft.startMinimized}
              onChange={(v) => setDraft({ ...draft, startMinimized: v })}
            />
          </div>
          {error && <pre className="form-error">{error}</pre>}
        </>
      ),
    },
  ];

  const current = steps[step];
  return (
    <div className="onboarding">
      <div className="ob-panel">
        <div className="ob-head">
          <div className="brand ob-brand">
            <span className="brand-mark">
              <svg viewBox="0 0 32 32" width="26" height="26">
                <rect x="2" y="2" width="28" height="28" rx="7" fill="none" stroke="currentColor" strokeWidth="1.6" />
                <path d="M16 8v10M11 14l5 5 5-5" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
                <path d="M10 23h12" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
              </svg>
            </span>
            <span className="brand-copy"><strong>XDL</strong><em>首次使用</em></span>
          </div>
          <button className="text-btn small" onClick={() => finish(true)} disabled={busy}>
            跳过引导
          </button>
        </div>

        <div className="ob-body">
          <span className="ob-kicker">{current.kicker}</span>
          <h2>{current.title}</h2>
          {current.el}
        </div>

        <div className="ob-foot">
          <div className="ob-dots" aria-hidden="true">
            {steps.map((_, i) => (
              <i key={i} className={i === step ? "active" : i < step ? "done" : ""} />
            ))}
          </div>
          <div className="ob-actions">
            {step > 0 && (
              <button className="btn ghost" onClick={() => setStep(step - 1)} disabled={busy}>
                上一步
              </button>
            )}
            {step < steps.length - 1 ? (
              <button className="btn" onClick={() => setStep(step + 1)}>
                下一步
                <Icon name="chevron" size={14} />
              </button>
            ) : (
              <button className="btn" onClick={() => finish(false)} disabled={busy}>
                {busy && <span className="spinner" />}
                开始使用
              </button>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
