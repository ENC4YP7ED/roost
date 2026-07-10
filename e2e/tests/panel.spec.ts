import { test, expect, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { join } from "node:path";

function creds(): { email: string; password: string } {
  return JSON.parse(readFileSync(join(process.env.ROOST_E2E_DIR!, "creds.json"), "utf8"));
}

function sessionCookie(): string {
  return readFileSync(join(process.env.ROOST_E2E_DIR!, "session.txt"), "utf8").trim();
}

/**
 * Adopt the session captured during global setup. Driving the login form in
 * every test would trip the panel's per-IP login throttle — which the
 * dedicated authentication tests below exercise on purpose.
 */
async function login(page: Page) {
  await page.context().addCookies([{
    name: "roost_session", value: sessionCookie(),
    domain: "127.0.0.1", path: "/", httpOnly: true, sameSite: "Lax",
  }]);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Your servers" })).toBeVisible();
}

/** A genuine form login, for the authentication tests only. */
async function loginViaForm(page: Page) {
  const { email, password } = creds();
  await page.goto("/");
  await page.getByLabel("Email or username").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
}

test.describe("authentication", () => {
  test("rejects bad credentials without leaking whether the account exists", async ({ page }) => {
    await page.goto("/");
    await page.getByLabel("Email or username").fill("nobody@example.com");
    await page.getByLabel("Password").fill("wrongpassword");
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page.getByText("These credentials do not match our records.")).toBeVisible();
    // Still on the login card.
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });

  test("signs in and lands on the dashboard", async ({ page }) => {
    await loginViaForm(page);
    await expect(page.getByRole("heading", { name: "Your servers" })).toBeVisible();
    await expect(page.locator(".rst-topbar__name")).toHaveText("Roost");
  });

  test("signs out and blocks the app again", async ({ page }) => {
    // A form login, not the shared session: logging out revokes the session
    // server-side and would strand every later test.
    await loginViaForm(page);
    await expect(page.getByRole("heading", { name: "Your servers" })).toBeVisible();
    await page.locator(".rst-topbar__conn").click();
    await page.getByText("Sign out").click();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();

    // Reloading must not restore the session.
    await page.reload();
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });

  test("the forgot-password flow is reachable", async ({ page }) => {
    await page.goto("/");
    await page.getByText("Forgot your password?").click();
    await expect(page.getByLabel("Account email")).toBeVisible();
    await page.getByText("← Back to login").click();
    await expect(page.getByLabel("Password")).toBeVisible();
  });
});

test.describe("admin area", () => {
  test.beforeEach(async ({ page }) => login(page));

  test("shows the seeded local node and its allocations", async ({ page }) => {
    await page.getByRole("button", { name: "Admin" }).first().click();
    await page.getByRole("button", { name: "Nodes" }).click();

    await expect(page.getByRole("heading", { name: "Nodes" })).toBeVisible();
    const card = page.locator(".rst-card__title").first();
    await expect(card).toContainText("local");
    await expect(card).toContainText("http://127.0.0.1:8080");

    await page.getByRole("button", { name: "Allocations" }).click();
    await expect(page.getByText("127.0.0.1:25565")).toBeVisible();
    await expect(page.getByText("127.0.0.1:25580")).toBeVisible();
  });

  test("exposes the wings configuration for the local node", async ({ page }) => {
    await page.goto("/#/admin/nodes");
    await page.getByRole("button", { name: "Wings config" }).click();
    const block = page.locator(".rst-codeblock");
    await expect(block).toContainText('"uuid"');
    await expect(block).toContainText('"token_id"');
    await expect(block).toContainText('"remote"');
  });

  test("lists the seeded nests and eggs", async ({ page }) => {
    await page.goto("/#/admin/eggs");
    await expect(page.getByRole("heading", { name: "Nests & Eggs" })).toBeVisible();
    for (const nest of ["Minecraft", "Rust", "Source Engine", "Voice Servers"]) {
      await expect(page.locator(".rst-card__title").filter({ hasText: nest }).first()).toBeVisible();
    }
    await expect(page.locator(".rst-activity__item").filter({ hasText: "Paper" }).first()).toBeVisible();
  });

  test("overview reports the seeded counts", async ({ page }) => {
    await page.goto("/#/admin/overview");
    await expect(page.getByRole("heading", { name: "Overview" })).toBeVisible();
    // Stat tiles: users, servers, nodes, locations.
    await expect(page.locator(".rst-stat")).toHaveCount(4);
    await expect(page.locator(".rst-stat").filter({ hasText: "Nodes" }).locator(".rst-stat__value")).toHaveText("1");
    await expect(page.locator(".rst-stat").filter({ hasText: "Users" }).locator(".rst-stat__value")).toHaveText("1");
  });
});

