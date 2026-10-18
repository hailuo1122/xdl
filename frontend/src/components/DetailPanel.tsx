import { useState } from "react";
import { api, errorText } from "../api";
import {
  bytes,
  displayName,
  eta,
  formatTime,
  hostOf,
  pctLabel,
  speed,
  stateLabel,
} from "../format";
import type { TaskDetail, TaskView } from "../types";
import { ChunkMap } from "./ChunkMap";
import { Icon } from "./Icon";
import { Select } from "./Select";

interface DetailPanelProps {
  detail: TaskDetail | null;
  loading: boolean;
  onClose: () => void;
  onPause: (t: TaskView) => void;
  onResume: (t: TaskView) => void;
  onOpen: (t: TaskView) => void;
  onReveal: (t: TaskView) => void;
  onRemove: (t: TaskView) => void;
  onToast: (kind: "info" | "success" | "error", text: string) => void;
}

async function copyText(text: string, onToast: DetailPanelProps["onToast"]) {
  try {
    await navigator.clipboard.writeText(text);
    onToast("success", "已复制到剪贴板");
  } catch {
    onToast("error", "复制失败，请手动选择文本");
  }
}

export function DetailPanel({
  detail,
  loading,
  onClose,
  onPause,
  onResume,
  onOpen,
  onReveal,
  onRemove,
  onToast,
}: DetailPanelProps) {
  const [removeArmed, setRemoveArmed] = useState(false);
  if (!detail) {
    return (
      <aside className="detail-panel">
        <div className="detail-head">
          <span className="detail-title">传输详情</span>
          <button className="icon-btn" onClick={onClose} aria-label="关闭详情">
            <Icon name="x" size={17} />
          </button>
        </div>
        <div className="detail-loading">{loading ? "正在读取任务…" : "任务已移除"}</div>
      </aside>
    );
  }
  const t = detail.task;
  const active = t.state === "running" || t.state === "resolving" || t.state === "queued";
  const canResume = t.state === "paused" || t.state === "failed";
  const progressPct = t.size ? Math.min(100, (t.done / t.size) * 100) : 0;

  return (
    <aside className="detail-panel">
      <div className="detail-head">
        <span className="detail-title">传输详情</span>
        <button className="icon-btn" onClick={onClose} aria-label="关闭详情">
          <Icon name="x" size={17} />
        </button>
      </div>

      <div className="detail-scroll">
        <div className="detail-file">
          <div className={`file-badge ${t.state}`}>
            <Icon name="file" size={22} />
          </div>
          <div className="detail-file-copy">
            <h3 title={displayName(t)}>{displayName(t)}</h3>
            <p>
              <span className={`state-chip ${t.state}`}>{stateLabel(t.state)}</span>
              <span className="dot-sep" aria-hidden="true" />
              {hostOf(t.finalUrl || t.url)}
            </p>
          </div>
        </div>

        <div className="detail-progress">
          <div className="detail-progress-head">
            <span className="big-pct">{t.state === "done" ? "100%" : pctLabel(t)}</span>
            <span className="detail-progress-size">
              {bytes(t.done)} / {bytes(t.size)}
            </span>
          </div>
          <div className={`detail-bar state-${t.state}`}>
            <span style={{ width: `${progressPct}%` }} />
          </div>
          <div className="detail-progress-foot">
            <span>{speed(t.speed)}</span>
            <span>{active ? `剩余 ${eta(t)}` : t.state === "done" ? formatTime(t.updatedAt) : stateLabel(t.state)}</span>
          </div>
        </div>

        {t.kind !== "torrent" && <ChunkMap task={t} chunks={detail.chunks} />}

        <div className="facts">
          {t.kind === "torrent" ? (
            <>
              <div className="fact">
                <span>Peers</span>
                <b>{t.state === "done" ? "—" : t.peers ?? 0}</b>
              </div>
              <div className="fact">
                <span>Seeds</span>
                <b>{t.state === "done" ? "—" : t.seeds ?? 0}</b>
              </div>
              <div className="fact">
                <span>分享率</span>
                <b>{(t.ratio ?? 0).toFixed(2)}</b>
              </div>
              <div className="fact">
                <span>已上传</span>
                <b>{bytes(t.uploaded ?? 0)}</b>
              </div>
            </>
          ) : (
            <>
              <div className="fact">
                <span>连接</span>
                <b>{t.state === "done" ? "—" : `${detail.connections || 0} / ${t.conns}`}</b>
              </div>
              <div className="fact">
                <span>协议</span>
                <b>{t.validator.acceptRanges ? "HTTP / Range" : "HTTP / 单流"}</b>
              </div>
            </>
          )}
          <div className="fact">
            <span>类型</span>
            <b>{t.kind.toUpperCase()}</b>
          </div>
          <div className="fact">
            <span>创建</span>
            <b>{formatTime(t.createdAt)}</b>
          </div>
        </div>

        {t.err && (
          <div className="detail-error">
            <div className="detail-error-head">
              <Icon name="failed" size={15} />
              <strong>{t.err.message}</strong>
            </div>
            {t.err.hint && <p>{t.err.hint}</p>}
            <div className="detail-error-actions">
              <code>{t.err.code}</code>
              <button
                className="text-btn small"
                onClick={() =>
                  copyText(
                    `${t.err?.code}: ${t.err?.message}\n${t.err?.hint || ""}\n${t.finalUrl || t.url}`,
                    onToast,
                  )
                }
              >
                复制诊断
              </button>
            </div>
          </div>
        )}

        <div className="detail-section">
          <div className="section-label">来源</div>
          <div className="url-row">
            <span className="url" title={t.finalUrl || t.url}>
              {t.finalUrl || t.url}
            </span>
            <button className="icon-btn" title="复制链接" onClick={() => copyText(t.finalUrl || t.url, onToast)}>
              <Icon name="copy" size={14} />
            </button>
          </div>
        </div>

        <div className="detail-section">
          <div className="section-label">保存位置</div>
          <div className="url-row">
            <span className="url" title={t.finalPath || t.dir}>
              {t.finalPath || t.dir || "—"}
            </span>
            <button className="icon-btn" title="打开所在文件夹" onClick={() => onReveal(t)}>
              <Icon name="folder" size={14} />
            </button>
          </div>
        </div>

        {t.kind !== "torrent" && (
          <div className="detail-section">
            <div className="section-label">服务器校验</div>
            <div className="validator-line">
              {t.validator.etag ? (
                <>
                  <span className="ok-dot" />
                  ETag 可断点续传
                </>
              ) : t.validator.acceptRanges ? (
                <>
                  <span className="ok-dot" />
                  支持 Range 分段
                </>
              ) : (
                <>
                  <span className="warn-dot" />
                  单连接下载
                </>
              )}
            </div>
            {(t.expectedSha256 || t.sha256) && (
              <div className="validator-line" style={{ marginTop: 8 }}>
                {t.state === "verifying" ? (
                  <>
                    <span className="ok-dot" />
                    正在计算 SHA-256…
                  </>
                ) : t.sha256 && t.expectedSha256 && t.sha256 === t.expectedSha256 ? (
                  <>
                    <span className="ok-dot" />
                    SHA-256 校验通过
                  </>
                ) : t.sha256 ? (
                  <>
                    <span className="ok-dot" />
                    SHA-256 {t.sha256.slice(0, 12)}…
                  </>
                ) : (
                  <>
                    <span className="warn-dot" />
                    将校验 {t.expectedSha256?.slice(0, 12)}…
                  </>
                )}
              </div>
            )}
          </div>
        )}

        <div className="detail-section">
          <div className="section-label">调度</div>
          <div className="form-row" style={{ gap: 8, marginTop: 6 }}>
            <label className="field" style={{ flex: 1 }}>
              <span className="field-label">优先级</span>
              <Select
                aria-label="任务优先级"
                value={t.priority ?? 0}
                onChange={(v) => {
                  void api
                    .setPriority(t.id, v)
                    .then(() => onToast("success", "优先级已更新"))
                    .catch((err) => onToast("error", errorText(err)));
                }}
                options={[
                  { value: 2, label: "高" },
                  { value: 0, label: "普通" },
                  { value: -1, label: "低" },
                ]}
              />
            </label>
            {t.kind !== "torrent" && (
              <label className="field" style={{ flex: 1 }}>
                <span className="field-label">单任务限速</span>
                <Select
                  aria-label="单任务限速"
                  value={t.limitBps ?? 0}
                  onChange={(v) => {
                    void api
                      .setLimitBps(t.id, v)
                      .then(() => onToast("success", "限速已更新"))
                      .catch((err) => onToast("error", errorText(err)));
                  }}
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
          </div>
        </div>

        <div className="detail-actions">
          {active && (
            <button className="btn secondary" onClick={() => onPause(t)}>
              <Icon name="pause" size={15} />暂停
            </button>
          )}
          {canResume && (
            <button className="btn" onClick={() => onResume(t)}>
              <Icon name="play" size={14} />{t.state === "failed" ? "重试" : "继续"}
            </button>
          )}
          {t.state === "done" && (
            <button className="btn" onClick={() => onOpen(t)}>
              <Icon name="external" size={15} />打开文件
            </button>
          )}
          <button className="btn ghost" onClick={() => onReveal(t)}>
            <Icon name="folder" size={15} />所在文件夹
          </button>
          <button
            className={`btn ghost danger ${removeArmed ? "armed" : ""}`}
            onClick={() => {
              if (removeArmed) {
                onRemove(t);
              } else {
                setRemoveArmed(true);
                setTimeout(() => setRemoveArmed(false), 3000);
              }
            }}
          >
            <Icon name="trash" size={15} />
            {removeArmed ? "确认移除" : "移除任务"}
          </button>
        </div>
      </div>
    </aside>
  );
}
