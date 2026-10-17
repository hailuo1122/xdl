# XDL

Windows 高速下载器（公开 Beta 候选，M5）。Go + Wails v3 beta.30 + React；浏览器扩展 WXT + React MV3。

**当前版本：`0.0.5-m5`** · 许可证：[MIT](./LICENSE) · 第三方声明：[THIRD_PARTY_NOTICES.md](./THIRD_PARTY_NOTICES.md)

## 已交付能力

| 里程碑 | 内容 |
|---|---|
| M1–M2 | HTTP 加速引擎、桌面壳、Chrome/Edge 浏览器接管 |
| M3 | HLS / DASH VOD、AES-128、mp4ff / 可选 ffmpeg |
| M4 | BT / 磁力（anacrolix）、文件选择、做种开关 |
| M5 | 批量导入、网站抓取、自动分类、计划暂停、更新检查、NSIS 骨架 |

### M5 要点

- **批量**：新建对话框多行粘贴 / 导入 txt；URL 规范化、去跟踪参数、去重后入队。
- **网站抓取**：单页或同源深度 ≤2；遵守 robots.txt；限速；预览勾选再入队。
- **自动分类**：按扩展名写入 Videos / Archives 等子目录（可关）。
- **计划暂停**：按时段 PauseAll + hold，离开时段恢复。
- **扩展**：右键「扫描本页可下载链接」批量提交。
- **发布骨架**：`build/nsis/xdl.nsi`、`internal/updater`（HTTPS 清单 + SHA-256）、`THIRD_PARTY_NOTICES.md`。

商店提交、代码签名证书、更新清单托管仍需用户侧账号与证书（见计划 §9）。

## 构建

前置：Go 1.27+、Node 24+、pnpm、WebView2（Win10/11 通常自带）。

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build-alpha.ps1
```

产物默认在 `%LOCALAPPDATA%\XDL-dev\`（`xdl.exe`、`extension\`）。

> `D:\xdy` 在开发机上可能被标记为 Low integrity，请从 `%LOCALAPPDATA%\XDL-dev` 运行。

安装/刷新原生宿主：

```powershell
& "$env:LOCALAPPDATA\XDL-dev\xdl.exe" --install-native-host
```

可选 NSIS（需已安装 NSIS，且先跑过 build-alpha）：

```powershell
makensis /DVERSION=0.0.5-m5 /DOUTDIR="$env:LOCALAPPDATA\XDL-dev" build\nsis\xdl.nsi
```

## 运行

```powershell
& "$env:LOCALAPPDATA\XDL-dev\testserver.exe" -addr 127.0.0.1:18080
& "$env:LOCALAPPDATA\XDL-dev\xdl.exe"
```

扩展目录：`%APPDATA%\XDL\extension`。开发扩展 ID：`jkmmncllhpjaklglnieoaacgchndkgkd`。

## 安全边界

- 无本地 HTTP 端口；Native Messaging → 命名管道（当前用户 SDDL）。
- 扩展不能指定任意落盘目录；自动分类仅在默认下载目录下建子目录。
- 抓取遵守 robots.txt，默认同源限速；不做 DRM / 登录墙绕过。
- BT 无搜索/索引；默认完成后停止做种。

## 已知限制

- 未签名 Beta：SmartScreen/杀软可能误报。
- 自动更新需自行托管 HTTPS 清单；发行前配置 Ed25519 公钥与代码签名。
- 官方 Chrome 稳定版自动化加载未打包扩展受限（见 `e2e/`）。
