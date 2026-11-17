import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { resetCloudForTest } from "../src/lib/cloud";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";
import { transport } from "../src/lib/transport";

beforeEach(() => { resetCloudForTest(); localStorage.clear(); });

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const hive = { id: "hv_1", name: "Home", status: "active", online: true, claimed_at: "2026-11-09T00:00:00Z", lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] }, relay_host: "hv_1.relay.camhive.app" };

type GrantRow = { id: string; member_id: string; member_email: string; camera_id: string; role: string; created_at: string; state: string; expires_at?: string };
const inDays = (d: number) => new Date(Date.now() + d * 86_400_000).toISOString();

function stub(opts: { signedIn?: boolean; memberLimit?: boolean; shareLimit?: boolean; grants?: GrantRow[]; notices?: unknown[]; grantError?: string } = {}) {
  const state = {
    members: [{ id: "m_1", email: "friend@example.de", status: "active", since: "2026-11-01T00:00:00Z" }],
    invitations: [{ id: "inv_1", email: "pending@example.de", expires_at: "2026-11-17T00:00:00Z" }],
    grants: opts.grants ?? [{ id: "g_1", member_id: "m_1", member_email: "friend@example.de", camera_id: "cam_1", role: "viewer", created_at: "2026-11-02T00:00:00Z", state: "active" }] as GrantRow[],
    calls: [] as string[],
    bodies: [] as unknown[],
    accepted: "",
  };
  vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
    const req = new Request(input, init);
    const u = new URL(req.url);
    const key = `${req.method} ${u.pathname}`;
    state.calls.push(key);
    if (u.host === "hv_1.h.camhive.app:16017") {
      if (u.pathname === "/healthz") return json(200, {});
      if (u.pathname === "/v0/cameras") return json(200, [{ id: "cam_1", name: "Door", group: "", enabled: true, source: { kind: "rtsp", address: "" }, streams: {}, created_at: "", updated_at: "" }, { id: "cam_2", name: "Garden", group: "", enabled: true, source: { kind: "rtsp", address: "" }, streams: {}, created_at: "", updated_at: "" }]);
      return json(404, { code: "not_found", message: "" });
    }
    switch (key) {
      case "GET /v1/me": return opts.signedIn === false ? json(401, { error: { code: "unauthenticated", message: "" } }) : json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "" });
      case "GET /v1/hives": return json(200, { hives: [hive], shared: [] });
      case "POST /v1/hives/hv_1/access": return json(201, { token: "tok", session_id: "s_1", expires_at: new Date(Date.now() + 900_000).toISOString(), offline: false, role: "owner", cameras: ["*"], hive, quality: "full", ice_servers: [] });
      case "GET /v1/me/plan": return json(200, { plan: {}, addons: [], promos: [], entitlements: { "members.max": 1, "shares.max": 2 } });
      case "GET /v1/members": return json(200, { members: state.members, invitations: state.invitations });
      case "POST /v1/members/invitations": {
        const b = (await req.json()) as { email: string };
        state.bodies.push(b);
        if (opts.memberLimit) return json(402, { error: { code: "plan_limit", message: "" } });
        if (b.email === "anna@example.de") return json(409, { error: { code: "member_state", message: "" } });
        state.invitations.push({ id: "inv_2", email: b.email, expires_at: "2026-11-17T00:00:00Z" });
        return json(201, { id: "inv_2", email: b.email, link: "https://app.test/invite?token=abc", expires_at: "2026-11-17T00:00:00Z" });
      }
      case "DELETE /v1/members/invitations/inv_1": state.invitations.splice(0, 1); return new Response(null, { status: 204 });
      case "DELETE /v1/members/m_1": state.members.splice(0, 1); return new Response(null, { status: 204 });
      case "POST /v1/invitations/accept": {
        const b = (await req.json()) as { token: string };
        state.accepted = b.token;
        if (b.token === "stale") return json(404, { error: { code: "bad_invitation", message: "" } });
        return json(201, { member_id: "m_9", owner_id: "acc_2" });
      }
      case "GET /v1/hives/hv_1/grants": return json(200, { grants: state.grants });
      case "PUT /v1/hives/hv_1/grants": {
        const b = (await req.json()) as { member_id: string; camera_id: string; role: string; expires_at?: string };
        state.bodies.push(b);
        if (opts.shareLimit) return json(402, { error: { code: "plan_limit", message: "" } });
        if (opts.grantError) return json(400, { error: { code: opts.grantError, message: "" } });
        state.grants.push({ id: "g_2", member_id: b.member_id, member_email: "friend@example.de", camera_id: b.camera_id, role: b.role, created_at: "", state: "active" });
        return json(200, { id: "g_2", member_id: b.member_id, camera_id: b.camera_id, role: b.role });
      }
      case "GET /v1/me/grant-notices": return json(200, { notices: opts.notices ?? [] });
      case "DELETE /v1/hives/hv_1/grants/g_1": state.grants.splice(0, 1); return new Response(null, { status: 204 });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  }) as typeof fetch);
  return state;
}

