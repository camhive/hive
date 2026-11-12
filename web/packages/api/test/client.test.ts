import { test } from "node:test";
import assert from "node:assert/strict";
import { createCamhiveClient, createHiveClient, unwrap, unwrapHive, CamhiveError, CSRF_HEADER, CSRF_VALUE } from "../src/index.ts";

test("adds the CSRF header to unsafe requests and sends cookies", async () => {
  const seen: Request[] = [];
  const fakeFetch: typeof fetch = async (input, init) => {
    const req = new Request(input, init);
    seen.push(req);
    return new Response(JSON.stringify({ status: "signed_in", session_id: "wss_1" }), { status: 200, headers: { "Content-Type": "application/json" } });
  };
  const client = createCamhiveClient({ baseUrl: "https://api.test", fetch: fakeFetch });
  const res = await client.POST("/v1/auth/login", { body: { email: "a@b.de", password: "long enough password" } });
  assert.equal(unwrap(res).status, "signed_in");
  assert.equal(seen[0].headers.get(CSRF_HEADER), CSRF_VALUE);
  assert.equal(seen[0].credentials, "include");
  await client.GET("/v1/me");
  assert.equal(seen[1].headers.get(CSRF_HEADER), null);
});

test("unwrap throws a typed error with the stable code", async () => {
  const fakeFetch: typeof fetch = async () =>
    new Response(JSON.stringify({ error: { code: "invalid_credentials", message: "invalid email or password" } }), { status: 401, headers: { "Content-Type": "application/json" } });
  const client = createCamhiveClient({ baseUrl: "https://api.test", fetch: fakeFetch });
  const res = await client.POST("/v1/auth/login", { body: { email: "a@b.de", password: "x" } });
  assert.throws(() => unwrap(res), (e: unknown) => e instanceof CamhiveError && e.status === 401 && e.code === "invalid_credentials");
});

test("admin token travels as a bearer", async () => {
  let auth = "";
  const fakeFetch: typeof fetch = async (input, init) => {
    auth = new Request(input, init).headers.get("Authorization") ?? "";
    return new Response(JSON.stringify({ plans: [] }), { status: 200, headers: { "Content-Type": "application/json" } });
  };
  const client = createCamhiveClient({ baseUrl: "https://api.test", adminToken: "tok", fetch: fakeFetch });
  await client.GET("/admin/v1/plans");
  assert.equal(auth, "Bearer tok");
});

test("hive client sends the bearer token without cookies and maps hive error codes", async () => {
  const seen: Request[] = [];
  const fakeFetch: typeof fetch = async (input, init) => {
    const req = new Request(input, init);
    seen.push(req);
    if (req.url.endsWith("/v0/cameras")) return new Response("[]", { status: 200, headers: { "Content-Type": "application/json" } });
    return new Response(JSON.stringify({ code: "token_expired", message: "access token rejected" }), { status: 401, headers: { "Content-Type": "application/json" } });
  };
  let token = "t1";
  const hive = createHiveClient({ baseUrl: "https://hv.test", token: () => token, fetch: fakeFetch });
  assert.deepEqual(unwrapHive(await hive.GET("/v0/cameras")), []);
  assert.equal(seen[0].headers.get("Authorization"), "Bearer t1");
  assert.equal(seen[0].credentials, "omit");
  token = "t2";
  const res = await hive.GET("/v0/health/cameras");
  assert.equal(seen[1].headers.get("Authorization"), "Bearer t2");
  assert.throws(() => unwrapHive(res), (e: unknown) => e instanceof CamhiveError && e.status === 401 && e.code === "token_expired");
});
