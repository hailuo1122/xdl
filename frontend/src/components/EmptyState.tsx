import { useState } from "react";
import { Icon } from "./Icon";

interface EmptyStateProps {
  integrationReady: boolean;
  extensionLive: boolean;
  onNew: () => void;
  onPaste: () => void;
  onQuickAdd: (url: string) => Promise<void> | void;
}

export function EmptyState({ integrationReady, extensionLive, onNew, onPaste, onQuickAdd }: EmptyStateProps) {
  const [url, setUrl] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const value = url.trim();
    if (!value) return;
    if (!/^https?:\/\//i.test(value)) {
      setError("只接受 http 或 https 链接");
      return;
    }
    setError("");
    setBusy(true);
    await onQuickAdd(value);
    setBusy(false);
    setUrl("");
  };

  return (
    <div className="empty-state">
      <div className="empty-intro">
        <h2>把链接放在这里</h2>
        <p className="empty-lead">
          XDL 会先探测服务器是否支持分段，再选择连接数与切分方式。下载中途断开也会从已有进度继续。
        </p>

        <form className="quick-add" onSubmit={submit}>
          <Icon name="link" size={16} />
          <input
            className="xdl-field bare"
            value={url}
            onChange={(e) => {
              setUrl(e.target.value);
              setError("");
            }}
            placeholder="https://example.com/file.zip"
            spellCheck={false}
            aria-label="下载链接"
          />
          <button className="btn" type="submit" disabled={busy || !url.trim()}>
            {busy ? <span className="spinner" /> : "开始下载"}
          </button>
        </form>
        {error && <p className="quick-error">{error}</p>}

        <div className="empty-actions">
          <button className="text-btn" onClick={onPaste}>
            <Icon name="paste" size={14} />从剪贴板粘贴
          </button>
          <button className="text-btn" onClick={onNew}>
            更多选项
          </button>
          <span className="empty-browser">
            <i className={`dot ${extensionLive ? "live" : integrationReady ? "ready" : "idle"}`} />
            {extensionLive ? "浏览器已连接，点击下载会自动进入这里" : integrationReady ? "浏览器组件已安装，扩展未连接" : "浏览器接管尚未安装"}
          </span>
        </div>
      </div>

      <aside className="empty-aside" aria-label="分段下载说明">
        <div className="empty-meter" role="img" aria-label="文件被分成多个下载分段">
          {Array.from({ length: 14 }, (_, i) => (
            <i
              key={i}
              className={i < 5 ? "done" : i === 5 ? "live" : ""}
              style={{ flexGrow: i === 9 ? 2.2 : 1 }}
            />
          ))}
        </div>
        <p>
          文件被切成若干段并行拉取。当某条连接变慢，剩余部分会被重新切给空闲连接。
        </p>
        <dl className="empty-facts">
          <div>
            <dt>默认连接</dt>
            <dd>8</dd>
          </div>
          <div>
            <dt>Turbo 上限</dt>
            <dd>32</dd>
          </div>
          <div>
            <dt>恢复粒度</dt>
            <dd>1 MiB</dd>
          </div>
        </dl>
      </aside>
    </div>
  );
}
