/**
 * A tiny dependency-tracking reactive core — just enough to drive a hand-built
 * component library without pulling in a framework.
 *
 *   const count = signal(0);
 *   effect(() => console.log(count.value));  // logs 0, then on every change
 *   count.value++;                            // triggers the effect
 */

type Subscriber = () => void;

let activeEffect: EffectRunner | null = null;
const effectStack: EffectRunner[] = [];

class EffectRunner {
  private deps = new Set<Set<Subscriber>>();
  /** Stable identity stored in dependency sets; bound once at construction. */
  readonly runBound: Subscriber;
  private active = true;

  constructor(private fn: () => void) {
    this.runBound = () => this.run();
  }

  run(): void {
    if (!this.active) return;
    this.cleanup();
    effectStack.push(this);
    activeEffect = this;
    try {
      this.fn();
    } finally {
      effectStack.pop();
      activeEffect = effectStack[effectStack.length - 1] ?? null;
    }
  }

  addDep(dep: Set<Subscriber>): void {
    this.deps.add(dep);
  }

  private cleanup(): void {
    for (const dep of this.deps) dep.delete(this.runBound);
    this.deps.clear();
  }

  stop(): void {
    this.cleanup();
    this.active = false;
  }
}

/** A reactive box around a single value. */
export class Signal<T> {
  private _value: T;
  private subs = new Set<Subscriber>();

  constructor(value: T) {
    this._value = value;
  }

  get value(): T {
    if (activeEffect) {
      this.subs.add(activeEffect.runBound);
      activeEffect.addDep(this.subs);
    }
    return this._value;
  }

  set value(next: T) {
    if (Object.is(next, this._value)) return;
    this._value = next;
    this.notify();
  }

  /** Replace the value via a transform, always notifying subscribers. */
  update(fn: (current: T) => T): void {
    this.value = fn(this._value);
  }

  /** Read without subscribing the current effect. */
  peek(): T {
    return this._value;
  }

  notify(): void {
    for (const sub of [...this.subs]) sub();
  }
}

export function signal<T>(value: T): Signal<T> {
  return new Signal(value);
}

/** Run `fn` now and re-run it whenever any signal it reads changes. */
export function effect(fn: () => void): () => void {
  const runner = new EffectRunner(fn);
  runner.run();
  return () => runner.stop();
}

/** A derived, read-only signal computed from other signals. */
export function computed<T>(fn: () => T): { readonly value: T } {
  const result = signal<T>(undefined as unknown as T);
  effect(() => {
    result.value = fn();
  });
  return {
    get value() {
      return result.value;
    },
  };
}
