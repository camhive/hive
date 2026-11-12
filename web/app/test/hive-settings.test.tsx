import { beforeEach, describe, expect, it, vi } from "vitest";
import { resetCloudForTest } from "../src/lib/cloud";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z", version: "0.3.0",
  lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"], cert_issuer: "letsencrypt" }, relay_host: "hv_1.relay.camhive.app" };

describe("hive connection settings", () => {
  it("shows the relay with the router hint when the LAN name fails, then the direct path after a successful re-probe", async () => {
    let lanUp = false;
    const calls: string[] = [];
    vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url);
      calls.push(`${new Request(input, init).method} ${url.pathname}`);
      if (url.host === "hv_1.h.camhive.app:16017") {
        if (lanUp) return json(200, { status: "ok" });
        throw new TypeError("Failed to fetch");
      }
      switch (`${new Request(input, init).method} ${url.pathname}`) {
        case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "2026-11-09T00:00:00Z" });
        case "GET /v1/hives": return json(200, { hives: [hive], shared: [] });
        case "DELETE /v1/hives/hv_1": return new Response(null, { status: 204 });
        default: return json(404, { error: { code: "not_found", message: "" } });
      }
    }) as typeof fetch);
    transport.invalidateAll();
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
    const user = userEvent.setup();
    await user.click(await screen.findByRole("link", { name: "Connection settings for Home" }));

    expect(await screen.findByRole("heading", { name: "Home" })).toBeInTheDocument();
    await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent("Through the Camhive relay"));
    expect(screen.getByTestId("rebind-hint")).toHaveTextContent("h.camhive.app");
    expect(screen.getByText("https://hv_1.relay.camhive.app")).toBeInTheDocument();
    expect(screen.getByText("Let's Encrypt")).toBeInTheDocument();
    // The reason row has a real label, not the i18n object behind the reason texts.
    expect(screen.getByText("Why")).toBeInTheDocument();
    expect(screen.queryByText(/returned an object/)).toBeNull();

    lanUp = true;
    await user.click(screen.getByRole("button", { name: "Check again" }));
    await waitFor(() => expect(screen.getByTestId("path")).toHaveTextContent("Direct on your network"));
    expect(screen.queryByTestId("rebind-hint")).toBeNull();
    expect(screen.getByText(/Chrome asks once/)).toBeInTheDocument();

    // The owner removes the hive after confirming; the list is refreshed and the app returns to the hives.
    vi.spyOn(window, "confirm").mockReturnValueOnce(false).mockReturnValueOnce(true);
    await user.click(screen.getByRole("button", { name: "Remove hive" }));
    expect(calls.filter((c) => c === "DELETE /v1/hives/hv_1")).toHaveLength(0);
    await user.click(screen.getByRole("button", { name: "Remove hive" }));
    await waitFor(() => expect(calls).toContain("DELETE /v1/hives/hv_1"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });
});
