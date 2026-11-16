import { afterEach, describe, expect, it } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { hiveCapability } from "../src/lib/capabilities";
import { NeedsUpdate } from "../src/components/NeedsUpdate";
import type { Hive } from "../src/lib/hives";

afterEach(cleanup);

describe("hive capabilities (ADR-0022)", () => {
  it("asks the hive's own list when it reports one, whatever its version", () => {
    expect(hiveCapability({ version: "0.1.6", capabilities: ["archive.delete", "motion"] }, "archive.delete")).toEqual({ has: true });
    expect(hiveCapability({ version: "9.9.9", capabilities: ["motion"] }, "archive.delete")).toEqual({ has: false, since: "0.1.3" });
    expect(hiveCapability({ version: "dev", capabilities: ["local_login"] }, "local_login")).toEqual({ has: true });
  });

  it("gives a hive without a list what shipped in its version, never more than the v0.1.6 baseline", () => {
    expect(hiveCapability({ version: "0.1.5" }, "archive.delete")).toEqual({ has: true });
    expect(hiveCapability({ version: "v0.1.4" }, "camera.identity")).toEqual({ has: true });
    expect(hiveCapability({ version: "0.1.2" }, "archive.delete")).toEqual({ has: false, since: "0.1.3" });
    expect(hiveCapability({ version: "0.1.5" }, "local_login")).toEqual({ has: false, since: "0.1.6" });
    // v0.1.6, the last release without a list, has local login and motion.
    expect(hiveCapability({ version: "0.1.6" }, "local_login")).toEqual({ has: true });
    expect(hiveCapability({ version: "0.1.6" }, "motion")).toEqual({ has: true });
    // Update settings ship with the list (v0.1.7): a hive without a list never has them.
    expect(hiveCapability({ version: "0.1.6" }, "updates")).toEqual({ has: false, since: "0.1.7" });
    expect(hiveCapability({ version: "0.2.0" }, "updates")).toEqual({ has: false, since: "0.1.7" });
    // An unknown version without a list is the baseline.
    expect(hiveCapability({ version: "dev" }, "archive.delete")).toEqual({ has: true });
    expect(hiveCapability({}, "motion")).toEqual({ has: true });
  });
});

describe("the needs-an-update notice", () => {
  it("tells the owner which version brings the feature and that the hive must be updated", () => {
    render(<NeedsUpdate hive={{ version: "0.1.2" } as Hive} since="0.1.3" owner />);
    const note = screen.getByRole("note");
    expect(note).toHaveTextContent("This needs hive version 0.1.3 or later; this hive runs 0.1.2.");
    expect(note).toHaveTextContent("Update the hive");
    expect(note).not.toHaveTextContent("Ask the owner");
  });

  it("tells a member to ask the owner", () => {
    render(<NeedsUpdate hive={{ version: "0.1.2" } as Hive} since="0.1.3" owner={false} />);
    expect(screen.getByRole("note")).toHaveTextContent("Ask the hive's owner to update it.");
  });
});
