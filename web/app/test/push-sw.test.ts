import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it, vi } from "vitest";

type Listener = (e: { data?: { json: () => unknown }; notification?: FakeNotification; waitUntil: (p: Promise<unknown>) => void }) => void;
type FakeNotification = { data: unknown; close: () => void };

/** Runs public/push-sw.js against a fake service worker global. */
function loadWorker(windows: { url: string; focus: () => Promise<unknown>; navigate: (u: string) => Promise<unknown> }[] = []) {
  const listeners: Record<string, Listener> = {};
  const shown: { title: string; options: NotificationOptions }[] = [];
  const opened: string[] = [];
  const self = {
    location: { origin: "https://app.camhive.app" },
    addEventListener: (type: string, fn: Listener) => { listeners[type] = fn; },
    registration: { showNotification: async (title: string, options: NotificationOptions) => { shown.push({ title, options }); } },
    clients: {
      matchAll: async () => windows,
      openWindow: async (url: string) => { opened.push(url); },
    },
  };
  const src = readFileSync(join(process.cwd(), "public", "push-sw.js"), "utf8"); // vitest runs in web/app
  new Function("self", src)(self);
  async function dispatch(type: string, e: Omit<Parameters<Listener>[0], "waitUntil">) {
    let done: Promise<unknown> = Promise.resolve();
    listeners[type]!({ ...e, waitUntil: (p) => { done = p; } });
    await done;
  }
  return { dispatch, shown, opened };
}

describe("push service worker", () => {
  it("shows what the cloud sent, tagged so a newer one replaces it", async () => {
    const w = loadWorker();
    const payload = { kind: "motion", title: "Motion on Front door", body: "Home, 13:00", path: "/hives/hv_1?camera=cam_1&day=2026-11-16", tag: "abc" };
    await w.dispatch("push", { data: { json: () => payload } });
    expect(w.shown).toHaveLength(1);
    expect(w.shown[0]!.title).toBe("Motion on Front door");
    expect(w.shown[0]!.options).toMatchObject({ body: "Home, 13:00", tag: "abc", icon: "/icons/icon-192.v2.png", data: { path: payload.path } });
  });

  it("shows something even for an empty push", async () => {
    const w = loadWorker();
    await w.dispatch("push", {});
    expect(w.shown[0]!.title).toBe("Camhive");
  });

  it("opens the path of a clicked notification in an open window, or a new one", async () => {
    const navigate = vi.fn(async () => undefined);
    const focus = vi.fn(async () => undefined);
    const withWindow = loadWorker([{ url: "https://app.camhive.app/", focus, navigate }]);
    const close = vi.fn();
    await withWindow.dispatch("notificationclick", { notification: { data: { path: "/hives/hv_1" }, close } });
    expect(close).toHaveBeenCalled();
    expect(navigate).toHaveBeenCalledWith("https://app.camhive.app/hives/hv_1");
    expect(focus).toHaveBeenCalled();

    // A window the worker does not control refuses navigate(): open a new one.
    const uncontrolled = loadWorker([{ url: "https://app.camhive.app/", focus, navigate: async () => { throw new TypeError("not controlled"); } }]);
    await uncontrolled.dispatch("notificationclick", { notification: { data: { path: "/hives/hv_2" }, close } });
    expect(uncontrolled.opened).toEqual(["https://app.camhive.app/hives/hv_2"]);

    const none = loadWorker();
    await none.dispatch("notificationclick", { notification: { data: { path: "//evil.example/x" }, close } });
    expect(none.opened).toEqual(["https://app.camhive.app/"]); // only paths inside the app
  });
});
