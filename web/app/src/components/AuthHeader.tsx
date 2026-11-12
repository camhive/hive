import { LogoMark } from "@/components/Logo";

/** Logo, title and an optional line of intro on top of the sign-in style forms. */
export function AuthHeader({ title, intro }: { title: string; intro?: React.ReactNode }) {
  return (
    <div className="mb-6">
      <LogoMark size={36} className="mb-5" />
      <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
      {intro && <div className="muted mt-1.5 text-sm">{intro}</div>}
    </div>
  );
}