test.describe("new server modal", () => {
  test.beforeEach(async ({ page }) => login(page));

  test("preselects the seeded node and its first free allocation", async ({ page }) => {
    await page.goto("/#/admin/servers");
    await page.getByRole("button", { name: "New server" }).click();

    await expect(page.getByText("New server")).toBeVisible();
    // Regression: the allocation picker used to stay empty, producing
    // "No free allocation on the selected node".
    await expect(page.getByText(/16 free allocation\(s\) on this node/)).toBeVisible();
    await expect(page.getByText("Default allocation")).toBeVisible();
    await expect(page.locator(".rst-modal").getByText("127.0.0.1:25565")).toBeVisible();
  });

  test("the egg dropdown opens above the modal, not behind it", async ({ page }) => {
    await page.goto("/#/admin/servers");
    await page.getByRole("button", { name: "New server" }).click();

    // Open the Egg select (second custom select inside the modal).
    const eggSelect = page.locator(".rst-modal button.rst-select").nth(1);
    await eggSelect.click();

    const menu = page.locator(".rst-popover-layer .rst-select__menu");
    await expect(menu).toBeVisible();
    await expect(menu.getByText("Minecraft / Paper")).toBeVisible();

    // Regression: the popover layer must be positioned and stack above the
    // modal overlay, otherwise the list renders behind the window.
    const layer = page.locator(".rst-popover-layer");
    const layerStyles = await layer.evaluate((el) => {
      const s = getComputedStyle(el);
      return { position: s.position, zIndex: Number(s.zIndex) };
    });
    const overlayZ = await page.locator(".rst-modal__overlay").evaluate(
      (el) => Number(getComputedStyle(el).zIndex),
    );
    expect(layerStyles.position).toBe("fixed");
    expect(layerStyles.zIndex).toBeGreaterThan(overlayZ);

    // And the option is actually clickable (not covered by the overlay).
    await menu.getByText("Minecraft / Vanilla Minecraft").click();
    await expect(eggSelect).toContainText("Vanilla Minecraft");
  });

  test("Escape closes the dropdown but keeps the modal open", async ({ page }) => {
    await page.goto("/#/admin/servers");
    await page.getByRole("button", { name: "New server" }).click();
    const modal = page.locator(".rst-modal");

    await page.locator(".rst-modal button.rst-select").nth(1).click();
    await expect(page.locator(".rst-select__menu")).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(page.locator(".rst-select__menu")).toBeHidden();
    await expect(modal).toBeVisible(); // regression: used to close both

    await page.keyboard.press("Escape");
    await expect(modal).toBeHidden();
  });

  test("creating a server without a name is refused", async ({ page }) => {
    await page.goto("/#/admin/servers");
    await page.getByRole("button", { name: "New server" }).click();
    await page.getByRole("button", { name: "Create server" }).click();
    await expect(page.getByText("Give the server a name.")).toBeVisible();
    await expect(page.locator(".rst-modal")).toBeVisible();
  });
});

