import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { PushDevices } from "../src/components/PushDevices";
import { deviceLabel, enablePush, keyBytes, leavePushHere, pushSupport, sha256Hex, thisDevice } from "../src/lib/push";

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("push helpers", () => {
  it("names a device after its browser and system", () => {
    expect(deviceLabel("Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0")).toBe("Firefox on Linux");
    expect(deviceLabel("Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Mobile Safari/537.36")).toBe("Chrome on Android");
    expect(deviceLabel("Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1")).toBe("Safari on iPhone");
    expect(deviceLabel("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36 Edg/141.0")).toBe("Edge on Windows");
    expect(deviceLabel("curl/8")).toBe("Browser");
  });

  it("decodes the server key and tells where push cannot work", () => {
    expect(Array.from(keyBytes("BAEC_w"))).toEqual([4, 1, 2, 255]);
    expect(pushSupport({ serviceWorker: true, pushManager: true, ios: false, standalone: false })).toBe("ok");
    expect(pushSupport({ serviceWorker: true, pushManager: false, ios: true, standalone: false })).toBe("install-first");
    expect(pushSupport({ serviceWorker: true, pushManager: true, ios: true, standalone: true })).toBe("ok");
    expect(pushSupport({ serviceWorker: false, pushManager: false, ios: false, standalone: false })).toBe("unsupported");
  });

  it("asks for permission, subscribes with the server key and registers the device", async () => {
    const subscribe = vi.fn(async () => ({ toJSON: () => ({ endpoint: "https://fcm.googleapis.com/fcm/send/x", keys: { p256dh: "P", auth: "A" } }) }));
    vi.stubGlobal("Notification", { requestPermission: async () => "granted" });
    vi.stubGlobal("navigator", { userAgent: "Mozilla/5.0 (X11; Linux x86_64) Firefox/140.0", serviceWorker: { ready: Promise.resolve({ pushManager: { subscribe, getSubscription: async () => null } }) } });
    const posted: unknown[] = [];
    vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      posted.push(await req.json());
      return json(201, { id: "pd_1", label: "Firefox on Linux", created_at: "2026-11-16T12:00:00Z", active: true });
    }) as typeof fetch);
    await enablePush("BAEC_w");
    expect(subscribe).toHaveBeenCalledWith({ userVisibleOnly: true, applicationServerKey: keyBytes("BAEC_w") });
    expect(posted).toEqual([{ endpoint: "https://fcm.googleapis.com/fcm/send/x", keys: { p256dh: "P", auth: "A" }, label: "Firefox on Linux" }]);
  });

  it("does nothing when permission is refused", async () => {
    vi.stubGlobal("Notification", { requestPermission: async () => "denied" });
    vi.stubGlobal("navigator", { userAgent: "x", serviceWorker: { ready: Promise.resolve({ pushManager: { subscribe: vi.fn() } }) } });
    await expect(enablePush("BAEC_w")).rejects.toThrow("permission");
  });
});

describe("push devices", () => {
  it("lists devices, marks sleeping ones, removes one and sends a test", async () => {
    let devices = [
      { id: "pd_2", label: "Chrome on Android", created_at: "2026-11-16T12:00:00Z", last_used_at: "2026-11-16T13:00:00Z", active: true },
      { id: "pd_1", label: "Firefox on Linux", created_at: "2026-11-15T12:00:00Z", active: false },
    ];
    const calls: string[] = [];
    vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      const key = `${req.method} ${new URL(req.url).pathname}`;
      calls.push(key);
      switch (key) {
        case "GET /v1/me/push": return json(200, { configured: true, public_key: "BAEC_w", devices, limit: 1 });
        case "DELETE /v1/me/push/devices/pd_1": devices = devices.filter((d) => d.id !== "pd_1"); return new Response(null, { status: 204 });
        case "POST /v1/me/push/test": return json(200, { sent: 1 });
        default: return json(404, { error: { code: "not_found", message: "" } });
      }
    }) as typeof fetch);
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><PushDevices /></QueryClientProvider>);
    const user = userEvent.setup();

    expect(await screen.findByText("Chrome on Android")).toBeInTheDocument();
    expect(screen.getByText(/over your plan's limit/)).toBeInTheDocument();
    expect(screen.getByText(/cannot receive push/)).toBeInTheDocument(); // jsdom has no service worker
    await user.click(screen.getByRole("button", { name: "Remove Firefox on Linux" }));
    await waitFor(() => expect(screen.queryByText("Firefox on Linux")).toBeNull());
    await user.click(screen.getByRole("button", { name: "Send a test notification" }));
    expect(await screen.findByRole("status")).toHaveTextContent("Sent to 1 device.");
    expect(calls).toContain("DELETE /v1/me/push/devices/pd_1");
  });

  it("says when the server has no push", async () => {
    vi.stubGlobal("fetch", (async () => json(200, { configured: false, devices: [], limit: 1 })) as typeof fetch);
    render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}><PushDevices /></QueryClientProvider>);
    expect(await screen.findByText(/not set up on this server/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send a test notification" })).toBeNull();
  });
});

