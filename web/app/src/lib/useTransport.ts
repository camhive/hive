import { useEffect, useState } from "react";
import type { Hive } from "./hives";
import { transport, type Decision, type Transport } from "./transport";

/** The HTTP path decision for a hive, probed on mount and re-probed on request. */
export function useTransport(hive: Hive | undefined, t: Transport = transport) {
  const [decision, setDecision] = useState<Decision | null>(hive ? t.remembered(hive.id) : null);
  const [probing, setProbing] = useState(false);
  const id = hive?.id;
  useEffect(() => {
    if (!hive) return;
    const off = t.subscribe((hid, d) => { if (hid === hive.id) setDecision(d); });
    setProbing(true);
    void t.select(hive).then(setDecision).finally(() => setProbing(false));
    return off;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);
  const reprobe = async () => {
    if (!hive) return;
    setProbing(true);
    try {
      setDecision(await t.select(hive, { force: true }));
    } finally {
      setProbing(false);
    }
  };
  return { decision, probing, reprobe };
}
