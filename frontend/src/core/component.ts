/**
 * Minimal class-based component. Subclasses build their DOM in `render()` and
 * return the root element. Lifecycle hooks let components clean up listeners /
 * effects when removed.
 */

export abstract class Component<P = void> {
  protected props: P;
  private _root: HTMLElement | null = null;
  private disposers: Array<() => void> = [];

  constructor(props: P) {
    this.props = props;
  }

  /** Build and return the root element. Called once on first access. */
  protected abstract render(): HTMLElement;

  /** The rendered root, lazily created. */
  get root(): HTMLElement {
    if (!this._root) {
      this._root = this.render();
      this.onMount();
    }
    return this._root;
  }

  /** Append this component into a parent element. */
  mountInto(parent: HTMLElement): this {
    parent.appendChild(this.root);
    return this;
  }

  /** Register a teardown callback run on destroy(). */
  protected onCleanup(fn: () => void): void {
    this.disposers.push(fn);
  }

  /** Override for post-render setup. */
  protected onMount(): void {}

  /** Remove from the DOM and run cleanups. */
  destroy(): void {
    for (const d of this.disposers.splice(0)) d();
    this._root?.remove();
    this._root = null;
  }
}
