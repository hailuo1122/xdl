import type { Filter } from "../types";
import { Icon } from "./Icon";

interface SidebarProps {
  filter: Filter;
  integrationReady: boolean;
  extensionLive: boolean;
  onFilter: (f: Filter) => void;
  onNew: () => void;
  onSettings: () => void;
  onBrowser: () => void;
  onHelp: () => void;
}

const nav: { key: Filter; label: string; icon: string }[] = [
  { key: "downloading", label: "下载中", icon: "play" },
  { key: "waiting", label: "等待中", icon: "pause" },
  { key: "stopped", label: "已停止", icon: "stop" },
  { key: "done", label: "已完成", icon: "done" },
];

export function Sidebar({
  filter,
  integrationReady,
  extensionLive,
  onFilter,
  onNew,
  onSettings,
  onBrowser,
  onHelp,
}: SidebarProps) {
  return (
    <>
      <aside className="rail">
        <div className="rail-brand" title="XDL">
          <svg viewBox="0 0 28 28" width="22" height="22" aria-hidden="true">
            <path
              d="M6 8.5h7.2L16.5 14 13.2 19.5H6L9.3 14 6 8.5Zm9.2 0H22L18.7 14 22 19.5h-6.8L18.5 14 15.2 8.5Z"
              fill="currentColor"
            />
          </svg>
        </div>

        <nav className="rail-nav" aria-label="主导航">
          <button
            className="rail-btn active"
            onClick={() => onFilter("downloading")}
            title="任务列表"
            aria-label="任务列表"
          >
            <Icon name="tasks" size={18} />
          </button>
          <button className="rail-btn rail-new" onClick={onNew} title="新建下载 (Ctrl+N)" aria-label="新建下载">
            <Icon name="plus" size={20} />
          </button>
          <button className="rail-btn" onClick={onBrowser} title="浏览器接管" aria-label="浏览器接管">
            <Icon name="browser" size={17} />
            <i className={`rail-dot ${extensionLive ? "live" : integrationReady ? "ready" : "idle"}`} />
          </button>
        </nav>

        <div className="rail-spacer" />

        <button className="rail-btn" onClick={onSettings} title="设置 (Ctrl+,)" aria-label="设置">
          <Icon name="sliders" size={17} />
        </button>
        <button className="rail-btn" onClick={onHelp} title="帮助" aria-label="帮助">
          <Icon name="help" size={17} />
        </button>
      </aside>

      <aside className="filters">
        <div className="filters-head">
          <h2>任务列表</h2>
        </div>

        <nav className="filter-nav" aria-label="任务筛选">
          {nav.map((item) => (
            <button
              key={item.key}
              className={`filter-item ${filter === item.key ? "active" : ""}`}
              onClick={() => onFilter(item.key)}
            >
              <Icon name={item.icon} size={14} />
              <span>{item.label}</span>
            </button>
          ))}
        </nav>
      </aside>
    </>
  );
}
