import { spawn } from "node:child_process";
import { mkdtempSync, writeFileSync, readFileSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const PORT = 18500;
const BINARY = resolve(__dirname, "../backend/roost");

/**
 * Boot the real binary on a fresh database and stash the generated admin
 * password where the tests can read it.
 */
export default async function globalSetup() {
  if (!existsSync(BINARY)) {
    throw new Error(`roost binary not found at ${BINARY} — run "make backend" first`);
  }
  const dir = mkdtempSync(join(tmpdir(), "roost-e2e-"));
  const logFile = join(dir, "roost.log");
  const chunks: string[] = [];

  const proc = spawn(BINARY, ["-db", join(dir, "e2e.db"), "-addr", `:${PORT}`], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  const collect = (b: Buffer) => chunks.push(b.toString());
  proc.stdout.on("data", collect);
  proc.stderr.on("data", collect);

  // Wait for the health endpoint.
  const deadline = Date.now() + 20_000;
  for (;;) {
    if (Date.now() > deadline) {
      proc.kill();
      throw new Error(`roost did not become healthy:\n${chunks.join("")}`);
    }
    try {
      const res = await fetch(`http://127.0.0.1:${PORT}/api/system/health`);
      if (res.ok) break;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 200));
  }

  const log = chunks.join("");
  writeFileSync(logFile, log);
  const password = /password:\s*(\S+)/.exec(log)?.[1];
  if (!password) throw new Error(`could not find the seeded admin password in:\n${log}`);

  writeFileSync(join(dir, "creds.json"), JSON.stringify({ email: "admin@example.com", password }));

  // Log in once over HTTP and cache the session cookie. Tests reuse it instead
  // of driving the login form, which would trip the per-IP login throttle.
  const res = await fetch(`http://127.0.0.1:${PORT}/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ user: "admin@example.com", password }),
  });
  if (!res.ok) throw new Error(`seed login failed: ${res.status}`);
  const setCookie = res.headers.getSetCookie?.() ?? [];
  const session = setCookie
    .map((c) => /roost_session=([^;]+)/.exec(c)?.[1])
    .find(Boolean);
  if (!session) throw new Error("no roost_session cookie in the login response");
  writeFileSync(join(dir, "session.txt"), session);

  process.env.ROOST_E2E_DIR = dir;
  process.env.ROOST_E2E_PID = String(proc.pid);
  proc.unref();
}

export function creds(): { email: string; password: string } {
  const dir = process.env.ROOST_E2E_DIR!;
  return JSON.parse(readFileSync(join(dir, "creds.json"), "utf8"));
}
