import { defineBackground } from "wxt/utils/define-background";
import { setupBackground } from "../src/background/setup";

export default defineBackground(() => {
  setupBackground();
});
