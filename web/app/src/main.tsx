import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";
import { setupI18n } from "./i18n";
import { applyTheme, readTheme } from "./lib/theme";
import { transport } from "./lib/transport";
import { installPrompt } from "./lib/install";
import { swUpdate } from "./lib/swUpdate";
import { App } from "./App";
import { registerSW } from "virtual:pwa-register";

setupI18n();
applyTheme(readTheme());
transport.attach(window); // re-probe hive paths when the network changes
swUpdate.attach(registerSW); // app shell only (vite.config.ts); a new version waits for Update (#70)
installPrompt.attach(window); // before any page: Chrome fires beforeinstallprompt early (#70)
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
