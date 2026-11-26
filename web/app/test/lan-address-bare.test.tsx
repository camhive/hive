import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { LanAddressStep } from "../src/components/LanAddress";
import type { Hive } from "../src/lib/hives";

const hive = { id: "hv_1", name: "Home", status: "active", online: false, claimed_at: "", lan: { host: "hv_1.h.camhive.app", port: 16017, addrs: ["192.168.1.20"] } } as Hive;

describe("the manual LAN address step (ADR-0023) inside a settings row (#174)", () => {
  it("is a card with its own section heading on the cameras page, one level below the row title in Settings", () => {
    const { unmount } = render(<LanAddressStep hive={hive} probing={false} reprobe={async () => {}} />);
    expect(screen.getByRole("heading", { level: 2 })).toHaveTextContent("Open your hive by its address");
    unmount();
    render(<LanAddressStep hive={hive} probing={false} reprobe={async () => {}} bare />);
    expect(screen.getByRole("heading", { level: 4 })).toHaveTextContent("Open your hive by its address");
    expect(screen.queryByRole("heading", { level: 2 })).toBeNull();
  });
});
