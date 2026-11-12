// Camhive cloud API client. Types come from the OpenAPI document
// (src/schema.d.ts, generated — do not edit); this file adds what every
// caller needs: cookie credentials, the CSRF header on unsafe methods and
// a typed error.
import createClient, { type Middleware } from "openapi-fetch";
import type { paths, components } from "./schema.js";

export type { paths, components };
export type ApiError = components["schemas"]["Error"]["error"];
export type Entitlements = components["schemas"]["Entitlements"];
export type Hive = components["schemas"]["Hive"];
export type AccessGrant = components["schemas"]["AccessGrant"];
export type ICEServer = components["schemas"]["ICEServer"];
export type AuditEvent = components["schemas"]["AuditEvent"];

/** Header every non-GET request must carry (CSRF, see the API description). */
export const CSRF_HEADER = "X-Requested-With";
export const CSRF_VALUE = "camhive";

/** Thrown by `call` when the API answers with an error body. */
export class CamhiveError extends Error {
  readonly status: number;
  readonly code: string;
  constructor(status: number, body: { code?: string; message?: string } | undefined) {
    super(body?.message ?? `HTTP ${status}`);
    this.name = "CamhiveError";
    this.status = status;
    this.code = body?.code ?? "unknown";
  }
}

const csrf: Middleware = {
  onRequest({ request }) {
    if (request.method !== "GET" && request.method !== "HEAD") {
      request.headers.set(CSRF_HEADER, CSRF_VALUE);
    }
    return request;
  },
};

export interface ClientOptions {
  /** API origin, e.g. https://api.camhive.app */
  baseUrl: string;
  /** Admin bearer token (only for /admin/v1). */
  adminToken?: string;
  fetch?: typeof fetch;
}

/** Creates the typed client: `client.GET("/v1/me")`, `client.POST("/v1/auth/login", { body })`. */
export function createCamhiveClient(opts: ClientOptions) {
  const client = createClient<paths>({ baseUrl: opts.baseUrl, credentials: "include", fetch: opts.fetch });
  client.use(csrf);
  if (opts.adminToken) {
    client.use({
      onRequest({ request }) {
        request.headers.set("Authorization", `Bearer ${opts.adminToken}`);
        return request;
      },
    });
  }
  return client;
}

export type CamhiveClient = ReturnType<typeof createCamhiveClient>;

/** Unwraps an openapi-fetch result: returns data or throws CamhiveError. */
export function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.error !== undefined || !result.response.ok) {
    const body = (result.error as { error?: ApiError } | undefined)?.error;
    throw new CamhiveError(result.response.status, body);
  }
  return result.data as T;
}

// --- Hive local API (api/hive-openapi.yaml) ---------------------------------
import type { paths as hivePaths, components as hiveComponents } from "./hive-schema.js";

export type { hivePaths, hiveComponents };
export type HiveCamera = hiveComponents["schemas"]["Camera"];
export type CameraHealth = hiveComponents["schemas"]["CameraHealth"];
export type HiveApiError = hiveComponents["schemas"]["Error"];

export interface HiveClientOptions {
  /** Base URL of the hive: LAN name or relay (the transport module decides). */
  baseUrl: string;
  /** Access token from POST /v1/hives/{id}/access, sent as Bearer. */
  token: string | (() => string);
  fetch?: typeof fetch;
}

/** Typed client for the hive's own API; no cookies, bearer token only. */
export function createHiveClient(opts: HiveClientOptions) {
  const client = createClient<hivePaths>({ baseUrl: opts.baseUrl, credentials: "omit", fetch: opts.fetch });
  client.use({
    onRequest({ request }) {
      const tok = typeof opts.token === "function" ? opts.token() : opts.token;
      request.headers.set("Authorization", `Bearer ${tok}`);
      return request;
    },
  });
  return client;
}

export type HiveClient = ReturnType<typeof createHiveClient>;

/** Unwraps a hive API result: data or HiveError with the hive's stable code. */
export function unwrapHive<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.error !== undefined || !result.response.ok) {
    const body = result.error as HiveApiError | undefined;
    throw new CamhiveError(result.response.status, body ? { code: body.code, message: body.message } : undefined);
  }
  return result.data as T;
}
