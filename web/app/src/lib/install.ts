// The in-app install offer (#70). Chrome and Edge fire beforeinstallprompt
// when the PWA can be installed, often before any page that would show a
// button is mounted, so one store catches it at start (main.tsx) and the
// Account page reads it. iOS has no such event: it gets a hint instead.

/** The event Chrome and Edge keep for a later prompt (not in the DOM types). */
export type DeferredPrompt = Event & {
  prompt(): Promise<void>;
  readonly userChoice: Promise<{ outcome: "accepted" | "dismissed"; platform: string }>;
};

export type InstallEnv = { standalone: boolean; ios: boolean };

export type InstallEvent = { type: "prompt"; prompt: DeferredPrompt } | { type: "used" } | { type: "installed" };

export type InstallState = { kind: "installed" } | { kind: "available"; prompt: DeferredPrompt } | { kind: "ios-hint" } | { kind: "none" };

function start(env: InstallEnv): InstallState {
  if (env.standalone) return { kind: "installed" };
  return env.ios ? { kind: "ios-hint" } : { kind: "none" };
}

/** Installed stays installed; a prompt can be used once, and the browser may fire a new one later. */
function step(s: InstallState, e: InstallEvent): InstallState {
  if (s.kind === "installed" || e.type === "installed") return { kind: "installed" };
  if (e.type === "prompt") return { kind: "available", prompt: e.prompt };
  return s.kind === "available" ? { kind: "none" } : s;
}

/** The state after the events, from where the app runs. */
export const installState = (env: InstallEnv, events: InstallEvent[]): InstallState => events.reduce(step, start(env));

/** Where this page runs: the installed app's window, and iPhone or iPad (iPadOS reports a Mac with touch). */
export function browserEnv(): InstallEnv {
  if (typeof window === "undefined") return { standalone: false, ios: false };
  const nav = navigator as Navigator & { standalone?: boolean };
  const ua = nav.userAgent;
  return {
    standalone: window.matchMedia?.("(display-mode: standalone)").matches === true || nav.standalone === true,
    ios: /iPhone|iPad|iPod/.test(ua) || (/Macintosh/.test(ua) && nav.maxTouchPoints > 1),
  };
}

/** The store: catches the browser's events and runs the prompt. */
export class InstallPrompt {
  private state: InstallState;
  private readonly listeners = new Set<() => void>();

  constructor(env: InstallEnv) {
    this.state = start(env);
  }

  attach(win: Window): void {
    win.addEventListener("beforeinstallprompt", (e) => {
      e.preventDefault(); // the app offers it in Account, not the browser's mini-infobar
      this.dispatch({ type: "prompt", prompt: e as DeferredPrompt });
    });
    win.addEventListener("appinstalled", () => this.dispatch({ type: "installed" }));
  }

  readonly subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  readonly snapshot = (): InstallState => this.state;

  /**
   * Shows the browser's install dialog; the prompt cannot be shown twice.
   * A browser that refuses (a stale event) leaves nothing to offer until it
   * fires a new one.
   */
  async install(): Promise<void> {
    if (this.state.kind !== "available") return;
    const { prompt } = this.state;
    this.dispatch({ type: "used" });
    try {
      await prompt.prompt();
      if ((await prompt.userChoice).outcome === "accepted") this.dispatch({ type: "installed" });
    } catch {
      /* refused: the state is already "none" */
    }
  }

  private dispatch(e: InstallEvent): void {
    this.state = step(this.state, e);
    this.listeners.forEach((fn) => fn());
  }
}

export const installPrompt = new InstallPrompt(browserEnv());
