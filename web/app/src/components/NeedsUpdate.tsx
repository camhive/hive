import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { useMutation } from "@tanstack/react-query";
import { unwrapHive } from "@camhive/api";
import { hiveCapability } from "@/lib/capabilities";
import { errorMessage } from "@/lib/errors";
import { RELEASES_URL, type Hive } from "@/lib/hives";
import { useHiveClient } from "@/lib/hiveApi";
import { useGrant } from "@/lib/useGrant";

type Props = {
  hive: Hive;
  /** The version that brings the feature (useHiveCapability's `since`). */
  since: string;
  /** The owner can update the hive; anyone else asks the owner. */
  owner: boolean;
};

/**
 * The hive is too old for a feature (ADR-0022): which version brings it and
 * what to do. Plan limits (PlanLimitBox) and roles have their own texts;
 * this notice is only for the hive's software. The owner gets "Update now"
 * where the hive can update from the app (#82), else how to update it.
 */
export function NeedsUpdate({ hive, since, owner }: Props) {
  const { t } = useTranslation();
  const fromApp = hiveCapability(hive, "updates").has;
  return (
    <div role="note" className="callout text-sm" data-testid="needs-update">
      <p className="font-medium">{t("capability.needsUpdate", { since, running: hive.version || t("capability.unknownVersion") })}</p>
      {!owner ? <p className="muted mt-1">{t("capability.askOwner")}</p>
        : fromApp ? <UpdateNowButton hive={hive} />
        : (
          <p className="muted mt-1">
            {t("capability.ownerHelp")}{" "}
            <a href={RELEASES_URL} target="_blank" rel="noreferrer" className="link">{t("capability.download")}</a>
          </p>
        )}
    </div>
  );
}

/** "Update now" (#82); it asks the hive for access only when clicked. */
function UpdateNowButton({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const [asked, setAsked] = useState(false);
  if (asked) return <UpdateNowRequest hive={hive} />;
  return <button type="button" className="btn btn-secondary btn-sm mt-2" disabled={!hive.online} onClick={() => setAsked(true)}>{t("updates.updateNow")}</button>;
}

function UpdateNowRequest({ hive }: { hive: Hive }) {
  const { t } = useTranslation();
  const { grant, access } = useGrant(hive);
  const client = useHiveClient(hive, grant);
  const request = useMutation({ mutationFn: async () => { unwrapHive(await client!.POST("/v0/hive/updates/install")); } });
  const { isIdle, mutate } = request;
  useEffect(() => { if (client && isIdle) mutate(); }, [client, isIdle, mutate]);
  const err = access.error ?? request.error;
  if (err) return <p role="alert" className="error mt-2">{errorMessage(t, err)}</p>;
  return <p role="status" className="mt-2">{request.isSuccess ? t("updates.requested") : "…"}</p>;
}
