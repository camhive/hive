// New app versions (#70). The service worker registers in prompt mode: a new
// version installs in the background and waits; the app offers it and
// reloads only when the user taps Update here, so a reload never cuts a
// live or recorded stream the user is watching. An app left open asks for a
// new version every hour.
import type { RegisterSWOptions } from "vite-plugin-pwa/types";

/**
 * current: nothing new. waiting: a new version is installed and waits.
 * requested: the user tapped Update, the new version is taking over.
 * ready: the new version took over from another window; this page still
 * runs the old code until it reloads.
 */
export type SwState = "current" | "waiting" | "requested" | "ready";

/**
 * waiting: a version is installed and waits (onNeedRefresh). controlling: a
 * new version controls the page (onNeedReload). timeout: the version did not
 * take over within ACTIVATE_WITHIN_MS of the tap.
 */
export type SwEvent = "waiting" | "tap" | "controlling" | "timeout";

export type SwEffect = "none" | "skip-waiting" | "reload";

const NEXT: Record<SwState, Partial<Record<SwEvent, { state: SwState; effect: SwEffect }>>> = {
  current: { waiting: { state: "waiting", effect: "none" }, controlling: { state: "ready", effect: "none" } },
  waiting: { tap: { state: "requested", effect: "skip-waiting" }, controlling: { state: "ready", effect: "none" } },
  requested: { controlling: { state: "requested", effect: "reload" }, timeout: { state: "waiting", effect: "none" } },
  ready: { tap: { state: "ready", effect: "reload" } },
};

/** The next state and what to do; the page reloads only after a tap in this window. */
export function swStep(state: SwState, event: SwEvent): { state: SwState; effect: SwEffect } {
  return NEXT[state][event] ?? { state, effect: "none" };
}

export const CHECK_EVERY_MS = 60 * 60 * 1000;

/** How long a tapped version has to take over before the offer comes back, so the user can tap again. */
export const ACTIVATE_WITHIN_MS = 30_000;

type Register = (options: RegisterSWOptions) => (reloadPage?: boolean) => Promise<void>;

/** The store: the service worker's events in, the banner's state out. */
export class SwUpdate {
  private state: SwState = "current";
  private readonly listeners = new Set<() => void>();
  private skipWaiting: () => void = () => undefined;
  private reload: () => void = () => undefined;
  private activating: ReturnType<typeof setTimeout> | undefined;

  /** Registers the service worker (virtual:pwa-register's registerSW). */
  attach(register: Register, reload: () => void = () => window.location.reload()): void {
    this.reload = reload;
    const updateSW = register({
      immediate: true,
      onNeedRefresh: () => this.dispatch("waiting"),
      onNeedReload: () => this.dispatch("controlling"),
      onRegisteredSW: (_url, registration) => { if (registration) checkHourly(registration); },
    });
    this.skipWaiting = () => void updateSW();
  }

  readonly subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  readonly snapshot = (): SwState => this.state;

  readonly tap = (): void => this.dispatch("tap");

  private dispatch(event: SwEvent): void {
    const { state, effect } = swStep(this.state, event);
    this.state = state;
    if (effect === "skip-waiting") {
      this.skipWaiting();
      this.activating = setTimeout(() => this.dispatch("timeout"), ACTIVATE_WITHIN_MS);
    }
    if (effect === "reload") {
      clearTimeout(this.activating); // it took over: nothing to undo
      this.reload();
    }
    this.listeners.forEach((fn) => fn());
  }
}

/** registration.update() every hour, and at once when the app comes back to the front after an hour or more (phones suspend timers). */
function checkHourly(registration: Pick<ServiceWorkerRegistration, "update">): void {
  let last = Date.now();
  const check = () => {
    if (document.visibilityState !== "visible") return; // in the background: the check on coming back covers it
    last = Date.now();
    if (navigator.onLine === false) return;
    registration.update().catch(() => undefined); // offline or the server is down: the next check tries again
  };
  setInterval(check, CHECK_EVERY_MS);
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible" && Date.now() - last >= CHECK_EVERY_MS) check();
  });
}

export const swUpdate = new SwUpdate();
