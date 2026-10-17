import { bytes } from "../format";
import type { Chunk, TaskView } from "../types";

const stateText: Record<string, string> = {
  pending: "等待",
  running: "下载中",
  done: "完成",
  failed: "失败",
  stolen: "已切分",
};

export function ChunkMap({ task, chunks }: { task: TaskView; chunks: Chunk[] }) {
  const hasChunks = chunks.length > 0;
  const shown = chunks.slice(0, 400);
  const fallback = task.size
    ? Math.min(28, Math.max(8, Math.round(task.size / (16 * 1024 * 1024))))
    : 20;
  const pct = task.size ? Math.min(1, task.done / task.size) : 0;

  return (
    <div className="chunkmap">
      <div className="chunkmap-head">
        <span>分片</span>
        <span className="chunkmap-meta">
          {hasChunks ? `${chunks.length} 段` : task.state === "done" ? "已完成" : "等待探测"}
        </span>
      </div>
      <div className="chunk-grid" aria-hidden="true">
        {hasChunks
          ? shown.map((c) => {
              const len = Math.max(1, c.end - c.start + 1);
              const local = Math.min(1, c.done / len);
              return (
                <span
                  key={c.index}
                  className={`chunk ${c.state} owner-${Math.abs(c.owner) % 6}`}
                  style={{ flexGrow: Math.max(1, len / 65536) }}
                  title={`#${c.index + 1} · ${bytes(c.start)}–${bytes(c.end + 1)} · ${stateText[c.state] || c.state} · ${Math.round(local * 100)}%`}
                >
                  <i style={{ width: `${local * 100}%` }} />
                </span>
              );
            })
          : Array.from({ length: fallback }, (_, i) => (
              <span key={i} className={`chunk ${i / fallback < pct ? "done" : "pending"}`} style={{ flexGrow: 1 }} />
            ))}
      </div>
      <div className="chunk-legend">
        <span><i className="lg done" />完成</span>
        <span><i className="lg running" />下载中</span>
        <span><i className="lg pending" />等待</span>
        {hasChunks && chunks.length > shown.length && <span className="more">仅显示前 {shown.length} 段</span>}
      </div>
    </div>
  );
}
