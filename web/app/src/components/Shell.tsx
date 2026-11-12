import { Link, Outlet } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import { applyTheme, readTheme, type Theme } from "@/lib/theme";
import { useMe, useSignOut } from "@/lib/session";
import { useCloudReachable } from "@/lib/cloud";
import { Logo } from "@/components/Logo";

export function Shell() {
  const { t, i18n } = useTranslation();
  const me = useMe();
  const signOut = useSignOut();
  const theme = readTheme();
  const reachable = useCloudReachable();
  return (
    <div className="min-h-screen flex flex-col">
      <header className="sticky top-0 z-30 border-b backdrop-blur" style={{ borderColor: "var(--border)", background: "var(--header)" }}>
        <div className="mx-auto max-w-6xl flex flex-wrap items-center gap-x-6 gap-y-1 px-4 py-2.5">
          <Link to="/" aria-label={t("app.name")} className="rounded-lg"><Logo size={26} /></Link>
          {me.data && (
            <nav className="order-last w-full -mx-2 flex items-center gap-1 sm:order-none sm:w-auto sm:mx-0" aria-label={t("nav.main")}>
              <Link to="/" className="nav-link" activeOptions={{ exact: true }} activeProps={{ "aria-current": "page" }}>{t("hives.title")}</Link>
              <Link to="/members" className="nav-link" activeProps={{ "aria-current": "page" }}>{t("nav.members")}</Link>
              <Link to="/account" className="nav-link" activeProps={{ "aria-current": "page" }}>{t("nav.account")}</Link>
            </nav>
          )}
          <div className="ml-auto flex items-center gap-2 text-sm">
            <select aria-label={t("home.locale")} className="input input-sm !w-auto" value={i18n.resolvedLanguage} onChange={(e) => void i18n.changeLanguage(e.target.value)}>
              <option value="en">EN</option>
              <option value="de">DE</option>
            </select>
            <select aria-label={t("nav.theme")} className="input input-sm !w-auto" defaultValue={theme} onChange={(e) => applyTheme(e.target.value as Theme)}>
              <option value="system">Auto</option>
              <option value="light">Light</option>
              <option value="dark">Dark</option>
            </select>
            {me.data ? (
              <button className="btn btn-secondary btn-sm" onClick={() => void signOut()}>{t("nav.signOut")}</button>
            ) : (
              <>
                <Link to="/login" className="nav-link">{t("nav.signIn")}</Link>
                <Link to="/register" className="btn btn-sm">{t("nav.signUp")}</Link>
              </>
            )}
          </div>
        </div>
      </header>
      {!reachable && (
        <p role="status" className="px-4 py-2.5 text-sm text-center font-medium" style={{ background: "var(--accent-soft)", color: "var(--accent-text)", borderBottom: "1px solid var(--border)" }} data-testid="cloud-down">
          {t("app.cloudDown")}
        </p>
      )}
      <main className="w-full max-w-lg mx-auto px-4 py-8 sm:py-10 flex-1 has-[.max-w-none]:max-w-6xl">
        <Outlet />
      </main>
      <footer className="mx-auto max-w-6xl w-full px-4 py-6 text-xs muted flex items-center justify-between gap-4">
        <span>© Camhive</span>
      </footer>
    </div>
  );
}
