import { defineContentScript } from "wxt/utils/define-content-script";
import { setupContent } from "../src/content/index";

export default defineContentScript({
  matches: ["<all_urls>"],
  runAt: "document_start",
  allFrames: false,
  main() {
    setupContent();
  },
});
