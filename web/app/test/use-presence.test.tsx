import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import type { HiveClient } from "@camhive/api";
import { PRESENCE_EVERY_MS, resetPresenceForTest } from "../src/lib/archiveAutolock";
import { useReportPresence } from "../src/lib/usePresence";
import { encryptionKey } from "../src/lib/archiveEncryption";
import type { Hive } from "../src/lib/hives";

const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "", version: "0.1.10", capabilities: ["archive.autolock", "archive.encryption"] } as unknown as Hive;

function setup(atHome: boolean | null = false) {
  const sent: boolean[] = [];
  const client = { POST: vi.fn(async (_path: string, opts: { body: { at_home: boolean } }) => { sent.push(opts.body.at_home); return { data: undefined, response: new Response(null, { status: 204 }) }; }) } as unknown as HiveClient;
  const qc = new QueryClient();
  const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  const view = renderHook((p: { h: Hive; atHome: boolean | null }) => useReportPresence(p.h, client, p.atHome, true), { wrapper, initialProps: { h: hive, atHome } });
  return { sent, qc, view };
}

const flush = () => act(async () => { await Promise.resolve(); });

beforeEach(() => { vi.useFakeTimers(); resetPresenceForTest(); });
afterEach(() => { vi.useRealTimers(); });

describe("the presence report while a page is open (#147)", () => {
  it("reports on open and then once a minute", async () => {
    const { sent } = setup(false);
    await flush();
    expect(sent).toEqual([false]);
    await act(async () => { vi.advanceTimersByTime(PRESENCE_EVERY_MS); });
    await act(async () => { vi.advanceTimersByTime(PRESENCE_EVERY_MS); });
    expect(sent).toEqual([false, false, false]);
  });

  it("reports at once when the archive is unlocked or the hive runs another version", async () => {
    const { sent, qc, view } = setup(false);
    await flush();
    await act(async () => { qc.setQueryData(encryptionKey("hv_1"), { state: "locked", sealing: true }); });
    expect(sent).toEqual([false]);
    await act(async () => { qc.setQueryData(encryptionKey("hv_1"), { state: "unlocked", sealing: true }); });
    expect(sent).toEqual([false, false]);
    view.rerender({ h: { ...hive, version: "0.1.11" } as Hive, atHome: false });
    await flush();
    expect(sent).toEqual([false, false, false]);
  });

  it("reports nothing while the path is unknown", async () => {
    const { sent } = setup(null);
    await act(async () => { vi.advanceTimersByTime(3 * PRESENCE_EVERY_MS); });
    expect(sent).toEqual([]);
  });
});
