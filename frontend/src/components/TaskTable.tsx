import { displayName, pctLabel, speed, stateLabel } from "../format";
import type { TaskView } from "../types";
import { Icon } from "./Icon";
import { ProgressBar } from "./ProgressBar";

interface TaskTableProps {
  tasks: TaskView[];
  selected: string | null;
  onSelect: (id: string) => void;
  onPause: (t: TaskView) => void;
  onResume: (t: TaskView) => void;
  onOpen: (t: TaskView) => void;
  onReveal: (t: TaskView) => void;
  onCopy: (t: TaskView) => void;
  onRemove: (t: TaskView) => void;
  confirmId: string | null;
  onCancelRemove: (t: TaskView) => void;
  onConfirmRemove: (t: TaskView) => void;
  onNew: () => void;
}

const PAUSABLE = new Set(["queued", "resolving", "running", "verifying", "waiting_user"]);
const LIVE = new Set(["running", "resolving"]);

export function TaskTable({
  tasks,
  selected,
  onSelect,
  onPause,
  onResume,
  onOpen,
  onReveal,
  onCopy,
  onRemove,
  confirmId,
  onCancelRemove,
  onConfirmRemove,
  onNew,
}: TaskTableProps) {
  if (tasks.length === 0) {
    return (
      <div className="list-empty">
        <p>这里还没有任务。</p>
        <button className="text-btn" onClick={onNew}>
          新建一个下载
        </button>
      </div>
    );
  }

  return (
    <div className="task-list" role="list">
      {tasks.map((t) => {
        const canPause = PAUSABLE.has(t.state);
        const canResume = t.state === "paused" || t.state === "failed";
        const live = LIVE.has(t.state);

        if (t.id === confirmId) {
          const running = live || t.state === "verifying";
          return (
            <article
              key={t.id}
              role="listitem"
              className="task-card confirming"
              onKeyDown={(e) => {
                if (e.key === "Escape") {
                  e.stopPropagation();
                  onCancelRemove(t);
                }
              }}
            >
              <div className="confirm-copy">
                <p className="confirm-title" title={displayName(t)}>
                  移除「{displayName(t)}」？
                </p>
                <p className="confirm-note">
                  {running
                    ? "会停止正在进行的下载，已经写入的文件不会删除。"
                    : "只从任务列表移除，已经下载的文件不会删除。"}
                </p>
              </div>
              <div className="confirm-actions">
                <button className="btn ghost compact" onClick={() => onCancelRemove(t)} autoFocus>
                  取消
                </button>
                <button className="btn danger compact" onClick={() => onConfirmRemove(t)}>
                  移除
                </button>
              </div>
            </article>
          );
        }

        return (
          <article
            key={t.id}
            role="listitem"
            tabIndex={0}
            className={`task-card state-${t.state} ${live ? "live" : ""} ${selected === t.id ? "selected" : ""}`}
            onClick={() => onSelect(t.id)}
            onKeyDown={(e) => {
              if (e.key === "Enter") onSelect(t.id);
              if (e.key === "Delete") {
                e.preventDefault();
                onRemove(t);
              }
            }}
            onDoubleClick={() => t.state === "done" && onOpen(t)}
          >
            <div className="card-top">
              <h3 title={displayName(t)}>{displayName(t)}</h3>
              <div className="card-actions" onClick={(e) => e.stopPropagation()}>
                {canPause && (
                  <button className="card-action" title="暂停" onClick={() => onPause(t)}>
                    <Icon name="pause" size={14} />
                  </button>
                )}
                {canResume && (
                  <button className="card-action" title={t.state === "failed" ? "重试" : "继续"} onClick={() => onResume(t)}>
                    <Icon name="play" size={13} />
                  </button>
                )}
                {t.state === "done" && (
                  <button className="card-action" title="打开文件" onClick={() => onOpen(t)}>
                    <Icon name="external" size={14} />
                  </button>
                )}
                <button
                  id={`remove-${t.id}`}
                  className="card-action"
                  title="移除任务"
                  aria-label="移除任务"
                  onClick={() => onRemove(t)}
                >
                  <Icon name="x" size={14} />
                </button>
                <button className="card-action" title="打开所在文件夹" onClick={() => onReveal(t)}>
                  <Icon name="folder" size={14} />
                </button>
                <button className="card-action" title="复制链接" onClick={() => onCopy(t)}>
                  <Icon name="link" size={14} />
                </button>
                <button
                  className={`card-action ${selected === t.id ? "pressed" : ""}`}
                  title="任务详情"
                  onClick={() => onSelect(t.id)}
                >
                  <Icon name="info" size={14} />
                </button>
              </div>
            </div>

            {(live || t.state === "failed") && (
              <div className="card-meta">
                {live ? (
                  <span className="meta-speed">{speed(t.speed)} · {pctLabel(t)}</span>
                ) : (
                  <span className="meta-failed">{t.err?.message || stateLabel(t.state)}</span>
                )}
              </div>
            )}

            <div className="card-progress">
              <ProgressBar task={t} compact />
            </div>
          </article>
        );
      })}
    </div>
  );
}
