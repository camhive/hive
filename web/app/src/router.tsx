import { createRootRoute, createRoute, createRouter, redirect } from "@tanstack/react-router";
import { Shell } from "@/components/Shell";
import { Home } from "@/pages/Home";
import { Register } from "@/pages/Register";
import { Confirm } from "@/pages/Confirm";
import { Login } from "@/pages/Login";
import { Forgot } from "@/pages/Forgot";
import { Reset } from "@/pages/Reset";
import { Account } from "@/pages/Account";
import { AddHive } from "@/pages/AddHive";
import { HiveSettings } from "@/pages/HiveSettings";
import { HivePage, type HiveSearch } from "@/pages/Hive";
import { Members } from "@/pages/Members";
import { HiveSharing } from "@/pages/HiveSharing";
import { Invite } from "@/pages/Invite";
import { Plan } from "@/pages/Plan";
import { Activity } from "@/pages/Activity";

const root = createRootRoute({ component: Shell });
const home = createRoute({ getParentRoute: () => root, path: "/", component: Home });
const register = createRoute({
  getParentRoute: () => root,
  path: "/register",
  component: Register,
  validateSearch: (s: Record<string, unknown>): { next?: string } => (typeof s.next === "string" && s.next.startsWith("/") ? { next: s.next } : {}),
});
const confirm = createRoute({
  getParentRoute: () => root,
  path: "/confirm",
  component: Confirm,
  validateSearch: (s: Record<string, unknown>): { email?: string } => (typeof s.email === "string" ? { email: s.email } : {}),
});
const login = createRoute({
  getParentRoute: () => root,
  path: "/login",
  component: Login,
  validateSearch: (s: Record<string, unknown>): { confirmed?: string; next?: string } => ({
    ...(typeof s.confirmed === "string" ? { confirmed: s.confirmed } : {}),
    ...(typeof s.next === "string" && s.next.startsWith("/") ? { next: s.next } : {}),
  }),
});

const forgot = createRoute({ getParentRoute: () => root, path: "/forgot", component: Forgot });
const reset = createRoute({
  getParentRoute: () => root,
  path: "/reset",
  component: Reset,
  validateSearch: (s: Record<string, unknown>): { token?: string } => (typeof s.token === "string" ? { token: s.token } : {}),
});
const account = createRoute({ getParentRoute: () => root, path: "/account", component: Account });
const addHive = createRoute({ getParentRoute: () => root, path: "/hives/add", component: AddHive });
const hiveSettings = createRoute({ getParentRoute: () => root, path: "/hives/$id/settings", component: HiveSettings });
const hiveSearch = (s: Record<string, unknown>): HiveSearch => ({
  ...(typeof s.camera === "string" ? { camera: s.camera } : {}),
  ...(typeof s.day === "string" && /^\d{4}-\d{2}-\d{2}$/.test(s.day) ? { day: s.day } : {}),
});
const hivePage = createRoute({ getParentRoute: () => root, path: "/hives/$id", component: HivePage, validateSearch: hiveSearch });
// The live, archive and cameras pages became one hive page; old links and bookmarks land there.
const oldHivePage = (path: "/hives/$id/live" | "/hives/$id/archive" | "/hives/$id/cameras") => createRoute({
  getParentRoute: () => root,
  path,
  validateSearch: hiveSearch,
  beforeLoad: ({ params, search }) => { throw redirect({ to: "/hives/$id", params: { id: params.id }, search, replace: true }); },
});
const live = oldHivePage("/hives/$id/live");
const archive = oldHivePage("/hives/$id/archive");
const camerasPage = oldHivePage("/hives/$id/cameras");
const membersPage = createRoute({ getParentRoute: () => root, path: "/members", component: Members });
const planPage = createRoute({ getParentRoute: () => root, path: "/plan", component: Plan });
const activityPage = createRoute({ getParentRoute: () => root, path: "/activity", component: Activity });
const hiveSharing = createRoute({ getParentRoute: () => root, path: "/hives/$id/sharing", component: HiveSharing });
const invite = createRoute({
  getParentRoute: () => root,
  path: "/invite",
  component: Invite,
  validateSearch: (s: Record<string, unknown>): { token?: string } => (typeof s.token === "string" ? { token: s.token } : {}),
});

export const routeTree = root.addChildren([home, register, confirm, login, forgot, reset, account, addHive, hiveSettings, hivePage, live, archive, camerasPage, membersPage, hiveSharing, invite, planPage, activityPage]);

export function makeRouter(history?: Parameters<typeof createRouter>[0]["history"]) {
  return createRouter({ routeTree, history });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof makeRouter>;
  }
}
