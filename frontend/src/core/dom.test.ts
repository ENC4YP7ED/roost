import { describe, it, expect } from "vitest";
import { el } from "./dom.ts";

describe("el() child handling", () => {
  it("treats a null second argument as a (skipped) child, not props", () => {
    // Regression: el(tag, cond ? node : null, ...) used to treat null as a
    // props object and crash in Object.entries(null).
    const node = el("div", null, el("span", "hi"));
    expect(node.tagName).toBe("DIV");
    expect(node.textContent).toBe("hi");
  });

  it("treats a false second argument as a skipped child", () => {
    const show = false;
    const node = el("div", show && el("span", "x"), el("b", "y"));
    expect(node.textContent).toBe("y");
  });

  it("still applies a real props object in the second position", () => {
    const node = el("div", { class: "card" }, "body");
    expect(node.className).toContain("card");
    expect(node.textContent).toBe("body");
  });

  it("mixes null/undefined children among real ones", () => {
    const node = el("div", el("span", "a"), null, undefined, false, el("span", "b"));
    expect(node.querySelectorAll("span").length).toBe(2);
  });
});
