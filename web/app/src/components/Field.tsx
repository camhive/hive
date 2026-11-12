import type { InputHTMLAttributes } from "react";

/** Labelled input; the hint is linked by aria-describedby so the label text stays exact. */
export function Field({ label, hint, ...rest }: { label: string; hint?: string } & InputHTMLAttributes<HTMLInputElement>) {
  const id = rest.id ?? rest.name ?? label;
  const hintId = hint ? `${id}-hint` : undefined;
  return (
    <div className="mb-4">
      <label className="block mb-1 text-sm font-medium" htmlFor={id}>{label}</label>
      <input id={id} className="input" aria-describedby={hintId} {...rest} />
      {hint && <span id={hintId} className="block mt-1 text-xs muted">{hint}</span>}
    </div>
  );
}
