import { describe, expect, it } from "vitest";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";

// #70: icon files carry a version in their names so Android picks up a new
// logo. Renaming them must not leave a reference to a file that is gone.
const app = (p: string) => join(process.cwd(), p); // vitest runs in web/app

describe("app icons", () => {
  it("every icon the manifest, index.html and the push worker name exists, versioned", () => {
    const refs = ["vite.config.ts", "index.html", "public/push-sw.js"].flatMap((f) =>
      [...readFileSync(app(f), "utf8").matchAll(/icons\/([\w.-]+\.(?:png|svg))/g)].map((m) => ({ file: f, icon: m[1]! })));
    expect(refs.length).toBeGreaterThanOrEqual(8);
    for (const r of refs) {
      expect(r.icon, `${r.file}: ${r.icon}`).toMatch(/\.v\d+\.(png|svg)$/);
      expect(existsSync(app(`public/icons/${r.icon}`)), `${r.file}: ${r.icon} missing`).toBe(true);
    }
  });
});
