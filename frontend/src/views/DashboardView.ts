import { el, icon } from "../core/dom.ts";
import { Segmented, LoadingState, EmptyState, Badge } from "../components/misc.ts";
import { client, unwrap } from "../api/client.ts";
import { navigate, store, savePrefs } from "../state/store.ts";
import { page, meter, statusDot, memText, cpuText } from "./shared.ts";
import { formatBytes } from "../util/format.ts";

interface ServerAttrs {
  identifier: string;
  name: string;
  description: string;
  node: string;
  status: string | null;
  limits: { memory: number; disk: number; cpu: number };
  relationships: { allocations: { data: Array<{ attributes: { ip: string; ip_alias?: string; port: number; is_default: boolean } }> } };
}

/** Server list with live resource polling. */
export function DashboardView(): HTMLElement {
  const body = el("div", LoadingState("Loading your servers…"));
  const pollers: number[] = [];

  const layoutToggle = Segmented({
    options: [
      { value: "grid", icon: "table-cells-large", title: "Grid" },
      { value: "list", icon: "list", title: "List" },
    ],
    value: store.prefs.peek().serverLayout,
    onChange: (v) => { savePrefs({ serverLayout: v as "grid" | "list" }); load(); },
  });

  async function load() {
    for (const p of pollers.splice(0)) clearInterval(p);
    try {
      const servers = unwrap<ServerAttrs>(await client.servers());
      if (!servers.length) {
        body.replaceChildren(EmptyState({
          icon: "server",
          title: "No servers yet",
          description: "Servers you own or have been invited to will appear here.",
        }));
        return;
      }
      const wrap = el("div", { class: `rst-servers--${store.prefs.peek().serverLayout}` });
      for (const s of servers) wrap.appendChild(ServerCard(s, pollers));
      body.replaceChildren(wrap);
    } catch (err) {
      body.replaceChildren(EmptyState({ icon: "triangle-exclamation", title: "Failed to load servers", description: String((err as Error).message) }));
    }
  }

  load();
  const root = page("Your servers", { icon: "server", actions: [layoutToggle] }, body);
  // Stop polling when the view is detached (route change swaps content).
  const observer = new MutationObserver(() => {
    if (!root.isConnected) {
      for (const p of pollers.splice(0)) clearInterval(p);
      observer.disconnect();
    }
  });
  observer.observe(document.body, { childList: true, subtree: true });
  return root;
}

function ServerCard(s: ServerAttrs, pollers: number[]): HTMLElement {
  const alloc = s.relationships.allocations.data.find((a) => a.attributes.is_default)?.attributes;
  const addr = alloc ? `${alloc.ip_alias || alloc.ip}:${alloc.port}` : "no allocation";

  const dot = statusDot("offline");
  const cpuMeter = el("div");
  const memMeter = el("div");
  const diskMeter = el("div");
  render(0, 0, 0, "offline");

  function render(cpu: number, mem: number, disk: number, state: string) {
    dot.className = `rst-statusdot rst-statusdot--${state}`;
    cpuMeter.replaceChildren(meter("CPU", cpuText(cpu, s.limits.cpu), s.limits.cpu > 0 ? cpu / s.limits.cpu : cpu / 400));
    memMeter.replaceChildren(meter("RAM", memText(mem, s.limits.memory), s.limits.memory > 0 ? mem / (s.limits.memory * 1024 * 1024) : 0.05));
    diskMeter.replaceChildren(meter("Disk", `${formatBytes(disk)} / ${formatBytes(s.limits.disk * 1024 * 1024)}`, s.limits.disk > 0 ? disk / (s.limits.disk * 1024 * 1024) : 0));
  }

  async function poll() {
    try {
      const stats = await client.resources(s.identifier);
      const a = stats.attributes as any;
      render(a.resources.cpu_absolute, a.resources.memory_bytes, a.resources.disk_bytes, a.current_state);
    } catch { /* panel offline mid-session; keep last values */ }
  }
  poll();
  pollers.push(window.setInterval(poll, 10_000));

  const badge = s.status
    ? Badge(s.status.replace("_", " "), s.status === "suspended" || s.status === "install_failed" ? "danger" : "warning")
    : null;

  return el("button.rst-servercard", { onclick: () => navigate({ kind: "server", id: s.identifier, tab: "console" }) },
    el("div.rst-servercard__head",
      dot,
      el("div.grow", { style: { minWidth: "0" } },
        el("div.rst-servercard__name", s.name),
        el("div.rst-servercard__addr", [icon("network-wired", { class: "faint" }), " ", addr, s.node ? `  ·  ${s.node}` : ""]),
      ),
      badge,
    ),
    el("div.rst-servercard__meters", cpuMeter, memMeter, diskMeter),
  );
}
