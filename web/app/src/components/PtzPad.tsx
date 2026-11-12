import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { unwrapHive, type HiveClient } from "@camhive/api";

/** Joystick-style PTZ: hold a button to move, release to stop; presets below. */
export function PtzPad({ client, cameraId }: { client: HiveClient; cameraId: string }) {
  const { t } = useTranslation();
  const [supported, setSupported] = useState<boolean | null>(null);
  const [presets, setPresets] = useState<{ token: string; name: string }[]>([]);
  const [error, setError] = useState<string | null>(null);
  const path = { params: { path: { id: cameraId } } };

  useEffect(() => {
    let cancelled = false;
    client.GET("/v0/cameras/{id}/ptz", path).then((r) => {
      if (cancelled) return;
      const ok = !!r.data?.supported;
      setSupported(ok);
      if (ok) client.GET("/v0/cameras/{id}/ptz/presets", path).then((p) => { if (!cancelled && Array.isArray(p.data)) setPresets(p.data.map((x) => ({ token: x.token ?? "", name: x.name ?? x.token ?? "" }))); }).catch(() => undefined);
    }).catch(() => { if (!cancelled) setSupported(false); });
    return () => { cancelled = true; };
  }, [client, cameraId]); // eslint-disable-line react-hooks/exhaustive-deps

  if (!supported) return null;
  const move = async (pan: number, tilt: number, zoom: number) => {
    try {
      unwrapHive(await client.POST("/v0/cameras/{id}/ptz/move", { ...path, body: { pan, tilt, zoom } }));
      setError(null);
    } catch {
      setError(t("live.ptzFailed"));
    }
  };
  const stop = () => void client.POST("/v0/cameras/{id}/ptz/stop", path).catch(() => undefined);
  const btn = (label: string, pan: number, tilt: number, zoom: number, glyph: string) => (
    <button type="button" className="btn btn-xs" aria-label={label} onPointerDown={() => void move(pan, tilt, zoom)} onPointerUp={stop} onPointerLeave={stop} onPointerCancel={stop}>{glyph}</button>
  );
  return (
    <div className="absolute bottom-2 right-2 flex flex-col items-end gap-1.5" data-testid="ptz">
      <div className="glass grid grid-cols-3 gap-0.5 rounded-lg p-1">
        <span /> {btn(t("live.ptz.up"), 0, 0.5, 0, "▲")} <span />
        {btn(t("live.ptz.left"), -0.5, 0, 0, "◀")} {btn(t("live.ptz.zoomIn"), 0, 0, 0.5, "+")} {btn(t("live.ptz.right"), 0.5, 0, 0, "▶")}
        <span /> {btn(t("live.ptz.down"), 0, -0.5, 0, "▼")} {btn(t("live.ptz.zoomOut"), 0, 0, -0.5, "−")}
      </div>
      {presets.length > 0 && (
        <select aria-label={t("live.ptz.presets")} className="input input-sm !w-auto glass !text-white text-xs" defaultValue="" onChange={(e) => { if (e.target.value) void client.POST("/v0/cameras/{id}/ptz/presets/{preset}/goto", { params: { path: { id: cameraId, preset: e.target.value } } }).catch(() => undefined); e.target.value = ""; }}>
          <option value="">{t("live.ptz.presets")}</option>
          {presets.map((p) => <option key={p.token} value={p.token}>{p.name}</option>)}
        </select>
      )}
      {error && <span className="glass text-xs px-2 py-0.5 rounded-md" role="alert">{error}</span>}
    </div>
  );
}
