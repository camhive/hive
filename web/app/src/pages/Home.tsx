import { useTranslation } from "react-i18next";
import { Link } from "@tanstack/react-router";
import { useMe } from "@/lib/session";
import { useHives, type Hive } from "@/lib/hives";
import { LogoMark } from "@/components/Logo";
import { HiveStatus } from "@/components/HiveStatus";

export function Home() {
  const me = useMe();
  if (me.isPending) return <p className="muted">…</p>;
  if (!me.data) return <Landing />;
  return <Hives email={me.data.email} />;
}

function Landing() {
  const { t } = useTranslation();
  const features = [
    { title: t("home.f1Title"), text: t("home.f1Text") },
    { title: t("home.f2Title"), text: t("home.f2Text") },
    { title: t("home.f3Title"), text: t("home.f3Text") },
  ];
  return (
    <div className="max-w-none">
      <section className="max-w-2xl pt-6 sm:pt-14">
        <LogoMark size={56} className="mb-8" />
        <p className="eyebrow mb-3">{t("home.eyebrow")}</p>
        <h1 className="text-4xl sm:text-5xl font-semibold leading-[1.08]" style={{ letterSpacing: "-0.03em" }}>
          {t("home.heroLead")}<br />
          {t("home.heroAccent")}
        </h1>
        <p className="sr-only">{t("app.tagline")}</p>
        <p className="muted mt-5 text-lg leading-relaxed">{t("home.heroText")}</p>
        <div className="mt-8 flex flex-wrap items-center gap-x-4 gap-y-2">
          <Link to="/register" className="btn !min-h-11 !px-5 !text-[.9375rem]">{t("nav.signUp")}</Link>
          <span className="text-sm muted">{t("auth.registerIntro")}</span>
        </div>
      </section>

      <section className="mt-16 sm:mt-24 grid gap-x-10 gap-y-8 border-t pt-10 sm:grid-cols-3" style={{ borderColor: "var(--border)" }}>
        {features.map((f) => (
          <div key={f.title}>
            <h2 className="font-semibold mb-1.5">{f.title}</h2>
            <p className="muted text-sm leading-relaxed">{f.text}</p>
          </div>
        ))}
      </section>

      <p className="mt-12 text-sm muted">{t("home.privacy")}</p>
    </div>
  );
}

function Hives({ email }: { email: string }) {
  const { t } = useTranslation();
  const hives = useHives({ refetchInterval: 30_000 });
  const owned = hives.data?.hives ?? [];
  const shared = hives.data?.shared ?? [];
  return (
    <div className="space-y-8 max-w-none" data-testid="me">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <div>
          <h1 className="page-title">{t("hives.title")}</h1>
          <p className="muted text-sm mt-1">{t("home.welcome", { email })}</p>
        </div>
        <Link to="/hives/add" className="btn">{t("hives.add")}</Link>
      </div>
      {hives.isPending && <p className="muted">…</p>}
      {hives.isError && <p role="alert" className="card error">{t("errors.network")}</p>}
      {hives.data && owned.length === 0 && shared.length === 0 && (
        <div className="card max-w-xl">
          <p className="muted">{t("hives.empty")}</p>
        </div>
      )}
      {owned.length > 0 && <HiveList hives={owned} />}
      {shared.length > 0 && (
        <section className="space-y-3">
          <h2 className="eyebrow">{t("hives.shared")}</h2>
          <HiveList hives={shared} />
        </section>
      )}
    </div>
  );
}

function HiveList({ hives }: { hives: Hive[] }) {
  const { t } = useTranslation();
  return (
    <ul className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3" aria-label={t("hives.title")}>
      {hives.map((h) => (
        <li key={h.id} className="card card-hover !p-5" data-testid="hive">
          <div className="flex items-start justify-between gap-3">
            <div className="min-w-0">
              <p className="font-semibold truncate"><Link to="/hives/$id" params={{ id: h.id }} className="hover:underline underline-offset-4">{h.name}</Link></p>
              <p className="text-xs muted truncate mt-0.5">{h.id}{h.version ? ` · v${h.version}` : ""}</p>
            </div>
            <HiveStatus hive={h} />
          </div>
          <div className="mt-5 flex items-center justify-between gap-3 text-sm">
            <Link to="/hives/$id" params={{ id: h.id }} className="link" aria-label={`${t("home.openLive")}: ${h.name}`}>{t("home.openLive")}</Link>
            <Link to="/hives/$id/settings" params={{ id: h.id }} className="muted hover:underline underline-offset-4" aria-label={t("hives.settingsFor", { name: h.name })}>{t("hives.settings")}</Link>
          </div>
        </li>
      ))}
    </ul>
  );
}
