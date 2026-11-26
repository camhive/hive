import { Fragment, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import type { Fact, Group, Tone } from "@/lib/hiveSettings";

const TONE: Record<Tone, string> = { good: "text-[var(--success)]", warn: "text-[var(--accent-text)] font-medium", bad: "error font-medium" };

/**
 * Facts comma-separated, each in its tone. `sentence` capitalises the first
 * letter of the first fact (a row's state starts a sentence; in the strip a
 * value follows its label). Done on the text, not with CSS, so values that
 * follow no rule, such as a hive id, are never touched.
 */
export function Facts({ facts, sentence }: { facts: Fact[]; sentence?: boolean }) {
  const { t, i18n } = useTranslation();
  return (
    <>
      {facts.map((f, i) => {
        const text = t(f.key, f.values);
        return (
          <Fragment key={f.key}>
            {i > 0 && ", "}
            <span className={f.tone ? TONE[f.tone] : undefined}>{sentence && i === 0 ? capitalise(text, i18n.language) : text}</span>
          </Fragment>
        );
      })}
    </>
  );
}

function capitalise(s: string, lang: string): string {
  return s.charAt(0).toLocaleUpperCase(lang) + s.slice(1);
}

/** The phone header is three rows high (126 px at 360 px); one row from 640 px. */
const SCROLL_MARGIN = "scroll-mt-36 sm:scroll-mt-20";

export const groupAnchor = (g: Group) => `settings-${g}`;
/** A row's anchor, for links to one row (the disk banner links to the move). */
export const rowAnchor = (id: string) => `settings-${id}`;

/** One group of the settings page: a heading over a card of rows. */
export function SettingsGroup({ group, danger, children }: { group: Group; danger?: boolean; children: ReactNode }) {
  const { t } = useTranslation();
  const id = groupAnchor(group);
  return (
    <section id={id} aria-labelledby={`${id}-title`} className={SCROLL_MARGIN} data-testid="settings-group" data-group={group}>
      <h2 id={`${id}-title`} className={`text-lg font-semibold mb-2 ${danger ? "error" : ""}`}>{t(`settings.group.${group}`)}</h2>
      <div className="card !py-1 divide-y divide-[var(--border)]" style={danger ? { borderColor: "var(--danger)" } : undefined}>{children}</div>
    </section>
  );
}

type RowProps = {
  /** Prefix of the heading's id and the row's anchor; the row is a region named by its title. */
  id: string;
  title: string;
  /** The state the row governs, before its action. */
  state?: ReactNode;
  action?: ReactNode;
  children?: ReactNode;
  testId?: string;
  /** An id for the area below, so a button can name what it opens (aria-controls). */
  bodyId?: string;
  bodyHidden?: boolean;
};

/** A settings row: title and state on the left, the action on the right, anything opened below. */
export function SettingsRow({ id, title, state, action, children, testId, bodyId, bodyHidden }: RowProps) {
  return (
    <section id={rowAnchor(id)} aria-labelledby={`${id}-title`} className={`py-4 ${SCROLL_MARGIN}`} data-testid={testId}>
      <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="min-w-0 flex-1 basis-56">
          <h3 id={`${id}-title`} className="font-medium">{title}</h3>
          {state && <p className="text-sm muted mt-0.5 [overflow-wrap:anywhere]" data-testid={`${id}-row-state`}>{state}</p>}
        </div>
        {action && <div className="shrink-0">{action}</div>}
      </div>
      {(children || bodyId) && <div id={bodyId} hidden={bodyHidden} className="mt-3">{children}</div>}
    </section>
  );
}

type PanelRowProps = Omit<RowProps, "action" | "children"> & {
  /** The button that opens the panel; none when there is nothing to open. */
  openLabel?: string;
  disabled?: boolean;
  /** The explanation, shown above the panel once it is open. */
  help: string;
  /** Shown instead of the panel, e.g. the hive needs an update (ADR-0022). */
  notice?: ReactNode;
  /** The panel; rendered only once opened, so it asks the hive only then. */
  panel: () => ReactNode;
};

/** A row whose action opens its panel below it, the explanation first. */
export function PanelRow({ openLabel, disabled, help, notice, panel, ...row }: PanelRowProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  if (notice) {
    const action = openLabel && <button type="button" className="btn btn-secondary btn-sm" disabled>{openLabel}</button>;
    return <SettingsRow {...row} action={action}>{notice}</SettingsRow>;
  }
  const bodyId = `${row.id}-panel`;
  const action = openLabel && (
    <button type="button" className="btn btn-secondary btn-sm" aria-expanded={open} aria-controls={bodyId} disabled={disabled} onClick={() => setOpen(!open)}>
      {open ? t("settings.close") : openLabel}
    </button>
  );
  return (
    <SettingsRow {...row} action={action} bodyId={bodyId} bodyHidden={!open}>
      {open && <div className="space-y-3"><p className="muted text-sm">{help}</p>{panel()}</div>}
    </SettingsRow>
  );
}
