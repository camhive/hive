import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./styles.css";
import { setupI18n } from "./i18n";
import { applyTheme, readTheme } from "./lib/theme";
import { transport } from "./lib/transport";
import { App } from "./App";
import { registerSW } from "virtual:pwa-register";

setupI18n();
applyTheme(readTheme());
transport.attach(window); // re-probe hive paths when the network changes
registerSW({ immediate: true }); // app shell only; see vite.config.ts
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App />
  </StrictMode>,
);
