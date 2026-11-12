import { createCamhiveClient } from "@camhive/api";
import { reportCloud } from "./cloud";

/** API origin: VITE_API_URL in production, the page origin otherwise (Vite proxies /v1 in dev). */
export const API_URL: string = import.meta.env.VITE_API_URL ?? (typeof window !== "undefined" ? window.location.origin : "");

// fetch is resolved per call so tests can stub it after this module loaded.
export const api = createCamhiveClient({
  baseUrl: API_URL,
  fetch: async (input, init) => {
    try {
      const res = await globalThis.fetch(input, init);
      reportCloud(true);
      return res;
    } catch (err) {
      reportCloud(false);
      throw err;
    }
  },
});
