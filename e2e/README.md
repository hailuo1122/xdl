# XDL 浏览器接管 E2E

前置条件：

1. 场景服务器已启动：`%LOCALAPPDATA%\XDL-dev\testserver.exe -addr 127.0.0.1:18080`
2. 扩展已构建：`cd extension && pnpm build`
3. 桌面端已构建、已运行，并已注册原生宿主：
   `xdl.exe --install-native-host`
4. 机器上安装了 Microsoft Edge；Chrome 用同版本 Chrome for Testing（官方 Chrome 稳定版禁用了命令行加载未打包扩展）。

运行：

```powershell
cd e2e
pnpm install
node takeover.test.mjs
```

默认无头 Edge。Chrome：

```powershell
npx --yes @puppeteer/browsers install chrome@stable --path "$env:LOCALAPPDATA\XDL-dev\browsers"
$env:XDL_E2E_BROWSER="chrome"
node takeover.test.mjs
```

回退场景（可选）：

```powershell
$env:XDL_E2E_FALLBACK="1"
$env:XDL_DATA_DIR="C:\Users\<you>\AppData\Local\XDL-dev\data-alpha"
$env:XDL_APP_EXE="$env:LOCALAPPDATA\XDL-dev\xdl.exe"
node takeover.test.mjs
```

脚本会在系统临时目录创建独立浏览器 profile / 下载目录，不修改日常浏览器配置；回退场景结束后会恢复宿主文件与 `app-path.txt` 并重启应用。证据写入 `%LOCALAPPDATA%\XDL-dev\evidence`。
