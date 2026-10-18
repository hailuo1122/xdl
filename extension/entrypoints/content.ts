import { defineContentScript } from "wxt/utils/define-content-script";
import { setupContent } from "../src/content/index";

export default defineContentScript({
  matches: ["<all_urls>"],
  runAt: "document_start",
  // Players are frequently wrapped in an iframe, so sniffing must run in every
  // frame; setupContent() keeps UI and click interception top-document only.
  allFrames: true,
  main() {
    setupContent();
  },
});
