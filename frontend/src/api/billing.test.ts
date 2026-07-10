import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { client, admin } from "./client.ts";

const fetchMock = vi.fn();
beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

const ok = (body: unknown) => () =>
  Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }));

describe("customer billing client", () => {
  it("posts a checkout with product and provider", async () => {
    fetchMock.mockImplementation(ok({ data: { redirect_url: "https://pay", order: "o1" } }));
    const res = await client.billing.checkout(7, "stripe");
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/client/billing/checkout");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body)).toEqual({ product_id: 7, provider: "stripe" });
    expect(res.data.redirect_url).toBe("https://pay");
  });

  it("builds the invoice URL with an encoded number", () => {
    expect(client.billing.invoiceURL("INV-2026-0001")).toBe(
      "/api/client/billing/invoices/INV-2026-0001/html",
    );
  });

  it("saves the billing profile via PUT", async () => {
    fetchMock.mockImplementation(ok({ object: "billing_profile", attributes: {} }));
    await client.billing.saveProfile({ Country: "DE" });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/client/billing/profile");
    expect(init.method).toBe("PUT");
  });
});

describe("admin billing client", () => {
  it("creates and updates products at the right endpoints", async () => {
    fetchMock.mockImplementation(ok({ object: "product", attributes: {} }));
    await admin.billing.createProduct({ name: "P" });
    expect(fetchMock.mock.calls[0][0]).toBe("/api/application/billing/products");
    expect(fetchMock.mock.calls[0][1].method).toBe("POST");

    await admin.billing.updateProduct(3, { active: false });
    expect(fetchMock.mock.calls[1][0]).toBe("/api/application/billing/products/3");
    expect(fetchMock.mock.calls[1][1].method).toBe("PATCH");
  });

  it("saves settings via PUT", async () => {
    fetchMock.mockImplementation(ok({}));
    await admin.billing.saveSettings({ enabled: true });
    expect(fetchMock.mock.calls[0][0]).toBe("/api/application/billing/settings");
    expect(fetchMock.mock.calls[0][1].method).toBe("PUT");
  });
});