function mount(path: string) {
  transport.invalidateAll();
  const router = makeRouter(createMemoryHistory({ initialEntries: [path] }));
  render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
  return { user: userEvent.setup(), router };
}
/** Unmounts, lets requests still in flight reach the stubbed fetch, then removes the stubs (a late request hitting the real fetch would mark the cloud down for the next test). */
async function settle() {
  cleanup();
  await new Promise((r) => setTimeout(r, 30));
  vi.unstubAllGlobals();
}

afterEach(settle);

describe("members page", () => {
  it("lists members and invitations, invites with the link shown, explains member_state and plan_limit, cancels and removes", async () => {
    const state = stub();
    const { user } = mount("/members");
    expect(await screen.findByTestId("member")).toHaveTextContent("friend@example.de");
    expect(screen.getByTestId("invitation")).toHaveTextContent("pending@example.de");

    await user.type(screen.getByLabelText("E-mail"), "anna@example.de");
    await user.click(screen.getByRole("button", { name: "Send invitation" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("This member cannot be changed right now.");
    await user.clear(screen.getByLabelText("E-mail"));
    await user.type(screen.getByLabelText("E-mail"), "new@example.de");
    await user.click(screen.getByRole("button", { name: "Send invitation" }));
    expect(await screen.findByRole("status")).toHaveTextContent("https://app.test/invite?token=abc");
    await waitFor(() => expect(screen.getAllByTestId("invitation")).toHaveLength(2));

    await user.click(within(screen.getAllByTestId("invitation")[0]!).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(state.calls).toContain("DELETE /v1/members/invitations/inv_1"));
    vi.spyOn(window, "confirm").mockReturnValue(true);
    await user.click(screen.getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(state.calls).toContain("DELETE /v1/members/m_1"));
    expect(await screen.findByText("Nobody yet.")).toBeInTheDocument();
  });

  it("shows the member cap from the plan on plan_limit", async () => {
    stub({ memberLimit: true });
    const { user } = mount("/members");
    await user.type(await screen.findByLabelText("E-mail"), "x@example.de");
    await user.click(screen.getByRole("button", { name: "Send invitation" }));
    await waitFor(() => expect(screen.getByTestId("plan-limit")).toHaveTextContent("Your plan allows 1 member."));
  });
});

describe("hive sharing page", () => {
  it("lists grants with camera names, adds a per-camera grant, revokes one, and explains shares.max", async () => {
    const state = stub();
    const { user } = mount("/hives/hv_1/sharing");
    const grant = await screen.findByTestId("grant");
    await waitFor(() => expect(grant).toHaveTextContent("friend@example.de · Door · viewer"));
    await user.selectOptions(screen.getByLabelText("Member"), "m_1");
    await user.selectOptions(await screen.findByLabelText("Camera"), "cam_2");
    await user.selectOptions(screen.getByLabelText("Role"), "operator");
    await user.click(screen.getByRole("button", { name: "Save grant" }));
    await waitFor(() => expect(state.bodies.at(-1)).toEqual({ member_id: "m_1", camera_id: "cam_2", role: "operator" }));
    await waitFor(() => expect(screen.getAllByTestId("grant")).toHaveLength(2));
    expect(screen.getAllByTestId("grant")[1]).toHaveTextContent("Garden · operator");
    await user.click(within(screen.getAllByTestId("grant")[0]!).getByRole("button", { name: "Revoke the share of Door with friend@example.de" }));
    await waitFor(() => expect(state.calls).toContain("DELETE /v1/hives/hv_1/grants/g_1"));
  });

  it("shows the sharing cap on plan_limit", async () => {
    stub({ shareLimit: true });
    const { user } = mount("/hives/hv_1/sharing");
    await user.selectOptions(await screen.findByLabelText("Member"), "m_1");
    await user.click(screen.getByRole("button", { name: "Save grant" }));
    await waitFor(() => expect(screen.getByTestId("plan-limit")).toHaveTextContent("Your plan allows 2 shares."));
  });
});

describe("invitation page", () => {
  it("asks to sign in first and keeps the token in the next link", async () => {
    stub({ signedIn: false });
    mount("/invite?token=abc");
    const card = (await screen.findByRole("heading", { name: "You were invited" })).closest(".card") as HTMLElement;
    const href = within(card).getByRole("link", { name: "Sign in" }).getAttribute("href") ?? "";
    expect(href).toMatch(/^\/login\?/);
    expect(new URL(href, "http://x").searchParams.get("next")).toBe("/invite?token=abc");
  });
  it("accepts with the signed-in account and lands on the hives; a stale token is explained", async () => {
    const state = stub();
    const { user, router } = mount("/invite?token=abc");
    await user.click(await screen.findByRole("button", { name: "Accept invitation" }));
    await waitFor(() => expect(state.accepted).toBe("abc"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
    cleanup();
    const second = mount("/invite?token=stale");
    await second.user.click(await screen.findByRole("button", { name: "Accept invitation" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The invitation is invalid or expired.");
  });
});

describe("expiring and expired grants (#85)", () => {
  const expiring: GrantRow = { id: "g_1", member_id: "m_1", member_email: "friend@example.de", camera_id: "cam_1", role: "viewer", created_at: "", state: "expiring", expires_at: inDays(2) };
  const expired: GrantRow = { id: "g_3", member_id: "m_1", member_email: "friend@example.de", camera_id: "cam_2", role: "operator", created_at: "", state: "expired", expires_at: inDays(-1) };
  const permanent: GrantRow = { id: "g_4", member_id: "m_1", member_email: "friend@example.de", camera_id: "*", role: "viewer", created_at: "", state: "active" };

  it("marks expiring and expired grants for the owner, who extends one to a new date and makes the expired one permanent", async () => {
    const state = stub({ grants: [expiring, expired, permanent] });
    const { user } = mount("/hives/hv_1/sharing");
    await waitFor(() => expect(screen.getAllByTestId("grant")).toHaveLength(3));
    const [soon, gone, open] = screen.getAllByTestId("grant") as [HTMLElement, HTMLElement, HTMLElement];
    expect(soon).toHaveTextContent("ends soon");
    expect(gone).toHaveTextContent(/· ended /);
    expect(gone).not.toHaveTextContent(/until|ends soon/);
    expect(open).not.toHaveTextContent(/ends soon|ended/);
    expect(within(open).queryByRole("button", { name: /permanent/ })).toBeNull();
    expect(await within(gone).findByRole("button", { name: "Extend the share of Garden with friend@example.de" })).toHaveTextContent("Renew");

    const toggle = await within(soon).findByRole("button", { name: "Extend the share of Door with friend@example.de" });
    expect(toggle).toHaveAttribute("aria-expanded", "false");
    await user.click(toggle);
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    expect(within(soon).getByLabelText("New end").getAttribute("min")).toMatch(/^\d{4}-\d\d-\d\dT\d\d:\d\d$/);
    await user.type(within(soon).getByLabelText("New end"), "2026-12-24T18:00");
    await user.click(within(soon).getByRole("button", { name: "Save new end" }));
    await waitFor(() => expect(state.bodies.at(-1)).toEqual({ member_id: "m_1", camera_id: "cam_1", role: "viewer", expires_at: new Date("2026-12-24T18:00").toISOString() }));

    await user.click(within(gone).getByRole("button", { name: "Make the share of Garden with friend@example.de permanent" }));
    await waitFor(() => expect(state.bodies.at(-1)).toEqual({ member_id: "m_1", camera_id: "cam_2", role: "operator" }));
    expect(state.calls.filter((c) => c === "PUT /v1/hives/hv_1/grants")).toHaveLength(2);
  });

  it("explains an end the cloud refuses", async () => {
    stub({ grants: [expiring], grantError: "grant_end_past" });
    const { user } = mount("/hives/hv_1/sharing");
    const row = await screen.findByTestId("grant");
    await user.click(await within(row).findByRole("button", { name: "Extend the share of Door with friend@example.de" }));
    await user.type(within(row).getByLabelText("New end"), "2026-12-24T18:00");
    await user.click(within(row).getByRole("button", { name: "Save new end" }));
    expect(await within(row).findByRole("alert")).toHaveTextContent("The end must lie in the future.");
  });

  const notice = (side: "owner" | "member", st: "expiring" | "expired", over: Record<string, string> = {}) => ({
    grant_id: `g_${side}_${st}`, side, hive_id: "hv_1", hive_name: "Home", camera_id: "cam_1", camera_name: "Door", role: "viewer",
    peer: side === "owner" ? "friend@example.de" : "anna@example.de", expires_at: st === "expiring" ? inDays(2) : inDays(-1), state: st, ...over,
  });

  it("shows the owner one notice per expiring or expired grant, leading to the hive's sharing page", async () => {
    stub({ notices: [notice("owner", "expiring"), notice("owner", "expired", { camera_id: "*", camera_name: "" })] });
    const { user, router } = mount("/");
    await waitFor(() => expect(screen.getAllByTestId("grant-notice")).toHaveLength(2));
    const [soon, gone] = screen.getAllByTestId("grant-notice") as [HTMLElement, HTMLElement];
    expect(soon).toHaveTextContent("Your share of Door on Home with friend@example.de ends on");
    expect(gone).toHaveTextContent("Your share of all cameras on Home with friend@example.de ended on");
    await user.click(within(soon).getByRole("link", { name: "Extend" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/hives/hv_1/sharing"));
  });

  it("shows the member their expiring and expired access, with no way to extend it themselves", async () => {
    stub({ notices: [notice("member", "expiring"), notice("member", "expired")] });
    mount("/");
    await waitFor(() => expect(screen.getAllByTestId("grant-notice")).toHaveLength(2));
    const [soon, gone] = screen.getAllByTestId("grant-notice") as [HTMLElement, HTMLElement];
    expect(soon).toHaveTextContent("Your access to Door on Home, shared by anna@example.de, ends on");
    expect(soon).toHaveTextContent("Ask them to extend it.");
    expect(gone).toHaveTextContent("Your access to Door on Home, shared by anna@example.de, ended on");
    expect(screen.queryByRole("link", { name: "Extend" })).toBeNull();
  });

  it("shows no notices when nothing ends soon", async () => {
    stub();
    mount("/");
    expect(await screen.findByTestId("me")).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByTestId("grant-notice")).toBeNull();
  });
});
