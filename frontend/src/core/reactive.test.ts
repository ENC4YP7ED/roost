import { describe, it, expect, vi } from "vitest";
import { signal, effect, computed } from "./reactive.ts";

describe("signal", () => {
  it("holds and updates a value", () => {
    const s = signal(1);
    expect(s.value).toBe(1);
    s.value = 2;
    expect(s.value).toBe(2);
  });

  it("peek reads without subscribing", () => {
    const s = signal(0);
    const spy = vi.fn();
    effect(() => { spy(s.peek()); });
    expect(spy).toHaveBeenCalledTimes(1);
    s.value = 1;
    expect(spy).toHaveBeenCalledTimes(1); // peek created no dependency
  });

  it("update applies a transform", () => {
    const s = signal(5);
    s.update((v) => v * 2);
    expect(s.value).toBe(10);
  });
});

describe("effect", () => {
  it("runs immediately and on every change", () => {
    const s = signal("a");
    const seen: string[] = [];
    effect(() => seen.push(s.value));
    expect(seen).toEqual(["a"]);
    s.value = "b";
    s.value = "c";
    expect(seen).toEqual(["a", "b", "c"]);
  });

  it("does not re-run when the value is unchanged (Object.is)", () => {
    const s = signal(1);
    const spy = vi.fn();
    effect(() => { s.value; spy(); });
    s.value = 1;
    expect(spy).toHaveBeenCalledTimes(1);
  });

  it("tracks multiple signals", () => {
    const a = signal(1);
    const b = signal(2);
    const spy = vi.fn();
    effect(() => { a.value; b.value; spy(); });
    expect(spy).toHaveBeenCalledTimes(1);
    a.value = 9;
    b.value = 9;
    expect(spy).toHaveBeenCalledTimes(3);
  });

  it("stops running after the disposer is called", () => {
    const s = signal(0);
    const spy = vi.fn();
    const stop = effect(() => { s.value; spy(); });
    s.value = 1;
    expect(spy).toHaveBeenCalledTimes(2);
    stop();
    s.value = 2;
    expect(spy).toHaveBeenCalledTimes(2);
  });

  it("re-collects dependencies on each run so stale ones are dropped", () => {
    const toggle = signal(true);
    const a = signal("a");
    const b = signal("b");
    const spy = vi.fn();
    effect(() => { spy(toggle.value ? a.value : b.value); });
    expect(spy).toHaveBeenCalledTimes(1);

    // While reading `a`, writing `b` must not trigger the effect.
    b.value = "b2";
    expect(spy).toHaveBeenCalledTimes(1);

    toggle.value = false; // now reads b
    expect(spy).toHaveBeenCalledTimes(2);

    // `a` is no longer a dependency.
    a.value = "a2";
    expect(spy).toHaveBeenCalledTimes(2);
    b.value = "b3";
    expect(spy).toHaveBeenCalledTimes(3);
  });

  it("supports nested effects without losing the outer tracking context", () => {
    const outer = signal(0);
    const inner = signal(0);
    const outerSpy = vi.fn();
    const innerSpy = vi.fn();

    effect(() => {
      outer.value;
      outerSpy();
      effect(() => { inner.value; innerSpy(); });
    });

    expect(outerSpy).toHaveBeenCalledTimes(1);
    expect(innerSpy).toHaveBeenCalledTimes(1);

    // Writing the outer signal must re-run the outer effect (and its nested one).
    outer.value = 1;
    expect(outerSpy).toHaveBeenCalledTimes(2);
  });
});

describe("computed", () => {
  it("derives from other signals", () => {
    const a = signal(2);
    const b = signal(3);
    const sum = computed(() => a.value + b.value);
    expect(sum.value).toBe(5);
    a.value = 10;
    expect(sum.value).toBe(13);
  });

  it("is itself reactive", () => {
    const n = signal(1);
    const doubled = computed(() => n.value * 2);
    const seen: number[] = [];
    effect(() => seen.push(doubled.value));
    n.value = 2;
    expect(seen).toEqual([2, 4]);
  });
});
