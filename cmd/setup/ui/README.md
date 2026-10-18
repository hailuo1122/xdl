# XDL Setup UI

XDL（Windows 下载器）的安装 / 卸载窗口。React 19 + TypeScript + motion + Vite，构建成**一个** HTML，由 `cmd/setup/ui_windows.go` 通过 `go:embed` 打进安装器，WebView2 用 `SetHtml` 直接加载。

设计说明见 [`DESIGN.md`](DESIGN.md)。

## 目录

```
cmd/setup/ui/
├─ index.html
├─ vite.config.ts          # vite-plugin-singlefile：JS / CSS / 字体全部内联
├─ src/
│  ├─ main.tsx
│  ├─ App.tsx              # 状态机、文案、进度归一
│  ├─ styles.css           # 全部样式与动效（800×520 画布，--u 等比）
│  ├─ bridge.ts            # Go 桥 + 浏览器预览实现
│  └─ components/
│     ├─ ChannelDeck.tsx   # 传输槽 + 读数 + 主键
│     └─ Mark.tsx          # 双箭头 chevron
└─ dist/index.html         # 构建产物（约 638KB，含字体）
```

## 开发与构建

```bash
pnpm --dir cmd/setup/ui install
pnpm --dir cmd/setup/ui dev      # 浏览器预览，自动使用 demo 桥
pnpm --dir cmd/setup/ui build    # 类型检查 + 产出 dist/index.html
go build ./cmd/setup             # dist/index.html 会被 go:embed
```

`dist/index.html` 没有外部依赖，双击就能打开。字体只内联 IBM Plex 的 latin 子集（约 100KB base64）；中文用系统字：等线 → 雅黑 → PingFang。

## Go 桥

页面挂载后立刻调用 `setupInfo()`。所有方法同步返回或返回 Promise 都兼容。

| 方法 | 约定 |
| --- | --- |
| `setupInfo()` | `{ version, dir, mode: 'install' \| 'uninstall', installed: string, hasPayload: boolean }`；`installed` 是已装版本号，空字符串表示没装 |
| `setupBrowse(initial)` | 打开选目录对话框，返回路径；取消返回 `''` |
| `setupInstall(dir)` | **resolve = 装完，reject = 失败**（`Error.message` 会显示在错误态） |
| `setupUninstall()` | 同上 |
| `setupLaunch()` | 启动已安装的 XDL |
| `setupClose()` | 关闭窗口 |
| `window.onProgress(p)` | Go 主动回调；页面挂载时把处理函数赋给 `window.onProgress` |

进度对象：

```ts
{ phase: string, ratio: number, status: string, detail: string }
```

- `status` 直接当主状态行（「正在写入程序」）；为空时用内置文案兜底。
- `detail` 显示在下面一行等宽小字（字节数、注册表键、文件名都合适）。
- 建议每 150–400ms 推一次；轨道有 540ms 缓动，太密会互相打断。
- **ratio 是阶段进度**：`files` 是真实字节比，`host` / `shortcuts` / `registry` 都是 `1`。页面会把它们折算成 0.86 / 0.90 / 0.94 / 0.97 的全局进度（见 `App.tsx` 的 `globalRatio()`），卸载阶段的 0.2 / 0.55 / 0.8 / 1 原样使用。
- 结束时请让 `setupInstall` / `setupUninstall` resolve，页面才走完成态。

## 预览参数（仅在没有原生桥时生效）

| 参数 | 作用 |
| --- | --- |
| `?state=working` | 直接进安装中 |
| `?state=done` | 完成态 |
| `?state=error` | 错误态 |
| `&ratio=0.62` | 配合 working / error 指定进度 |
| `&mode=uninstall` | 卸载模式 |
| `&installed=1` | 覆盖安装文案 + 「已安装」标签 |
| `&nopayload=1` | 安装包不完整 |
| `&instant=1` | 点安装后立刻完成 |

例：`index.html?mode=uninstall&state=working&ratio=0.45`

## 键盘与可达性

- Tab 顺序：安装位置 → 主键 → 关闭；`Enter` / `Space` 触发主键；`Esc` 关闭（安装中不响应）。
- 进度条带 `role="progressbar"` 与 `aria-valuenow`；状态变化通过隐藏的 `aria-live="polite"` 区域播报。
- 可交互元素有 2px 青绿 `:focus-visible` 描边。
- `prefers-reduced-motion: reduce` 下所有位移 / 淡入 / 巡检 / 扫光关闭，直接切状态。
