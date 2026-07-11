/**
 * Scroll-triggered reveal. Elements start hidden (`.reveal`) and fade + rise
 * into place the first time they enter the viewport (`.is-visible`), then stop
 * being observed. Elements already on screen at mount reveal immediately, so
 * this doubles as the above-the-fold entrance.
 *
 * A single shared IntersectionObserver handles every revealed element on the
 * page. Where IntersectionObserver isn't available the helper is a no-op and
 * the element stays visible.
 */

let observer: IntersectionObserver | null = null;

function sharedObserver(): IntersectionObserver {
  if (observer) return observer;
  observer = new IntersectionObserver(
    (entries, obs) => {
      for (const entry of entries) {
        if (entry.isIntersecting) {
          entry.target.classList.add("is-visible");
          obs.unobserve(entry.target);
        }
      }
    },
    // Fire a touch before the element is fully on screen so it finishes
    // arriving as the user scrolls to it, not after.
    { threshold: 0.1, rootMargin: "0px 0px -8% 0px" },
  );
  return observer;
}

/** Mark a node for scroll reveal, optionally staggered by `delayMs`. Returns it. */
export function reveal<T extends HTMLElement>(node: T, delayMs = 0): T {
  if (typeof IntersectionObserver === "undefined") return node;
  node.classList.add("reveal");
  if (delayMs) node.style.transitionDelay = `${delayMs}ms`;
  // Observe after layout settles so in-view elements are measured correctly.
  requestAnimationFrame(() => sharedObserver().observe(node));
  return node;
}
