import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryHistory } from "@tanstack/react-router";
import { QueryClient } from "@tanstack/react-query";
import { App } from "../src/App";
import { makeRouter } from "../src/router";

// A fake cloud: register → confirm → login → /me, with the real error shape.
function fakeApi() {
  let signedIn = false;
  const calls: string[] = [];
  const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
  const fetchImpl: typeof fetch = async (input, init) => {
    const req = new Request(input, init);
    const url = new URL(req.url);
    calls.push(`${req.method} ${url.pathname}`);
    if (req.method !== "GET" && req.headers.get("X-Requested-With") !== "camhive") return json(403, { error: { code: "csrf", message: "" } });
    switch (`${req.method} ${url.pathname}`) {
      case "POST /v1/auth/register": return json(202, { status: "confirmation_sent" });
      case "POST /v1/auth/confirm": {
        const body = (await req.json()) as { code: string };
        return body.code === "123456" ? json(200, { status: "verified" }) : json(400, { error: { code: "bad_code", message: "" } });
      }
      case "POST /v1/auth/login": {
        const body = (await req.json()) as { password: string };
        if (body.password !== "correct horse battery") return json(401, { error: { code: "invalid_credentials", message: "" } });
        signedIn = true;
        return json(200, { status: "signed_in", session_id: "wss_1" });
      }
      case "GET /v1/me": return signedIn ? json(200, { id: "acc_1", email: "anna@example.de", locale: "en", totp_enabled: false, created_at: "2026-11-09T00:00:00Z" }) : json(401, { error: { code: "unauthenticated", message: "" } });
      default: return json(404, { error: { code: "not_found", message: "" } });
    }
  };
  return { fetchImpl, calls };
}

describe("sign-up → confirm → login → /me", () => {
  beforeEach(() => { vi.restoreAllMocks(); });

  it("walks the whole flow against the API shapes, showing stable errors on the way", async () => {
    const fake = fakeApi();
    vi.stubGlobal("fetch", fake.fetchImpl);
    const router = makeRouter(createMemoryHistory({ initialEntries: ["/register"] }));
    render(<App router={router} queryClient={new QueryClient({ defaultOptions: { queries: { retry: false } } })} />);
    const user = userEvent.setup();

    await user.type(await screen.findByLabelText("E-mail"), "anna@example.de");
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Create account" }));
    expect(await screen.findByText(/sent a 6-digit code to anna@example.de/)).toBeInTheDocument();

    await user.type(screen.getByLabelText("Confirmation code"), "000000");
    await user.click(screen.getByRole("button", { name: "Confirm e-mail" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("The code is wrong or expired.");
    await user.clear(screen.getByLabelText("Confirmation code"));
    await user.type(screen.getByLabelText("Confirmation code"), "123456");
    await user.click(screen.getByRole("button", { name: "Confirm e-mail" }));
    expect(await screen.findByText("E-mail confirmed. Sign in now.")).toBeInTheDocument();

    await user.type(screen.getByLabelText("E-mail"), "anna@example.de");
    await user.type(screen.getByLabelText("Password"), "wrong password");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Wrong e-mail or password.");
    await user.clear(screen.getByLabelText("Password"));
    await user.type(screen.getByLabelText("Password"), "correct horse battery");
    await user.click(screen.getByRole("button", { name: "Sign in" }));

    await waitFor(() => expect(screen.getByTestId("me")).toHaveTextContent("Signed in as anna@example.de"));
    expect(fake.calls).toContain("POST /v1/auth/register");
    expect(fake.calls).toContain("GET /v1/me");
  });
});
