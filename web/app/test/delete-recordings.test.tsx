import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createHiveClient, type HiveCamera } from "@camhive/api";
import { DeleteRecordings } from "../src/components/DeleteRecordings";
import { dayStart } from "../src/lib/archive";

afterEach(cleanup);

const json = (status: number, body: unknown) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const cameras = [{ id: "cam_1", name: "Door" }, { id: "cam_2", name: "Yard" }] as HiveCamera[];
const plan = {
  cameras: [{ camera: "cam_1", from: "2026-11-09T09:00:00Z", to: "2026-11-09T11:10:00Z", segments: 4, bytes: 2_500_000_000, pending: 1 }],
  exports: { count: 1, bytes: 1_000_000 },
};

function mount(opts: { answer?: (body: Record<string, unknown>) => Response; preset?: { camera: string; from: Date; to: Date } } = {}) {
  const bodies: Array<Record<string, unknown>> = [];
  const fetch = vi.fn(async (req: Request) => {
    const body = (await req.json()) as Record<string, unknown>;
    expect(`${req.method} ${new URL(req.url).pathname}`).toBe("POST /v0/archive/deletions");
    bodies.push(body);
    return opts.answer?.(body) ?? json(200, plan);
  });
  const client = createHiveClient({ baseUrl: "https://hive.test", token: "tok", fetch: fetch as unknown as typeof globalThis.fetch });
  const onDone = vi.fn();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <DeleteRecordings client={client} cameras={cameras} hiveName="Home" preset={opts.preset} onDone={onDone} />
    </QueryClientProvider>,
  );
  return { bodies, onDone, user: userEvent.setup() };
}

describe("deleting recordings (ADR-0016)", () => {
  it("previews a time range on chosen cameras with exports, then deletes exactly that", async () => {
    const { bodies, user } = mount();
    const preview = screen.getByRole("button", { name: "Preview" });
    expect(preview).toBeDisabled(); // no camera yet
    await user.click(screen.getByRole("checkbox", { name: "Door" }));
    await user.click(screen.getByRole("radio", { name: "A time range" }));
    await user.type(screen.getByLabelText("From"), "2026-11-09T10:05");
    await user.type(screen.getByLabelText("To"), "2026-11-09T11:00");
    await user.click(screen.getByRole("checkbox", { name: /Also delete exports/ }));
    await user.click(preview);

    expect(bodies[0]).toEqual({ cameras: ["cam_1"], from: new Date("2026-11-09T10:05").toISOString(), to: new Date("2026-11-09T11:00").toISOString(), include_exports: true, dry_run: true });
    const summary = await screen.findByTestId("deletion-preview");
    const row = within(summary).getByTestId("deletion-row");
    expect(row).toHaveTextContent("Door");
    expect(row).toHaveTextContent("4 files");
    expect(row).toHaveTextContent("2.5 GB");
    expect(row).toHaveTextContent(new Date("2026-11-09T09:00:00Z").toLocaleString());
    expect(summary).toHaveTextContent("1 export");
    expect(summary).toHaveTextContent("cannot be undone");

    await user.click(screen.getByRole("button", { name: "Delete permanently" }));
    await waitFor(() => expect(bodies).toHaveLength(2));
    expect(bodies[1]).toEqual({ ...bodies[0], dry_run: false });
    expect(await screen.findByRole("status")).toHaveTextContent("Deleted 4 files (2.5 GB)");
    expect(screen.getByRole("status")).toHaveTextContent("1 file still being recorded");
  });

  it("asks for the hive's name before deleting everything on all cameras, and a changed choice needs a new preview", async () => {
    const { bodies, user } = mount();
    await user.click(screen.getByRole("checkbox", { name: /All cameras/ }));
    expect(screen.getByRole("checkbox", { name: "Door" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Preview" }));
    expect(bodies[0]).toEqual({ include_exports: false, dry_run: true });
    const del = await screen.findByRole("button", { name: "Delete permanently" });
    expect(del).toBeDisabled();
    await user.type(screen.getByLabelText(/Type Home to confirm/), "Home");
    expect(del).toBeEnabled();

    await user.click(screen.getByRole("radio", { name: "Older than" }));
    expect(screen.queryByTestId("deletion-preview")).toBeNull();
    await user.clear(screen.getByLabelText("Days"));
    await user.type(screen.getByLabelText("Days"), "30");
    const before = Date.now();
    await user.click(screen.getByRole("button", { name: "Preview" }));
    const to = Date.parse(bodies[1]!.to as string);
    expect(bodies[1]!.from).toBeUndefined();
    expect(Math.abs(to - (before - 30 * 86_400_000))).toBeLessThan(10_000);
  });

  it("starts from a camera's day and explains a running export", async () => {
    const day = dayStart("2026-11-09");
    const { bodies, user } = mount({ preset: { camera: "cam_1", from: day, to: dayStart("2026-11-10") }, answer: () => json(409, { code: "export_running", message: "" }) });
    expect(screen.getByRole("checkbox", { name: "Door" })).toBeChecked();
    expect(screen.getByRole("radio", { name: "A time range" })).toBeChecked();
    await user.click(screen.getByRole("button", { name: "Preview" }));
    expect(bodies[0]).toMatchObject({ cameras: ["cam_1"], from: day.toISOString(), to: dayStart("2026-11-10").toISOString() });
    expect(await screen.findByRole("alert")).toHaveTextContent("An export of this footage is still being made");
  });
});
