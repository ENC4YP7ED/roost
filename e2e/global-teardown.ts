export default async function globalTeardown() {
  const pid = Number(process.env.ROOST_E2E_PID);
  if (pid) {
    try {
      process.kill(pid, "SIGTERM");
    } catch {
      /* already gone */
    }
  }
}
