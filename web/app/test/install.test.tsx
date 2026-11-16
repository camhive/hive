import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { InstallCard } from "../src/components/InstallCard";
import { InstallPrompt, browserEnv, installState, type DeferredPrompt } from "../src/lib/install";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** A beforeinstallprompt event as Chrome and Edge fire it. */
function promptEvent(outcome: "accepted" | "dismissed") {
  const ev = new Event("beforeinstallprompt", { cancelable: true }) as Event & DeferredPrompt;
  ev.prompt = vi.fn(async () => undefined);
  Object.defineProperty(ev, "userChoice", { value: Promise.resolve({ outcome, platform: "web" }) });
  return ev;
}

const env = (over: Partial<{ standalone: boolean; ios: boolean }> = {}) => ({ standalone: false, ios: false, ...over });

describe("install state machine", () => {
  it("starts installed in the app window, with a hint on iOS, else with nothing to offer", () => {
    expect(installState(env({ standalone: true }), []).kind).toBe("installed");
    expect(installState(env({ standalone: true, ios: true }), []).kind).toBe("installed");
    expect(installState(env({ ios: true }), []).kind).toBe("ios-hint");
    expect(installState(env(), []).kind).toBe("none");
  });

  it("offers the browser's prompt until it is used or the app is installed", () => {
    const p = promptEvent("accepted");
    expect(installState(env(), [{ type: "prompt", prompt: p }])).toEqual({ kind: "available", prompt: p });
    expect(installState(env(), [{ type: "prompt", prompt: p }, { type: "used" }]).kind).toBe("none");
    expect(installState(env(), [{ type: "prompt", prompt: p }, { type: "installed" }]).kind).toBe("installed");
    expect(installState(env(), [{ type: "installed" }, { type: "prompt", prompt: p }]).kind).toBe("installed");
    expect(installState(env({ standalone: true }), [{ type: "prompt", prompt: p }]).kind).toBe("installed");
  });
});

describe("install prompt in the browser", () => {
  it("catches the browser's event, installs from the button and hides once installed", async () => {
    const win = new EventTarget() as unknown as Window;
    const store = new InstallPrompt(env());
    store.attach(win);
    render(<InstallCard store={store} />);
    expect(screen.queryByRole("button", { name: "Install Camhive" })).toBeNull();

    const ev = promptEvent("accepted");
    act(() => { win.dispatchEvent(ev); });
    expect(ev.defaultPrevented).toBe(true); // no mini-infobar: the app offers it
    await userEvent.click(screen.getByRole("button", { name: "Install Camhive" }));
    expect(ev.prompt).toHaveBeenCalledOnce();
    expect(screen.queryByRole("button", { name: "Install Camhive" })).toBeNull(); // the event is used up

    act(() => { win.dispatchEvent(new Event("appinstalled")); });
    expect(screen.queryByRole("region", { name: "Install the app" })).toBeNull();
  });

  it("offers the button again when the browser fires a new prompt after a dismissal", async () => {
    const win = new EventTarget() as unknown as Window;
    const store = new InstallPrompt(env());
    store.attach(win);
    render(<InstallCard store={store} />);
    act(() => { win.dispatchEvent(promptEvent("dismissed")); });
    await userEvent.click(screen.getByRole("button", { name: "Install Camhive" }));
    expect(screen.queryByRole("button", { name: "Install Camhive" })).toBeNull();
    act(() => { win.dispatchEvent(promptEvent("accepted")); });
    expect(screen.getByRole("button", { name: "Install Camhive" })).toBeInTheDocument();
  });

  it("hides the offer when the app is installed from the browser's own menu", () => {
    const win = new EventTarget() as unknown as Window;
    const store = new InstallPrompt(env());
    store.attach(win);
    render(<InstallCard store={store} />);
    act(() => { win.dispatchEvent(promptEvent("accepted")); });
    expect(screen.getByRole("button", { name: "Install Camhive" })).toBeInTheDocument();
    act(() => { win.dispatchEvent(new Event("appinstalled")); }); // no click in the app
    expect(screen.queryByRole("region", { name: "Install the app" })).toBeNull();
  });

  it("stays installed after an accepted prompt, even when the browser fires another one", async () => {
    const win = new EventTarget() as unknown as Window;
    const store = new InstallPrompt(env());
    store.attach(win); // no appinstalled event follows here
    render(<InstallCard store={store} />);
    act(() => { win.dispatchEvent(promptEvent("accepted")); });
    await userEvent.click(screen.getByRole("button", { name: "Install Camhive" }));
    act(() => { win.dispatchEvent(promptEvent("accepted")); });
    expect(screen.queryByRole("button", { name: "Install Camhive" })).toBeNull();
  });

  it("does not fail when the browser refuses to show the prompt", async () => {
    const win = new EventTarget() as unknown as Window;
    const store = new InstallPrompt(env());
    store.attach(win);
    const ev = promptEvent("accepted");
    ev.prompt = vi.fn(async () => { throw new DOMException("already shown", "InvalidStateError"); });
    act(() => { win.dispatchEvent(ev); });
    await expect(store.install()).resolves.toBeUndefined();
    expect(store.snapshot().kind).toBe("none");
  });

  it("explains Add to Home Screen on iOS and shows nothing in the installed app", () => {
    render(<InstallCard store={new InstallPrompt(env({ ios: true }))} />);
    expect(screen.getByRole("region", { name: "Install the app" })).toHaveTextContent("Share → Add to Home Screen");
    cleanup();
    render(<InstallCard store={new InstallPrompt(env({ standalone: true }))} />);
    expect(screen.queryByRole("region", { name: "Install the app" })).toBeNull();
  });
});

describe("where the app runs", () => {
  const stubBrowser = (ua: string, opts: { touch?: number; standalone?: boolean; displayStandalone?: boolean } = {}) => {
    vi.stubGlobal("navigator", { userAgent: ua, maxTouchPoints: opts.touch ?? 0, ...(opts.standalone !== undefined ? { standalone: opts.standalone } : {}) });
    vi.stubGlobal("matchMedia", (q: string) => ({ matches: q === "(display-mode: standalone)" && !!opts.displayStandalone }));
  };
  const IPHONE = "Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1";
  const MAC = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Safari/605.1.15";
  const CHROME = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36";

  it("tells iPhone, iPad (a Mac with touch) and a Mac apart", () => {
    stubBrowser(IPHONE, { touch: 5 });
    expect(browserEnv()).toEqual({ ios: true, standalone: false });
    stubBrowser(MAC, { touch: 5 }); // iPadOS Safari asks for the desktop site
    expect(browserEnv().ios).toBe(true);
    stubBrowser(MAC, { touch: 0 });
    expect(browserEnv().ios).toBe(false);
    stubBrowser(CHROME);
    expect(browserEnv()).toEqual({ ios: false, standalone: false });
  });

  it("knows the installed app's window from iOS and from display-mode", () => {
    stubBrowser(IPHONE, { touch: 5, standalone: true });
    expect(browserEnv()).toEqual({ ios: true, standalone: true });
    stubBrowser(CHROME, { displayStandalone: true });
    expect(browserEnv().standalone).toBe(true);
  });
});
