import { describe, expect, it, vi } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { msUntilRefresh, useAccess, REFRESH_MARGIN_MS, MIN_REFRESH_MS, MAX_REFRESH_MS } from "../src/lib/access";

describe("access tokens", () => {
  it("schedules renewal a minute before expiry, never sooner than the floor", () => {
    const now = Date.parse("2026-11-10T00:00:00Z");
    expect(msUntilRefresh("2026-11-10T00:15:00Z", now)).toBe(15 * 60_000 - REFRESH_MARGIN_MS);
    expect(msUntilRefresh("2026-11-10T00:00:30Z", now)).toBe(MIN_REFRESH_MS);
    expect(msUntilRefresh("2026-11-09T00:00:00Z", now)).toBe(MIN_REFRESH_MS);
    expect(msUntilRefresh("2099-01-01T00:00:00Z", now)).toBe(MAX_REFRESH_MS);
  });

  it("issues a token for the hive with the CSRF header and exposes the grant", async () => {
    const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      expect(req.method).toBe("POST");
      expect(new URL(req.url).pathname).toBe("/v1/hives/hv_1/access");
      expect(req.headers.get("X-Requested-With")).toBe("camhive");
      const body = (await req.json()) as { client: string; offline: boolean };
      expect(body.offline).toBe(false);
      return new Response(JSON.stringify({ token: "v4.public.x", session_id: "s1", expires_at: "2099-01-01T00:00:00Z", offline: false, role: "owner", cameras: ["*"], hive: { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z" }, ice_servers: [{ urls: ["stun:s"] }] }), { status: 201, headers: { "Content-Type": "application/json" } });
    });
    vi.stubGlobal("fetch", fetch);
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
    const { result } = renderHook(() => useAccess("hv_1"), { wrapper });
    await waitFor(() => expect(result.current.data?.token).toBe("v4.public.x"));
    expect(result.current.data?.ice_servers).toEqual([{ urls: ["stun:s"] }]);
    const idle = renderHook(() => useAccess(undefined), { wrapper });
    expect(idle.result.current.fetchStatus).toBe("idle");
  });
});
