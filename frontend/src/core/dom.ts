/**
 * Hyperscript-ish DOM helpers. `el()` creates elements with a terse props
 * object; signal-valued props/children update reactively via `effect`.
 */

import { effect, Signal } from "./reactive.ts";

export type Child =
  | Node
  | string
  | number
  | false
  | null
  | undefined
  | Signal<unknown>
  | (() => Child)
  | Child[];

type StyleMap = Partial<CSSStyleDeclaration> & Record<string, string | number>;

export interface Props {
  class?: string | (() => string) | Signal<string>;
  style?: StyleMap;
  dataset?: Record<string, string>;
  attrs?: Record<string, string | number | boolean | null | undefined>;
  // anything else: properties (onclick, value, etc.) or event handlers
  [key: string]: unknown;
}

/** Create an element. Tag may include `.class` and `#id` shorthand. */
export function el<K extends keyof HTMLElementTagNameMap>(
  tag: K | string,
  props: Props | Child = {},
  ...children: Child[]
): HTMLElementTagNameMap[K] {
  // Allow el("div", child) without a props object.
  if (isChild(props)) {
    children = [props as Child, ...children];
    props = {};
  }
  const p = props as Props;

  const { tagName, classes, id } = parseTag(tag);
  const node = document.createElement(tagName) as HTMLElementTagNameMap[K];
  if (id) node.id = id;
  if (classes.length) node.classList.add(...classes);

  applyProps(node, p);
  appendChildren(node, children);
  return node;
}

function parseTag(tag: string): { tagName: string; classes: string[]; id: string } {
  const classes: string[] = [];
  let id = "";
  const tagName = tag.replace(/[.#][^.#]+/g, (m) => {
    if (m[0] === ".") classes.push(m.slice(1));
    else id = m.slice(1);
    return "";
  }) || "div";
  return { tagName, classes, id };
}

function applyProps(node: HTMLElement, p: Props): void {
  for (const [key, value] of Object.entries(p)) {
    if (value == null) continue;
    switch (key) {
      case "class":
        bindClass(node, value as Props["class"]);
        break;
      case "style":
        Object.assign(node.style, value as StyleMap);
        break;
      case "dataset":
        Object.assign(node.dataset, value as Record<string, string>);
        break;
      case "attrs":
        for (const [an, av] of Object.entries(value as Record<string, unknown>)) {
          if (av === false || av == null) node.removeAttribute(an);
          else node.setAttribute(an, av === true ? "" : String(av));
        }
        break;
      default:
        if (key.startsWith("on") && typeof value === "function") {
          node.addEventListener(key.slice(2).toLowerCase(), value as EventListener);
        } else if (value instanceof Signal) {
          effect(() => setProp(node, key, (value as Signal<unknown>).value));
        } else if (typeof value === "function" && key !== "ref") {
          effect(() => setProp(node, key, (value as () => unknown)()));
        } else if (key === "ref" && typeof value === "function") {
          (value as (n: HTMLElement) => void)(node);
        } else {
          setProp(node, key, value);
        }
    }
  }
}

function setProp(node: HTMLElement, key: string, value: unknown): void {
  if (key in node) {
    // DOM property (value, checked, disabled, textContent…).
    (node as unknown as Record<string, unknown>)[key] = value;
  } else if (value === false || value == null) {
    node.removeAttribute(key);
  } else {
    node.setAttribute(key, String(value));
  }
}

function bindClass(node: HTMLElement, value: Props["class"]): void {
  const base = node.className;
  const apply = (extra: string) => {
    node.className = [base, extra].filter(Boolean).join(" ");
  };
  if (value instanceof Signal) {
    effect(() => apply(value.value));
  } else if (typeof value === "function") {
    effect(() => apply((value as () => string)()));
  } else if (typeof value === "string") {
    apply(value);
  }
}

function appendChildren(node: HTMLElement, children: Child[]): void {
  for (const child of children) appendChild(node, child);
}

function appendChild(node: HTMLElement, child: Child): void {
  if (child == null || child === false) return;

  if (Array.isArray(child)) {
    for (const c of child) appendChild(node, c);
    return;
  }
  if (child instanceof Node) {
    node.appendChild(child);
    return;
  }
  if (child instanceof Signal || typeof child === "function") {
    // Reactive text/fragment: anchor with a comment and swap on change.
    const anchor = document.createComment("");
    node.appendChild(anchor);
    let rendered: ChildNode[] = [];
    effect(() => {
      const value = child instanceof Signal ? child.value : (child as () => Child)();
      for (const n of rendered) n.remove();
      rendered = [];
      const frag = document.createDocumentFragment();
      materialize(frag, value as Child, rendered);
      anchor.after(frag);
    });
    return;
  }
  node.appendChild(document.createTextNode(String(child)));
}

function materialize(parent: DocumentFragment, child: Child, collect: ChildNode[]): void {
  if (child == null || child === false) return;
  if (Array.isArray(child)) {
    for (const c of child) materialize(parent, c, collect);
    return;
  }
  if (child instanceof Node) {
    collect.push(child as ChildNode);
    parent.appendChild(child);
    return;
  }
  const text = document.createTextNode(String(child));
  collect.push(text);
  parent.appendChild(text);
}

function isChild(v: unknown): v is Child {
  return (
    v == null ||
    v === false ||
    typeof v === "string" ||
    typeof v === "number" ||
    v instanceof Node ||
    v instanceof Signal ||
    Array.isArray(v)
  );
}

/** Font Awesome icon element. `name` is the icon (e.g. "database"). */
export function icon(name: string, opts: { solid?: boolean; brand?: boolean; class?: string; spin?: boolean } = {}): HTMLElement {
  const family = opts.brand ? "fa-brands" : opts.solid === false ? "fa-regular" : "fa-solid";
  const cls = ["fa-icon", family, `fa-${name}`, opts.spin ? "fa-spin" : "", opts.class ?? ""]
    .filter(Boolean)
    .join(" ");
  return el("i", { class: cls, attrs: { "aria-hidden": "true" } });
}

/** Remove all children of a node. */
export function clear(node: Node): void {
  while (node.firstChild) node.firstChild.remove();
}

/** Mount a node or component-root into a container, replacing its contents. */
export function mount(container: HTMLElement, node: Node): void {
  clear(container);
  container.appendChild(node);
}
