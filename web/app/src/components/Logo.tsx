const BLADES = [
  "M37.74 22.12 55.21 19.68v21.29L42.8 25.04Z",
  "M43.43 32.03 54.27 45.95 35.84 56.59l7.59-18.72Z",
  "M37.69 41.91 31.06 58.26 12.62 47.62l20.01-2.79Z",
  "M26.26 41.88 8.79 44.32V23.03l12.41 15.93Z",
  "M20.57 31.97 9.73 18.05 28.16 7.41l-7.59 18.72Z",
];
const HONEY_BLADE = "M26.31 22.09 32.94 5.74l18.44 10.64-20.01 2.79Z";

/** The Camhive mark: a camera aperture drawn as a honeycomb cell, one blade in honey. */
export function LogoMark({ size = 28, className }: { size?: number; className?: string }) {
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" className={className} aria-hidden="true" focusable="false" strokeWidth="2" strokeLinejoin="round">
      {BLADES.map((d) => <path key={d} d={d} fill="currentColor" stroke="currentColor" />)}
      <path d={HONEY_BLADE} fill="var(--accent)" stroke="var(--accent)" />
    </svg>
  );
}

/** Mark plus wordmark, for the header. */
export function Logo({ size = 28, className = "" }: { size?: number; className?: string }) {
  return (
    <span className={`inline-flex items-center gap-2 ${className}`}>
      <LogoMark size={size} />
      <span className="font-semibold" style={{ fontSize: size * 0.64, letterSpacing: "-0.02em" }}>camhive</span>
    </span>
  );
}
