import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { http, unwrap, ApiError, auth, client, admin } from "./client.ts";

const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

// A fresh Response per call: a Response body can only be read once, so a
// shared instance would break on the second fetch.
function ok(body: unknown, status = 200) {
  return () => Promise.resolve(new Response(JSON.stringify(body), {
    status, headers: { "Content-Type": "application/json" },
  }));
}

function fail(body: unknown, status: number) {
  return () => Promise.resolve(new Response(JSON.stringify(body), { status }));
}

describe("unwrap", () => {
  it("flattens a list envelope into attributes", () => {
    const list = {
      object: "list" as const,
      data: [
        { object: "server", attributes: { name: "a" } },
        { object: "server", attributes: { name: "b" } },
      ],
    };
    expect(unwrap<{ name: string }>(list)).toEqual([{ name: "a" }, { name: "b" }]);
  });

  it("tolerates a missing data array", () => {
    expect(unwrap({} as never)).toEqual([]);
  });
});

describe("request handling", () => {
  it("returns undefined for 204 without parsing a body", async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response(null, { status: 204 })));
    await expect(http.del("/x")).resolves.toBeUndefined();
  });

  it("throws ApiError carrying the panel's detail message", async () => {
    fetchMock.mockImplementation(fail({ errors: [{ code: "Forbidden", status: "403", detail: "nope" }] }, 403));
    await expect(http.get("/x")).rejects.toThrowError(new ApiError(403, "nope"));
    try {
      await http.get("/x");
    } catch (e) {
      expect(e).toBeInstanceOf(ApiError);
      expect((e as ApiError).status).toBe(403);
    }
  });

  it("falls back to the status code when the body is not an error envelope", async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response("<html>502</html>", { status: 502 })));
    await expect(http.get("/x")).rejects.toThrow("HTTP 502");
  });

  it("sends JSON bodies with the right content type", async () => {
    fetchMock.mockImplementation(ok({}));
    await http.post("/x", { a: 1 });
    const [, init] = fetchMock.mock.calls[0];
    expect(init.method).toBe("POST");
    expect(init.body).toBe('{"a":1}');
    expect(init.headers["Content-Type"]).toBe("application/json");
    expect(init.credentials).toBe("same-origin");
  });

  it("omits the body and content type for GET", async () => {
    fetchMock.mockImplementation(ok({}));
    await http.get("/x");
    const [, init] = fetchMock.mock.calls[0];
    expect(init.body).toBeUndefined();
    expect(init.headers).toEqual({});
  });
});

describe("auth endpoints", () => {
  it("passes captcha tokens through on login", async () => {
    fetchMock.mockImplementation(ok({ data: { complete: true } }));
    await auth.login("admin", "pw", { "1": "tok" });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/auth/login");
    expect(JSON.parse(init.body)).toEqual({
      user: "admin", password: "pw", captcha_tokens: { "1": "tok" },
    });
  });

  it("sends a recovery token instead of a code when asked", async () => {
    fetchMock.mockImplementation(ok({ data: { complete: true } }));
    await auth.checkpoint("ct", "REC", true);
    expect(JSON.parse(fetchMock.mock.calls[0][1].body)).toEqual({
      confirmation_token: "ct", recovery_token: "REC",
    });

    fetchMock.mockImplementation(ok({ data: { complete: true } }));
    await auth.checkpoint("ct", "123456", false);
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toEqual({
      confirmation_token: "ct", authentication_code: "123456",
    });
  });
});

describe("url construction", () => {
  it("encodes file paths in the file manager", async () => {
    fetchMock.mockImplementation(ok({ object: "list", data: [] }));
    await client.files.list("abc", "/a b/c&d");
    expect(fetchMock.mock.calls[0][0]).toBe(
      "/api/client/servers/abc/files/list?directory=%2Fa%20b%2Fc%26d",
    );
  });

  it("requests database passwords explicitly", async () => {
    fetchMock.mockImplementation(ok({ object: "list", data: [] }));
    await client.databases.list("abc");
    expect(fetchMock.mock.calls[0][0]).toContain("include=password");
  });

  it("builds the force-delete server path", async () => {
    fetchMock.mockImplementation(() => Promise.resolve(new Response(null, { status: 204 })));
    await admin.servers.remove(7, true);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/application/servers/7/force");
    await admin.servers.remove(7);
    expect(fetchMock.mock.calls[1][0]).toBe("/api/application/servers/7");
  });

  it("escapes admin user filters", async () => {
    fetchMock.mockImplementation(ok({ object: "list", data: [] }));
    await admin.users.list("a b&c");
    expect(fetchMock.mock.calls[0][0]).toBe("/api/application/users?filter=a%20b%26c");
    await admin.users.list("");
    expect(fetchMock.mock.calls[1][0]).toBe("/api/application/users");
  });

  it("exposes the egg export url without fetching", () => {
    expect(admin.nests.exportURL(1, 2)).toBe("/api/application/nests/1/eggs/2/export");
  });
});

describe("tls endpoints", () => {
  it("saves the full settings payload", async () => {
    fetchMock.mockImplementation(ok({ restart_required: true }));
    await admin.tls.save({ enabled: true, domain: "p.example.com", email: "a@b.co", staging: false });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/application/tls");
    expect(init.method).toBe("PUT");
    expect(JSON.parse(init.body)).toEqual({
      enabled: true, domain: "p.example.com", email: "a@b.co", staging: false,
    });
  });
});
