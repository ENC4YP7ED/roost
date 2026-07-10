import { el, icon } from "../core/dom.ts";
import { Button } from "../components/Button.ts";
import { notify } from "../components/Toast.ts";
import { client } from "../api/client.ts";
import { store } from "../state/store.ts";
import { page, statTile, statusDot, memText, cpuText } from "./shared.ts";
import { formatBytes, formatUptime } from "../util/format.ts";
import type { ServerCtx } from "./ServerView.ts";

/**
 * Live console. Connects directly to wings' websocket with a panel-signed
 * JWT (the same protocol Pterodactyl's React app speaks). When wings is
 * unreachable the view degrades to an offline banner + REST power actions.
 */
export function ServerConsole(ctx: ServerCtx): HTMLElement {
  const out = el("div.rst-console__out", {
    style: { "--console-fs": `${store.prefs.peek().consoleFontSize}px` } as never,
    attrs: { role: "log", "aria-live": "polite" },
  });
  let ws: WebSocket | null = null;
  let closed = false;

  const state = { current: "offline" };
  const dot = statusDot("offline");
  const stateLabel = el("span.mono.faint", "offline");

  const statCPU = el("div");
  const statMem = el("div");
  const statDisk = el("div");
  const statUptime = el("div");
  renderStats({ cpu_absolute: 0, memory_bytes: 0, disk_bytes: 0, uptime: 0 });

  function renderStats(r: { cpu_absolute: number; memory_bytes: number; disk_bytes: number; uptime: number }) {
    statCPU.replaceChildren(statTile("CPU", cpuText(r.cpu_absolute, ctx.attrs.limits.cpu), "microchip"));
    statMem.replaceChildren(statTile("Memory", memText(r.memory_bytes, ctx.attrs.limits.memory), "memory"));
    statDisk.replaceChildren(statTile("Disk", formatBytes(r.disk_bytes), "hard-drive"));
    statUptime.replaceChildren(statTile("Uptime", r.uptime > 0 ? formatUptime(Math.floor(r.uptime / 1000)) : "—", "stopwatch"));
  }

  function setState(s: string) {
    state.current = s;
    dot.className = `rst-statusdot rst-statusdot--${s}`;
    stateLabel.textContent = s;
  }

  function print(line: string, cls = "") {
    const atBottom = out.scrollTop + out.clientHeight >= out.scrollHeight - 24;
    // Strip ANSI escapes; the OLED console has its own palette.
    const clean = line.replace(/\[[0-9;]*m/g, "");
    out.appendChild(el("div", { class: cls }, clean));
    while (out.childElementCount > 2000) out.firstElementChild!.remove();
    if (atBottom) out.scrollTop = out.scrollHeight;
  }

  async function connect() {
    try {
      const creds = await client.websocket(ctx.id);
      ws = new WebSocket(creds.data.socket);
      ws.onopen = () => ws!.send(JSON.stringify({ event: "auth", args: [creds.data.token] }));
      ws.onmessage = (msg) => {
        let data: { event: string; args?: string[] };
        try { data = JSON.parse(msg.data); } catch { return; }
        switch (data.event) {
          case "auth success":
            print("[panel] connected to node", "line--daemon");
            ws!.send(JSON.stringify({ event: "send logs", args: [null] }));
            break;
          case "console output":
          case "install output":
            print(data.args?.[0] ?? "");
            break;
          case "daemon message":
            print(`[daemon] ${data.args?.[0] ?? ""}`, "line--daemon");
            break;
          case "daemon error":
            print(`[daemon] ${data.args?.[0] ?? ""}`, "line--err");
            break;
          case "status":
            setState(data.args?.[0] ?? "offline");
            break;
          case "stats":
            try {
              const s = JSON.parse(data.args?.[0] ?? "{}");
              renderStats({
                cpu_absolute: s.cpu_absolute ?? 0,
                memory_bytes: s.memory_bytes ?? 0,
                disk_bytes: s.disk_bytes ?? 0,
                uptime: s.uptime ?? 0,
              });
              if (s.state) setState(s.state);
            } catch { /* ignore malformed stats frame */ }
            break;
          case "token expiring":
            client.websocket(ctx.id).then((fresh) =>
              ws?.send(JSON.stringify({ event: "auth", args: [fresh.data.token] })));
            break;
          case "token expired":
            ws?.close();
            break;
        }
      };
      ws.onclose = () => {
        if (closed) return;
        print("[panel] connection to node lost — retrying in 5s", "line--err");
        setState("offline");
        setTimeout(() => { if (!closed) connect(); }, 5000);
      };
    } catch (err) {
      print(`[panel] node unreachable: ${(err as Error).message}`, "line--err");
      // REST fallback for stats so the tiles aren't dead.
      try {
        const stats = await client.resources(ctx.id);
        const a = stats.attributes as any;
        renderStats(a.resources);
        setState(a.current_state);
      } catch { /* full offline */ }
    }
  }

  const cmd = el("input.rst-console__cmd", {
    attrs: { placeholder: "Type a command…", "aria-label": "Console command", spellcheck: "false" },
    onkeydown: (e: KeyboardEvent) => {
      if (e.key !== "Enter") return;
      const value = (cmd as HTMLInputElement).value.trim();
      if (!value) return;
      (cmd as HTMLInputElement).value = "";
      if (ws && ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ event: "send command", args: [value] }));
      } else {
        client.command(ctx.id, value).catch((err) => notify.error(String(err.message)));
      }
    },
  }) as HTMLInputElement;

  const power = (signal: string, label: string, ic: string, variant: "default" | "danger" = "default") =>
    Button({
      label, icon: ic, size: "sm", variant,
      onClick: async () => {
        if (ws && ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ event: "set state", args: [signal] }));
        } else {
          try { await client.power(ctx.id, signal); notify.success(`Sent ${signal}`); }
          catch (err) { notify.error(String((err as Error).message)); }
        }
      },
    });

  const powerRow = el("div.rst-console__power",
    power("start", "Start", "play"),
    power("restart", "Restart", "rotate"),
    power("stop", "Stop", "stop"),
    power("kill", "Kill", "skull", "danger"),
  );

  connect();

  const root = page(
    el("span.row", { style: { gap: "10px", alignItems: "center" } }, dot, ctx.attrs.name as string, stateLabel),
    { sub: ctx.attrs.description || undefined, actions: [powerRow] },
    el("div.rst-console",
      el("div.rst-console__stats", statCPU, statMem, statDisk, statUptime),
      el("div.rst-console__term",
        out,
        el("div.rst-console__in", el("span.rst-console__prompt", icon("angle-right")), cmd),
      ),
    ),
  );

  const observer = new MutationObserver(() => {
    if (!root.isConnected) {
      closed = true;
      ws?.close();
      observer.disconnect();
    }
  });
  observer.observe(document.body, { childList: true, subtree: true });
  return root;
}
