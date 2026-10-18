<div align="center">

# XDL

**Windows 上的一块下载仪表面板。**

多连接 HTTP · HLS / DASH · BT / 磁力 · 浏览器接管

![Go](https://img.shields.io/badge/Go-1.27-00ADD8?logo=go&logoColor=white)
![Wails](https://img.shields.io/badge/Wails-v3%20beta.30-DF0000)
![Windows](https://img.shields.io/badge/Windows-10%20%2F%2011-0078D4)
![License](https://img.shields.io/badge/license-MIT-2ea44f)

`0.0.5-m5` · 公开 Beta · [MIT](./LICENSE) · [第三方声明](./THIRD_PARTY_NOTICES.md)

[![Download](https://img.shields.io/badge/%E4%B8%8B%E8%BD%BD-setup.exe%20%C2%B7%2024%20MB-1A1F1D)](https://github.com/hailuo1122/xdl/releases/latest)

</div>

> 下载本来很简单：给个地址，文件落到硬盘。
> 后来有了 302、Range、AES-128、DASH 清单、磁力链接，以及浏览器里那条永远停在 99% 的下载条。
> XDL 是拿来对付后面这些的。

**目录**：[能干什么](#它能干什么) · [跑起来](#三十秒跑起来) · [怎么工作](#它是怎么工作的) · [安装器](#安装器为什么长这样) · [冷知识](#只有读代码才会知道的事) · [不适合谁](#适合谁--不适合谁) · [FAQ](#faq)

---

## 它能干什么

| 你遇到的事 | XDL 的反应 |
| --- | --- |
| 8 GB 的 ISO，单线程 300 KB/s | 默认拆 8 条连接（1–32 可调），块 4–64 MiB；哪条连接摸鱼，剩下的块会被分走 |
| 下到 97% 蓝屏 | 崩溃恢复回退 1 MiB 重读，不从头再来 |
| 同一个文件有五个镜像 | 每个先跑 256 KiB 赛跑，按 EWMA 速度排位，最多 8 个源 |
| 课程站在 HLS 里塞了 AES-128 | 取 key、按清单或媒体序号推 IV、解完再重封装，能不转码就不转码 |
| 想下 BT 但只想要其中三个文件 | 文件选择；默认下完停止做种 |
| 网页里 40 个链接想一起下 | 扩展右键「扫描本页可下载链接」，预览勾选再入队 |
| 一列 URL 粘在 txt 里 | 多行粘贴 / 导入，规范化、去跟踪参数、去重 |
| 半夜不想让它抢带宽 | 按时段自动暂停 / 恢复 |
| 不想看 NSIS 的「下一步」 | 800×520 一块面板，点一下「安装」 |

**里程碑**：M1–M2 HTTP 引擎与浏览器接管 → M3 HLS / DASH → M4 BT / 磁力 → M5 批量、抓取、自动分类、计划暂停、自定义安装包。

### 协议侧的一些实话

- **多镜像**：同一文件的多个 http(s) 地址先探测再按速度选源；aria2 `addUri` 里主地址之后的 URI 也当镜像。
- **Metalink**：`.meta4` / `.metalink` 展开成文件任务，带 sha-256 和镜像列表；同一文件同时有 http(s) 与 ftp 时走多连接 HTTP。
- **FTP**：单连接，支持 REST 续传，不进入多连接分块。SFTP 没接——没有远程目录场景，认证和主机密钥要单独定，不塞进下载引擎。
- **代理**：默认读环境变量（`HTTP_PROXY` 等）；任务可单独填 `http(s)://`、`socks5://` 或 `direct`。FTP 只吃 SOCKS5 或直连；BT / 磁力不走这条代理。

## 三十秒跑起来

不想编译？直接下 [最新安装包](https://github.com/hailuo1122/xdl/releases/latest)（24 MB，per-user，不需要管理员）。

前置：Go 1.27+、Node 24+、pnpm、WebView2（Win10/11 通常自带）。

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build-alpha.ps1
& "$env:LOCALAPPDATA\XDL-dev\xdl.exe"
```

产物默认在 `%LOCALAPPDATA%\XDL-dev\`（`xdl.exe`、`extension\`）。

注册 / 刷新浏览器原生宿主：

```powershell
& "$env:LOCALAPPDATA\XDL-dev\xdl.exe" --install-native-host
```

打安装包（先跑过 build-alpha）：

```powershell
powershell -ExecutionPolicy Bypass -File scripts\pack-setup.ps1
# build\XDL-0.0.5-m5-setup.exe
```

> `D:\xdy` 在开发机上被标成了 Low integrity，请从 `%LOCALAPPDATA%\XDL-dev` 运行。这是机器的毛病，不是程序的。

## 它是怎么工作的

```
Chrome / Edge
  └─ XDL 扩展（MV3，WXT + React）
        │  Native Messaging：只递 URL、referer、cookie 上下文
        ▼
   native host ──命名管道（当前用户 SDDL）──►  Go 引擎
                                                 ├─ 分块调度器（1–32 连接）
                                                 ├─ HTTP / FTP / 多镜像
                                                 ├─ HLS / DASH / remux
                                                 ├─ BT / 磁力（anacrolix）
                                                 ├─ bbolt 任务快照
                                                 └─ Wails v3 ──► React 桌面端
```

几条设计上的取舍，顺手交代：

- **默认 8 条连接，不是 64。** 源站限速、丢包、磁盘写入，三样里总有一个先到瓶颈；再多只是把同一根水管切成更细的段。8 是甜点，1–32 随你调。
- **块 4–64 MiB。** 太小调度开销大，太大尾部拖沓。剩下不足 2 MiB、预计 3 秒内能跑完的慢块会被拆给别的连接——内部叫 steal，听起来像抢银行，其实只是抢进度。
- **镜像不是轮询。** 每个源先跑 256 KiB，EWMA 记速度，连续失败就降权。选源这件事，测出来的比配置里的可信。
- **扩展不碰磁盘。** 它只能把 URL 和上下文递进来；落盘路径由 Go 决定，自动分类也只会在默认下载目录下建子目录。
- **没有本地 HTTP 端口。** 桌面端和扩展通过命名管道说话，管道 ACL 是当前用户 SDDL。少一个端口，少一类事故。

## 安装器为什么长这样

XDL 的安装窗口不是 NSIS，是 `cmd/setup` 里一个 800×520 的 WebView2 页面：React + Motion，打成单文件 HTML，`go:embed` 进 setup.exe。

它被做成了一块仪表面板，而不是四步向导：

- 上半是铭牌（XDL、版本）、一句说明、安装路径；下半是一条横贯整幅的暗槽，八条轨道对应八条连接。
- 待命时轨道慢慢巡检；安装时八条不同步地涨；完成时一起封口，铜色箭头轻轻磕一下。没有烟花，那个很俗，而且我们也没做粒子系统。
- **百分比不是后端直接给的。** Go 的 `ratio` 是阶段进度：`files` 是真实字节比，`host` / `shortcuts` / `registry` 都返回 1。前端把它们折算成 0.86 / 0.90 / 0.94 / 0.97，否则刚写完文件就会显示 100%。
- 单文件 632 KB，字体是 IBM Plex 的 latin 子集（base64 内联）。中文没内嵌——完整中文字库 8–12 MB，为了一个安装器不值得；Windows 上用等线，比雅黑更像仪表。

色板、字体、两个线框和被否掉的方案都在 [`cmd/setup/ui/DESIGN.md`](./cmd/setup/ui/DESIGN.md)。安装到当前用户目录，不需要管理员；卸载会删程序、浏览器组件和开始菜单入口，下载记录保留。

## 只有读代码才会知道的事

1. **慢块真的会被偷。** `StealMinRemain = 2 MiB`、`StealMinETA = 3s`，两个条件同时满足才动手。
2. **崩溃恢复回退 1 MiB。** 不相信「最后写到的位置」，宁可重读一点。
3. **镜像赛跑只跑 256 KiB。** 再多是浪费，再少测不准；探测超时 8 秒，最多 8 个源。
4. **HLS 的 IV 有两个来源。** 清单里写了就用清单的，没写就按媒体序号推——很多播放器在这里翻车。
5. **Metalink 的 sha-256 会被真的用上。** 不是解析完就丢。
6. **`speedlab/` 是认真的。** 里面有带宽受限的 Range 服务器、分阶段隔离（纯网络 / 加缓冲 / 加磁盘）、以及引擎 vs 手写实现的对照。它的 README 第一句就是「磁盘基准噪声极大，单次结果不可信」。
7. **桌面端有一张 ChunkMap。** 你能看见每个块在谁手里、跑到哪。
8. **安装器的八条轨道不是装饰。** 默认连接数就是 8。设计稿里第一版是竖着的，被否了——太像音频电平表。

## 适合谁 / 不适合谁

**适合**

- 经常从镜像站、课程站、直链网盘拖大文件的人。
- 手里有一列 URL 要搬，不想一个个点的人。
- 想看着八条连接一起跑，并且觉得这很解压的人。
- 愿意接受 Beta 软件偶尔抽风、并且会看日志的人。

**不适合**

- 想找资源的人。它不提供索引、搜索、种子站。
- 想绕会员 / DRM / 登录墙的人。它不做，也不会做。
- 想要 200 KB 绿色版的人。Go + WebView2，物理上做不到。
- 觉得下载器应该有 30 个按钮和 4 个悬浮球的人。

## 它不做什么

- 不提供资源、搜索、索引。
- 不绕 DRM、登录墙、付费墙。
- 不保证满速。你家宽带、源站限速、磁盘写入，总有一个先到；XDL 只保证自己这一侧不拖后腿。
- 不做「会员加速」。
- 不帮你整理硬盘。自动分类只是按扩展名丢进 Videos / Archives 几个文件夹，别指望它懂你。

## 安全边界

- 无本地 HTTP 端口；Native Messaging → 命名管道（当前用户 SDDL）。
- 扩展不能指定任意落盘目录；自动分类仅在默认下载目录下建子目录。
- 抓取遵守 robots.txt，默认同源、限速；不做 DRM / 登录墙绕过。
- BT 无搜索 / 索引；默认完成后停止做种。

## 已知限制

- **未签名 Beta。** SmartScreen / 杀软可能误报。签名、清单托管和商店提交不在引擎范围内。
- 自动更新检查已经能拉 HTTPS 清单；清单放哪、用哪张证书，由发行配置决定。
- 官方 Chrome 稳定版不允许命令行加载未打包扩展，浏览器接管的自动化测试用 Edge 或 Chrome for Testing（见 `e2e/`）。
- 接口和 UI 还会动，任务数据格式尽量不动。

## 开发

```
internal/engine       传输、分块、镜像、FTP、队列、校验、预分配
internal/media        HLS / DASH / ffmpeg / remux
internal/bittorrent   BT / 磁力
internal/nativehost   原生宿主
internal/ipc          命名管道
internal/app          Wails 绑定、窗口、内嵌前端
frontend/             React 桌面端（含 ChunkMap）
extension/            WXT + React MV3 扩展
cmd/setup/            安装器（Go + React 仪表面板）
speedlab/             吞吐基准：真实引擎，loopback，分阶段隔离
e2e/                  浏览器接管端到端
```

```powershell
go test ./...                    # 引擎 / 解析 / 策略
cd e2e; pnpm install; node takeover.test.mjs
```

## FAQ

**为什么 Windows 报毒？**
未签名 Beta。SmartScreen 对「没见过的新 exe」一律先怀疑。签名要证书，证书要钱和主体审核，目前都没有。不放心就自己 `go build`。

**能跑满千兆吗？**
取决于源站、你的宽带、磁盘和目标服务器的良心。XDL 只负责不在自己这一侧拖后腿。

**为什么叫 XDL？**
X 是变量，DL 是 download。你下什么，X 就是什么。比 `SuperUltraDownloaderPro` 低调一点。

**为什么安装包 24 MB？**
Go 运行时、WebView2 页面、内联字体，以及真正的程序都在里面。没有一个是来凑数的。

**有 macOS / Linux 吗？**
没有。浏览器接管、命名管道、开始菜单、WebView2 都是 Windows 的活。引擎是跨平台的，壳不是。

**能下 BT 吗？有搜索吗？**
能。没有搜索，也不打算做——那是另一个产品，以及另一堆法律问题。

**为什么安装器不是「下一步」向导？**
安装只有一件事要决定：装哪。把它做成四步不会更安全，只会让你多点三次。

## Credits

站在这些肩膀上：[anacrolix/torrent](https://github.com/anacrolix/torrent)、[Eyevinn/mp4ff](https://github.com/Eyevinn/mp4ff)、[grafov/m3u8](https://github.com/grafov/m3u8)、[jlaffaye/ftp](https://github.com/jlaffaye/ftp)、[temoto/robotstxt](https://github.com/temoto/robotstxt)、[Microsoft/go-winio](https://github.com/microsoft/go-winio)、[bbolt](https://github.com/etcd-io/bbolt)、[Wails](https://wails.io)、[WXT](https://wxt.dev)、[IBM Plex](https://www.ibm.com/plex/)。

完整清单见 [`THIRD_PARTY_NOTICES.md`](./THIRD_PARTY_NOTICES.md)。

## License

[MIT](./LICENSE)。拿去用，出了事别找我。
