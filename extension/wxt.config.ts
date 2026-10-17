import { defineConfig } from "wxt";
import react from "@vitejs/plugin-react";
const publicKey =
  "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAy+F0M44423JTzcBPos64AakXqNg69qp9pbGSc8d1spH5ZkiNlfgY7BDJw/d0kmOTTCrFrcz/rdqzjdOmKeaS0UKccK5O5PXR+Y550BZqfyFFvxLJ3hXnG6v7ZBU18enfkeghyTYOnsuBLEJCYenWHnVGy33xe1XESCpNpt8K6PVgl5Rag1WWrYaa/dNplYYhlqdGeG+N1QKyBa3IsIfHPlUQKd7wQXncLibIK1p3BsqRLYQm+oT7Jrgx7vq72WYnlEJcTyljtaJQMsHGwgHtOxc0bDuCMpUJaC2h37wVCMojmq8oqBAxpH2Npeh8iqYjIJjko0ye9hCwOLFIBymv6QIDAQAB";

// Fixed development identity: chrome-extension://jkmmncllhpjaklglnieoaacgchndkgkd
export default defineConfig({
  manifest: {
    name: "XDL 下载接管",
    description: "把浏览器下载交给 XDL：多连接加速、断点续传，失败自动回退浏览器。",
    version: "0.2.0",
    key: publicKey,
    minimum_chrome_version: "116",
    permissions: [
      "nativeMessaging",
      "downloads",
      "webRequest",
      "cookies",
      "storage",
      "contextMenus",
      "notifications",
      "alarms",
      "tabs",
      "activeTab"
    ],
    host_permissions: ["<all_urls>"],
    action: { default_title: "XDL 下载接管" },
    options_ui: { page: "options.html", open_in_tab: true },
  },
  vite: () => ({ plugins: [react()] }),
  hooks: {
    "build:manifestGenerated": (_wxt, manifest) => {
      if (manifest.options_ui) manifest.options_ui.open_in_tab = true;
    },
  },
});
