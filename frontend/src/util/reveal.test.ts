import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { reveal } from "./reveal.ts";

describe("reveal()", () => {
  const original = globalThis.IntersectionObserver;
  let observed: Element[];

  beforeEach(() => {
    observed = [];
    // Minimal IntersectionObserver stand-in for the test environment.
    globalThis.IntersectionObserver = class {
      constructor(_cb: IntersectionObserverCallback) {}
      observe(el: Element) { observed.push(el); }
      unobserve() {}
      disconnect() {}
    } as unknown as typeof IntersectionObserver;
    vi.stubGlobal("requestAnimationFrame", (cb: FrameRequestCallback) => { cb(0); return 0; });
  });

  afterEach(() => {
    globalThis.IntersectionObserver = original;
    vi.unstubAllGlobals();
  });

  it("tags the node, applies a stagger delay, and observes it", () => {
    const node = document.createElement("div");
    const out = reveal(node, 120);
    expect(out).toBe(node);
    expect(node.classList.contains("reveal")).toBe(true);
    expect(node.style.transitionDelay).toBe("120ms");
    expect(observed).toContain(node);
  });

  it("is a no-op (leaves the node visible) without IntersectionObserver", () => {
    // @ts-expect-error simulate an environment lacking the API
    globalThis.IntersectionObserver = undefined;
    const node = document.createElement("div");
    expect(reveal(node)).toBe(node);
    expect(node.classList.contains("reveal")).toBe(false);
  });
});
