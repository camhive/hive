import { useEffect, useState } from "react";

/** True while the element is (nearly) in the viewport; always true where IntersectionObserver is missing. */
export function useOnScreen(ref: React.RefObject<Element | null>, margin = "200px"): boolean {
  const [on, setOn] = useState(true);
  useEffect(() => {
    const el = ref.current;
    if (!el || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver(([e]) => setOn(!!e?.isIntersecting), { rootMargin: margin });
    io.observe(el);
    return () => io.disconnect();
  }, [ref, margin]);
  return on;
}
