import { afterEach, describe, expect, it, vi } from "vitest";
import { act, cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { RegisterSWOptions } from "vite-plugin-pwa/types";
import { UpdateBanner } from "../src/components/UpdateBanner";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { ACTIVATE_WITHIN_MS, CHECK_EVERY_MS, SwUpdate, swStep } from "../src/lib/swUpdate";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

/** registerSW as vite-plugin-pwa hands it out: keeps the callbacks, returns updateSW. */
function fakeRegister() {
  const sw = { opts: {} as RegisterSWOptions, updateSW: vi.fn(async (_reload?: boolean) => undefined) };
  const register = (opts: RegisterSWOptions) => { sw.opts = opts; return sw.updateSW; };
  return { sw, register };
}

function attached() {
  const { sw, register } = fakeRegister();
  const reload = vi.fn();
  const store = new SwUpdate();
  store.attach(register, reload);
  return { store, sw, reload };
}

describe("update state machine", () => {
  it("offers a waiting version, activates it on the tap, reloads once it controls the page", () => {
    expect(swStep("current", "waiting")).toEqual({ state: "waiting", effect: "none" });
    expect(swStep("waiting", "tap")).toEqual({ state: "requested", effect: "skip-waiting" });
    expect(swStep("requested", "controlling")).toEqual({ state: "requested", effect: "reload" });
  });

  it("never reloads without a tap in this window", () => {
    // Another window's tap activated the new version: this page keeps playing and offers a reload.
    expect(swStep("waiting", "controlling")).toEqual({ state: "ready", effect: "none" });
    expect(swStep("current", "controlling")).toEqual({ state: "ready", effect: "none" });
    expect(swStep("ready", "tap")).toEqual({ state: "ready", effect: "reload" });
    expect(swStep("ready", "waiting")).toEqual({ state: "ready", effect: "none" });
  });

  it("offers the version again when the takeover never comes", () => {
    expect(swStep("requested", "timeout")).toEqual({ state: "waiting", effect: "none" });
    for (const s of ["current", "waiting", "ready"] as const) expect(swStep(s, "timeout")).toEqual({ state: s, effect: "none" });
  });

  it("ignores a tap with nothing to offer and a second tap while activating", () => {
    expect(swStep("current", "tap")).toEqual({ state: "current", effect: "none" });
    expect(swStep("requested", "tap")).toEqual({ state: "requested", effect: "none" });
    expect(swStep("requested", "waiting")).toEqual({ state: "requested", effect: "none" });
  });
});

describe("service worker updates", () => {
  it("registers in prompt mode, so a new version never reloads the app by itself", () => {
    // vitest runs in web/app; autoUpdate would keep every other test here green.
    expect(readFileSync(join(process.cwd(), "vite.config.ts"), "utf8")).toMatch(/registerType:\s*"prompt"/);
  });

  it("goes back to the offer when the new version does not take over in time", async () => {
    vi.useFakeTimers();
    const { store, sw, reload } = attached();
    sw.opts.onNeedRefresh!();
    store.tap();
    expect(store.snapshot()).toBe("requested");
    await vi.advanceTimersByTimeAsync(ACTIVATE_WITHIN_MS);
    expect(store.snapshot()).toBe("waiting");
    store.tap();
    expect(sw.updateSW).toHaveBeenCalledTimes(2);
    sw.opts.onNeedReload!();
    expect(reload).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(ACTIVATE_WITHIN_MS);
    expect(store.snapshot()).toBe("requested"); // a takeover in time leaves nothing to undo
  });

  it("does not ask while the app is in the background, and asks when it comes back", async () => {
    vi.useFakeTimers();
    const { sw } = attached();
    const update = vi.fn(async () => undefined);
    sw.opts.onRegisteredSW!("/sw.js", { update } as unknown as ServiceWorkerRegistration);
    const visibility = vi.spyOn(document, "visibilityState", "get").mockReturnValue("hidden");
    await vi.advanceTimersByTimeAsync(3 * CHECK_EVERY_MS);
    expect(update).not.toHaveBeenCalled();
    visibility.mockReturnValue("visible");
    document.dispatchEvent(new Event("visibilitychange"));
    expect(update).toHaveBeenCalledTimes(1);
    visibility.mockRestore();
  });

  it("registers in prompt mode: the update waits for the tap, then the page reloads", () => {
    const { store, sw, reload } = attached();
    expect(sw.opts.immediate).toBe(true);
    expect(store.snapshot()).toBe("current");

    act(() => sw.opts.onNeedRefresh!());
    expect(store.snapshot()).toBe("waiting");
    expect(sw.updateSW).not.toHaveBeenCalled();
    expect(reload).not.toHaveBeenCalled();

    store.tap();
    expect(sw.updateSW).toHaveBeenCalledTimes(1);
    expect(reload).not.toHaveBeenCalled();
    store.tap();
    expect(sw.updateSW).toHaveBeenCalledTimes(1);

    sw.opts.onNeedReload!();
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("does not reload when another window activated the new version", () => {
    const { store, sw, reload } = attached();
    sw.opts.onNeedRefresh!();
    sw.opts.onNeedReload!();
    expect(reload).not.toHaveBeenCalled();
    expect(store.snapshot()).toBe("ready");
    store.tap();
    expect(reload).toHaveBeenCalledTimes(1);
    expect(sw.updateSW).not.toHaveBeenCalled();
  });

  it("asks for a new version every hour while online, and when the app comes back after an hour away", async () => {
    vi.useFakeTimers();
    const { sw } = attached();
    const update = vi.fn(async () => undefined);
    sw.opts.onRegisteredSW!("/sw.js", { update } as unknown as ServiceWorkerRegistration);

    await vi.advanceTimersByTimeAsync(CHECK_EVERY_MS - 1);
    expect(update).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(update).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(CHECK_EVERY_MS);
    expect(update).toHaveBeenCalledTimes(2);

    // A phone suspends timers: back in front after more than an hour, it checks at once.
    vi.setSystemTime(Date.now() + CHECK_EVERY_MS + 1000);
    document.dispatchEvent(new Event("visibilitychange"));
    expect(update).toHaveBeenCalledTimes(3);
    document.dispatchEvent(new Event("visibilitychange"));
    expect(update).toHaveBeenCalledTimes(3);

    vi.stubGlobal("navigator", { ...navigator, onLine: false });
    await vi.advanceTimersByTimeAsync(CHECK_EVERY_MS);
    expect(update).toHaveBeenCalledTimes(3);
  });

  it("survives a failed check", async () => {
    vi.useFakeTimers();
    const { sw, store } = attached();
    const update = vi.fn(async () => { throw new TypeError("Failed to fetch"); });
    sw.opts.onRegisteredSW!("/sw.js", { update } as unknown as ServiceWorkerRegistration);
    await vi.advanceTimersByTimeAsync(CHECK_EVERY_MS);
    expect(update).toHaveBeenCalledTimes(1);
    expect(store.snapshot()).toBe("current");
  });
});

describe("update banner", () => {
  it("shows nothing until a new version waits, then updates on the tap", async () => {
    const { store, sw } = attached();
    render(<UpdateBanner store={store} />);
    expect(screen.queryByRole("button", { name: "Update" })).toBeNull();
    const region = screen.getByTestId("app-update"); // mounted and empty, so screen readers announce what is put in it
    expect(region).toHaveAttribute("aria-live", "polite");
    expect(region).toBeEmptyDOMElement();

    act(() => sw.opts.onNeedRefresh!());
    expect(screen.getByTestId("app-update")).toBe(region);
    expect(region).toHaveTextContent("A new version is available.");
    await userEvent.click(screen.getByRole("button", { name: "Update" }));
    expect(sw.updateSW).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: "Updating…" })).toBeDisabled();
  });

  it("offers a reload when another window already updated", async () => {
    const { store, sw, reload } = attached();
    render(<UpdateBanner store={store} />);
    act(() => { sw.opts.onNeedRefresh!(); sw.opts.onNeedReload!(); });
    await userEvent.click(screen.getByRole("button", { name: "Update" }));
    expect(reload).toHaveBeenCalledTimes(1);
  });
});
