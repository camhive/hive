import { beforeEach, describe, expect, it, vi } from "vitest";
import { resetCloudForTest } from "../src/lib/cloud";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { formatClaimCode, installCommands, restartCommand } from "../src/lib/hives";

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const me = { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "2026-11-09T00:00:00Z" };

function stubApi(state: { hives: Array<{ id: string; name: string; status: string; online: boolean; claimed_at: string }>; limit?: boolean }) {
  const calls: string[] = [];
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const key = `${req.method} ${new URL(req.url).pathname}`;
    calls.push(key);
    switch (key) {
      case "GET /v1/me": return json(200, me);
      case "GET /v1/hives": return json(200, { hives: state.hives, shared: [] });
      case "GET /v1/me/plan": return json(200, { plan: { id: "free" }, addons: [], promos: [], entitlements: { "hives.max": 1 } });
      case "POST /v1/hives/claim": {
        const body = (await req.json()) as { code: string; name?: string };
        if (state.limit) return json(402, { error: { code: "plan_limit", message: "" } });
        if (body.code !== "K7QM-3XZP") return json(404, { error: { code: "unknown_code", message: "" } });
        state.hives.push({ id: "hv_0123456789abcdef", name: body.name ?? "", status: "active", online: false, claimed_at: "2026-11-09T00:00:00Z" });
        return json(201, { hive_id: "hv_0123456789abcdef", name: body.name ?? "" });
      }
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return calls;
}

function mount(path: string) {
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
  return userEvent.setup();
}

describe("claim wizard", () => {
  it("shows install commands per platform, refuses a bad code, claims, then sees the hive come online", async () => {
    const state = { hives: [] as Array<{ id: string; name: string; status: string; online: boolean; claimed_at: string }> };
    const calls = stubApi(state);
    const user = mount("/hives/add");

    // Step 1: the claim command points at this deployment's API.
    expect(await screen.findByText(/hive claim --api/)).toHaveTextContent("sudo hive claim --api http://localhost:3000");
    await user.click(screen.getByRole("tab", { name: "Windows" }));
    expect(screen.getByText(/hive\.exe claim/)).toBeInTheDocument();

    // Step 2: the code is normalised while typing; an unknown code is explained.
    const code = screen.getByLabelText("Claim code");
    await user.type(code, "zzzz zzzz");
    expect(code).toHaveValue("ZZZZ-ZZZZ");
    await user.type(screen.getByLabelText("Name (optional)"), "Home");
    await user.click(screen.getByRole("button", { name: "Claim hive" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Unknown or expired code.");

    await user.clear(code);
    await user.type(code, "k7qm3xzp");
    await user.click(screen.getByRole("button", { name: "Claim hive" }));

    // Step 3: waits, then flips to online once the list says so.
    const step = await screen.findByTestId("online-step");
    expect(step).toHaveTextContent("Claimed as “Home”");
    expect(step).toHaveTextContent("hive.exe restart");
    expect(step).toHaveTextContent("Waiting for the hive to come online…");
    state.hives[0]!.online = true;
    await waitFor(() => expect(step).toHaveTextContent("The hive is online."), { timeout: 6000 });
    expect(calls.filter((c) => c === "GET /v1/hives").length).toBeGreaterThanOrEqual(2);
  }, 15_000);

  it("explains the plan limit with the cap and an upgrade link", async () => {
    stubApi({ hives: [], limit: true });
    const user = mount("/hives/add");
    await user.type(await screen.findByLabelText("Claim code"), "K7QM-3XZP");
    await user.click(screen.getByRole("button", { name: "Claim hive" }));
    const box = await screen.findByTestId("plan-limit");
    await waitFor(() => expect(box).toHaveTextContent("Your plan allows 1 hive."));
    expect(screen.getByRole("link", { name: "See plans and upgrade" })).toHaveAttribute("href", "/plan");
  });

  it("lists hives with their online state on the home page", async () => {
    stubApi({ hives: [
      { id: "hv_a", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z" },
      { id: "hv_b", name: "Shop", status: "active", online: false, claimed_at: "2026-11-09T00:00:00Z" }] });
    mount("/");
    const items = await screen.findAllByTestId("hive");
    expect(items).toHaveLength(2);
    expect(items[0]).toHaveTextContent("Home");
    expect(items[0]).toHaveTextContent("Online");
    expect(items[1]).toHaveTextContent("Offline");
    expect(screen.getByRole("link", { name: "Add hive" })).toHaveAttribute("href", "/hives/add");
  });
});

describe("helpers", () => {
  it("formats claim codes as the agent prints them", () => {
    expect(formatClaimCode("k7qm3xzp")).toBe("K7QM-3XZP");
    expect(formatClaimCode(" k7q")).toBe("K7Q");
    expect(formatClaimCode("K7QM-3XZP-EXTRA")).toBe("K7QM-3XZP");
  });
  it("builds install commands per platform", () => {
    expect(installCommands("linux-deb", "https://api.camhive.app")).toEqual(["sudo dpkg -i camhive-hive_*_linux_amd64.deb", "sudo hive claim --api https://api.camhive.app"]);
    expect(installCommands("linux-rpm", "https://api.camhive.app")[0]).toContain("rpm -i");
    expect(installCommands("windows", "https://api.camhive.app")).toEqual(["hive.exe install", "hive.exe claim --api https://api.camhive.app"]);
    expect(restartCommand("windows")).toBe("hive.exe restart");
    expect(restartCommand("linux-deb")).toBe("sudo hive restart");
  });
});
