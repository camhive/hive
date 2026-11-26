import { useEffect, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import type { Hive } from "@/lib/hives";
import { parseLanAddress, transport } from "@/lib/transport";
import { Field } from "@/components/Field";

/**
 * ADR-0023: Camhive cannot reach the hive and its LAN name does not answer
 * (no DNS without the internet). The browser can still reach the hive at its
 * LAN address once the user proceeded through the certificate warning, which
 * must be a name mismatch for exactly this hive's LAN name; the step quotes
 * that warning per browser and says that any other one means stop. It links to
 * the remembered address, says what the warning must say, takes a new
 * address after a DHCP change and probes again when the user comes back.
 * `bare` drops the card and its section heading level, for a row in the
 * settings page's Connection group.
 */
export function LanAddressStep({ hive, probing, reprobe, bare }: { hive: Hive; probing: boolean; reprobe: () => Promise<void>; bare?: boolean }) {
  const { t } = useTranslation();
  const port = hive.lan?.port ?? 0;
  const [input, setInput] = useState(() => (transport.addresses(hive)[0] ?? "").replace(new RegExp(`:${port}$`), ""));
  const [error, setError] = useState<string | null>(null);
  const [tried, setTried] = useState(false);
  const address = parseLanAddress(input, port);
  const name = hive.lan?.host ?? "";
  const host = (address ?? "").replace(/:\d+$/, ""); // how the browser names the address in its warning
  const Heading = bare ? "h4" : "h2"; // bare sits under a settings row's h3

  useEffect(() => {
    const back = () => { if (document.visibilityState === "visible") void reprobe(); };
    document.addEventListener("visibilitychange", back);
    return () => document.removeEventListener("visibilitychange", back);
  }, [reprobe]);

  async function retry(e: FormEvent) {
    e.preventDefault();
    setError(null);
    if (!transport.setAddress(hive, input)) {
      setError(t("lanAddress.invalid"));
      return;
    }
    await reprobe();
    setTried(true);
  }

  return (
    <form className={bare ? "" : "card max-w-xl"} onSubmit={(e) => void retry(e)} aria-labelledby="lan-address-title" data-testid="lan-address">
      <Heading id="lan-address-title" className={bare ? "font-medium mb-1" : "text-lg font-semibold mb-1"}>{t("lanAddress.title")}</Heading>
      <p className="muted text-sm mb-3">{t("lanAddress.intro")}</p>
      <ol className="list-decimal pl-5 space-y-2 text-sm mb-4">
        <li>
          {t("lanAddress.open", { name })}
          <ul className="list-disc pl-5 mt-1 space-y-1" data-testid="lan-address-checks">
            {(["chrome", "firefox", "safari"] as const).map((b) => <li key={b}>{t(`lanAddress.${b}`, { name, host })}</li>)}
          </ul>
          <strong className="block mt-1">{t("lanAddress.stop")}</strong>
        </li>
        <li>{t("lanAddress.back")}</li>
      </ol>
      <Field label={t("lanAddress.field")} name="lan-address" inputMode="decimal" autoComplete="off" hint={t("lanAddress.hint")} value={input} onChange={(e) => { setInput(e.target.value); setTried(false); }} />
      {address && (
        <p className="mb-4">
          <a className="link" href={`https://${address}/`} target="_blank" rel="noopener noreferrer">{t("lanAddress.link", { address })}</a>
        </p>
      )}
      {error && <p role="alert" className="error text-sm mb-3">{error}</p>}
      {tried && !probing && !error && <p role="status" className="muted text-sm mb-3">{t("lanAddress.stillNot")}</p>}
      <button className="btn" disabled={probing}>{t("lanAddress.retry")}</button>
    </form>
  );
}