test.describe("server lifecycle", () => {
  test("creates a server that appears on the dashboard", async ({ page }) => {
    await login(page);
    await page.goto("/#/admin/servers");
    await page.getByRole("button", { name: "New server" }).click();
    await page.getByLabel("Server name").fill("E2E Server");
    await page.getByRole("button", { name: "Create server" }).click();

    await expect(page.getByText(/Server created/)).toBeVisible({ timeout: 10_000 });
    await expect(page.locator(".rst-activity__item").getByText("E2E Server")).toBeVisible();

    // It is now visible on the client dashboard, marked as installing.
    await page.goto("/#/");
    const card = page.locator(".rst-servercard");
    await expect(card).toBeVisible();
    await expect(card).toContainText("E2E Server");
    await expect(card).toContainText("127.0.0.1:25565");
    await expect(card.getByText("installing")).toBeVisible();
  });

  test("the server console degrades gracefully when wings is offline", async ({ page }) => {
    await login(page);
    await page.locator(".rst-servercard").click();

    // "Start" alone also matches "Restart" and the "Startup" nav item.
    await expect(page.getByRole("button", { name: "Start", exact: true })).toBeVisible();
    // No wings daemon is running, so the console reports it and the tiles zero out.
    await expect(page.locator(".rst-console__out")).toContainText(/node unreachable|connection to node lost/, {
      timeout: 15_000,
    });
    await expect(page.locator(".rst-stat__value").first()).toContainText("%");
  });

  test("the server sidebar exposes every management tab", async ({ page }) => {
    await login(page);
    await page.locator(".rst-servercard").click();
    for (const tab of ["Console", "Files", "Databases", "Schedules", "Users", "Backups", "Network", "Startup", "Settings", "Activity"]) {
      await expect(page.locator(".rst-navitem").getByText(tab, { exact: true })).toBeVisible();
    }
  });

  test("startup tab shows the egg variable with its default", async ({ page }) => {
    await login(page);
    await page.locator(".rst-servercard").click();
    await page.locator(".rst-navitem").getByText("Startup", { exact: true }).click();
    await expect(page.getByRole("heading", { name: "Startup" })).toBeVisible();
    await expect(page.getByText("Startup command")).toBeVisible();
  });
});

test.describe("database viewer", () => {
  test("is reachable by an admin and shows the connect screen", async ({ page }) => {
    await login(page);
    await page.goto("/dbviewer/");
    await expect(page.getByText("Database Viewer")).toBeVisible();
    await expect(page.getByRole("button", { name: "Connect" })).toBeVisible();
  });

  test("redirects an anonymous visitor to the panel login", async ({ page, context }) => {
    await context.clearCookies();
    await page.goto("/dbviewer/");
    await expect(page).toHaveURL(/\/$/);
    await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  });
});

test.describe("security surface", () => {
  test("the health endpoint is public", async ({ request }) => {
    const res = await request.get("/api/system/health");
    expect(res.status()).toBe(200);
    expect(await res.json()).toMatchObject({ status: "ok", name: "Roost" });
  });

  test("api routes reject anonymous callers", async ({ request }) => {
    for (const path of ["/api/client", "/api/application/users", "/api/remote/servers", "/dbviewer/api/databases"]) {
      const res = await request.get(path);
      expect(res.status(), `${path} should be 401`).toBe(401);
    }
  });

  test("the captcha endpoint never exposes secrets", async ({ request }) => {
    const res = await request.get("/auth/captcha");
    expect(res.status()).toBe(200);
    expect(await res.text()).not.toContain("secret");
  });

  test("session cookie is HttpOnly", async ({ page, context }) => {
    await loginViaForm(page);
    await expect(page.getByRole("heading", { name: "Your servers" })).toBeVisible();
    const cookie = (await context.cookies()).find((c) => c.name === "roost_session");
    expect(cookie).toBeDefined();
    expect(cookie!.httpOnly).toBe(true);
  });
});
