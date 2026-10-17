import { percent, pctLabel } from "../format";
import type { TaskView } from "../types";

export function ProgressBar({ task, compact = false }: { task: TaskView; compact?: boolean }) {
  const p = percent(task);
  const unknown = !task.size && (task.state === "running" || task.state === "resolving");
  return (
    <div className={`progress-wrap ${compact ? "compact" : ""}`}>
      <div className={`pbar state-${task.state} ${unknown ? "unknown" : ""}`}>
        {unknown ? (
          <span className="indeterminate" />
        ) : (
          <span className="pfill" style={{ width: `${p}%` }} />
        )}
        <span className="pticks" aria-hidden="true" />
      </div>
      {!compact && <span className="pct">{unknown ? "解析中" : pctLabel(task)}</span>}
    </div>
  );
}
