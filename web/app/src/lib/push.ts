// Web Push in the app (ADR-0021 §7): the account's devices, and this
// browser subscribing through the service worker (public/push-sw.js).
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { unwrap } from "@camhive/api";
import { api } from "./api";
import { browserEnv } from "./install";

const KEY = ["me", "push"];

export function usePush() {
  return useQuery({ queryKey: KEY, queryFn: async () => unwrap(await api.GET("/v1/me/push")) });
}

export function useRemoveDevice() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => unwrap(await api.DELETE("/v1/me/push/devices/{id}", { params: { path: { id } } })),
    onSuccess: () => qc.invalidateQueries({ queryKey: KEY }),
  });
}

export function useTestPush() {
  return useMutation({ mutationFn: async () => unwrap(await api.POST("/v1/me/push/test")).sent });
}

export type Support = "ok" | "unsupported" | "install-first";

/** iOS delivers Web Push only to a PWA added to the home screen (#70). */
export function pushSupport(env: { serviceWorker: boolean; pushManager: boolean; ios: boolean; standalone: boolean }): Support {
  if (env.ios && !env.standalone) return "install-first";
  return env.serviceWorker && env.pushManager ? "ok" : "unsupported";
}

/** What this browser can do right now. */
export function browserSupport(): Support {
  const nav = typeof navigator === "undefined" ? undefined : navigator;
  return pushSupport({
    serviceWorker: !!nav && "serviceWorker" in nav,
    pushManager: typeof window !== "undefined" && "PushManager" in window,
    ...browserEnv(),
  });
}

/** "Firefox on Linux": enough to tell one's devices apart. */
export function deviceLabel(ua: string): string {
  const browser = /Edg\//.test(ua) ? "Edge" : /Firefox\//.test(ua) ? "Firefox" : /Chrome\//.test(ua) ? "Chrome" : /Safari\//.test(ua) ? "Safari" : "";
  const system = /iPhone/.test(ua) ? "iPhone" : /iPad/.test(ua) ? "iPad" : /Android/.test(ua) ? "Android" : /Windows/.test(ua) ? "Windows"
    : /Mac OS X/.test(ua) ? "macOS" : /Linux/.test(ua) ? "Linux" : "";
  if (!browser) return "Browser";
  return system ? `${browser} on ${system}` : browser;
}

/** The server's base64url key as the bytes PushManager.subscribe wants. */
export function keyBytes(b64url: string): Uint8Array<ArrayBuffer> {
  const bin = atob(b64url.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - (b64url.length % 4)) % 4));
  const out = new Uint8Array(new ArrayBuffer(bin.length));
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/** Asks for permission, subscribes this browser and registers it. A
 * subscription made with another server key (the cloud's key was replaced)
 * is dropped first; subscribing again with the same key returns the same
 * subscription, and registering it again is harmless. */
export async function enablePush(publicKey: string): Promise<void> {
  if ((await Notification.requestPermission()) !== "granted") throw new Error("notification permission refused");
  const reg = await navigator.serviceWorker.ready;
  const key = keyBytes(publicKey);
  const existing = await reg.pushManager.getSubscription();
  if (existing && !sameKey(existing.options.applicationServerKey, key)) await existing.unsubscribe();
  const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: key });
  const { endpoint, keys } = sub.toJSON() as { endpoint: string; keys: { p256dh: string; auth: string } };
  unwrap(await api.POST("/v1/me/push/devices", { body: { endpoint, keys: { p256dh: keys.p256dh, auth: keys.auth }, label: deviceLabel(navigator.userAgent) } }));
}

function sameKey(a: ArrayBuffer | null, b: Uint8Array): boolean {
  if (!a || a.byteLength !== b.length) return false;
  const x = new Uint8Array(a);
  return x.every((v, i) => v === b[i]);
}

/** Hex SHA-256, as the cloud reports a device's endpoint. */
export async function sha256Hex(text: string): Promise<string> {
  const sum = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return Array.from(new Uint8Array(sum), (b) => b.toString(16).padStart(2, "0")).join("");
}

async function currentSubscription(): Promise<PushSubscription | null> {
  if (typeof navigator === "undefined" || !navigator.serviceWorker) return null;
  const reg = await navigator.serviceWorker.getRegistration();
  return (await reg?.pushManager.getSubscription()) ?? null;
}

/** The signed-in account's device that is this browser, if any: its
 * subscription is made with the current key and its endpoint's hash is in
 * the account's list. Another account's subscription on a shared browser
 * does not count. */
export async function thisDevice(devices: { id: string; endpoint_sha256: string }[], publicKey: string): Promise<string | null> {
  const sub = await currentSubscription();
  if (!sub || !sameKey(sub.options.applicationServerKey, keyBytes(publicKey))) return null;
  const hash = await sha256Hex(sub.endpoint);
  return devices.find((d) => d.endpoint_sha256 === hash)?.id ?? null;
}

/** On sign-out: this browser stops getting the account's notifications.
 * Best effort; signing out must not fail because of it. */
export async function leavePushHere(): Promise<void> {
  try {
    const sub = await currentSubscription();
    if (!sub) return;
    const state = unwrap(await api.GET("/v1/me/push"));
    const hash = await sha256Hex(sub.endpoint);
    const mine = state.devices.find((d) => d.endpoint_sha256 === hash);
    if (mine) unwrap(await api.DELETE("/v1/me/push/devices/{id}", { params: { path: { id: mine.id } } }));
    await sub.unsubscribe();
  } catch {
    // the device row is removed by the push service's 410 later at the latest
  }
}
