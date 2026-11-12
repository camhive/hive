import { describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";

vi.mock("qrcode", () => ({ default: { toDataURL: async () => "data:image/png;base64,AAAA" } }));

describe("account page", () => {
  it("enrols TOTP, shows recovery codes once, lists and revokes sessions", async () => {
    let totp = false;
    const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
    const calls: string[] = [];
    vi.stubGlobal("fetch", (async (input: RequestInfo | URL, init?: RequestInit) => {
      const req = new Request(input, init);
      const key = `${req.method} ${new URL(req.url).pathname}`;
      calls.push(key);
      switch (key) {
        case "GET /v1/me": return json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: totp, created_at: "2026-11-09T00:00:00Z" });
        case "POST /v1/me/totp/enrol": return json(200, { secret: "JBSWY3DPEHPK3PXP", uri: "otpauth://totp/Camhive:anna@example.de?secret=JBSWY3DPEHPK3PXP" });
        case "POST /v1/me/totp/confirm": {
          const body = (await req.json()) as { code: string };
          if (body.code !== "123456") return json(400, { error: { code: "bad_code", message: "" } });
          totp = true;
          return json(200, { status: "totp_enabled", recovery_codes: ["aaaa-bbbb-cccc", "dddd-eeee-ffff"] });
        }
        case "GET /v1/me/sessions": return json(200, { sessions: [
          { id: "wss_1", current: true, created_at: "2026-11-09T00:00:00Z", last_seen_at: "2026-11-09T01:00:00Z", user_agent: "Firefox" },
          { id: "wss_2", current: false, created_at: "2026-11-08T00:00:00Z", last_seen_at: "2026-11-08T01:00:00Z", user_agent: "Phone" }] });
        case "DELETE /v1/me/sessions/wss_2": return new Response(null, { status: 204 });
        default: return json(404, { error: { code: "not_found", message: "" } });
      }
    }) as typeof fetch);
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/account"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
    const user = userEvent.setup();

    await user.click(await screen.findByRole("button", { name: "Enable two-factor" }));
    expect(await screen.findByTestId("secret")).toHaveTextContent("JBSWY3DPEHPK3PXP");
    expect(await screen.findByAltText("QR code for the authenticator app")).toBeInTheDocument();
    await user.type(screen.getByLabelText("Authenticator code"), "000000");
    await user.click(screen.getByRole("button", { name: "Confirm two-factor" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The code is wrong or expired.");
    await user.clear(screen.getByLabelText("Authenticator code"));
    await user.type(screen.getByLabelText("Authenticator code"), "123456");
    await user.click(screen.getByRole("button", { name: "Confirm two-factor" }));
    const recovery = await screen.findByTestId("recovery");
    expect(recovery).toHaveTextContent("aaaa-bbbb-cccc");
    expect(screen.getByRole("button", { name: "Download codes" })).toBeInTheDocument();

    expect(await screen.findByText("Phone")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Revoke" }));
    await waitFor(() => expect(calls).toContain("DELETE /v1/me/sessions/wss_2"));
  });
});