describe("push after a key change and on a shared browser (review of #112)", () => {
  type FakeSub = { endpoint: string; options: { applicationServerKey: ArrayBuffer }; unsubscribe: ReturnType<typeof vi.fn>; toJSON: () => unknown };
  function fakeBrowser(existing: FakeSub | null) {
    const subscribe = vi.fn(async () => ({ toJSON: () => ({ endpoint: "https://fcm.googleapis.com/fcm/send/new", keys: { p256dh: "P", auth: "A" } }) }));
    const pushManager = { subscribe, getSubscription: async () => existing };
    vi.stubGlobal("Notification", { requestPermission: async () => "granted" });
    vi.stubGlobal("PushManager", function PushManager() {});
    vi.stubGlobal("navigator", { userAgent: "x", serviceWorker: { ready: Promise.resolve({ pushManager }), getRegistration: async () => ({ pushManager }) } });
    return subscribe;
  }
  const sub = (key: string): FakeSub => ({ endpoint: "https://fcm.googleapis.com/fcm/send/old", options: { applicationServerKey: keyBytes(key).buffer }, unsubscribe: vi.fn(async () => true), toJSON: () => ({}) });

  it("drops a subscription made with another server key before subscribing again", async () => {
    const old = sub("AQID");
    const subscribe = fakeBrowser(old);
    vi.stubGlobal("fetch", (async () => json(201, { id: "pd_1", label: "x", endpoint_sha256: "h", created_at: "2026-11-16T12:00:00Z", active: true })) as typeof fetch);
    await enablePush("BAEC_w");
    expect(old.unsubscribe).toHaveBeenCalled();
    expect(subscribe).toHaveBeenCalled();
  });

  it("keeps a subscription made with the current key", async () => {
    const same = sub("BAEC_w");
    fakeBrowser(same);
    vi.stubGlobal("fetch", (async () => json(201, { id: "pd_1", label: "x", endpoint_sha256: "h", created_at: "2026-11-16T12:00:00Z", active: true })) as typeof fetch);
    await enablePush("BAEC_w");
    expect(same.unsubscribe).not.toHaveBeenCalled();
  });

  it("recognises this browser only among the signed-in account's devices", async () => {
    const mine = sub("BAEC_w");
    fakeBrowser(mine);
    const hash = await sha256Hex(mine.endpoint);
    expect(await thisDevice([{ id: "pd_1", endpoint_sha256: hash }], "BAEC_w")).toBe("pd_1");
    expect(await thisDevice([{ id: "pd_2", endpoint_sha256: "other" }], "BAEC_w")).toBeNull(); // another account enabled push here
    expect(await thisDevice([{ id: "pd_1", endpoint_sha256: hash }], "AQID")).toBeNull(); // made with an old key
  });

  it("signing out removes this browser's device and unsubscribes", async () => {
    const mine = sub("BAEC_w");
    fakeBrowser(mine);
    const hash = await sha256Hex(mine.endpoint);
    const calls: string[] = [];
    vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      calls.push(`${req.method} ${new URL(req.url).pathname}`);
      if (req.method === "GET") return json(200, { configured: true, public_key: "BAEC_w", devices: [{ id: "pd_1", label: "x", endpoint_sha256: hash, created_at: "2026-11-16T12:00:00Z", active: true }], limit: 1 });
      return new Response(null, { status: 204 });
    }) as typeof fetch);
    await leavePushHere();
    expect(calls).toEqual(["GET /v1/me/push", "DELETE /v1/me/push/devices/pd_1"]);
    expect(mine.unsubscribe).toHaveBeenCalled();
  });
});
