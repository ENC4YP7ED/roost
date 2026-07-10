import { el, icon, clear } from "../core/dom.ts";
import { Button, IconButton } from "../components/Button.ts";
import { TextInput } from "../components/TextInput.ts";
import { openModal, confirmModal } from "../components/Modal.ts";
import { attachMenu, type MenuItem } from "../components/Menu.ts";
import { notify } from "../components/Toast.ts";
import { LoadingState, EmptyState } from "../components/misc.ts";
import { client, unwrap } from "../api/client.ts";
import { page, localTime } from "./shared.ts";
import { formatBytes } from "../util/format.ts";
import type { ServerCtx } from "./ServerView.ts";

interface FileEntry {
  name: string;
  mode: string;
  size: number;
  is_file: boolean;
  is_symlink: boolean;
  mimetype: string;
  modified_at: string;
}

const EDITABLE = /^(text\/|application\/(json|x-yaml|yaml|toml|xml|javascript|x-sh|x-properties))/;

/** Wings-backed file manager: browse, edit, rename, archive, delete. */
export function ServerFiles(ctx: ServerCtx): HTMLElement {
  let dir = "/";
  const listing = el("div", LoadingState("Loading directory…"));
  const crumb = el("div.rst-files__crumb");

  function renderCrumb() {
    clear(crumb);
    crumb.appendChild(el("button.rst-files__seg", { onclick: () => open("/") }, icon("house", { class: "faint" })));
    const parts = dir.split("/").filter(Boolean);
    let acc = "";
    for (const part of parts) {
      acc += "/" + part;
      const target = acc;
      crumb.append(el("span.faint", "/"), el("button.rst-files__seg", { onclick: () => open(target) }, part));
    }
  }

  async function open(path: string) {
    dir = path || "/";
    renderCrumb();
    listing.replaceChildren(LoadingState());
    try {
      const files = unwrap<FileEntry>(await client.files.list(ctx.id, dir));
      files.sort((a, b) => Number(a.is_file) - Number(b.is_file) || a.name.localeCompare(b.name));
      if (!files.length) {
        listing.replaceChildren(EmptyState({ icon: "folder-open", title: "Empty directory" }));
        return;
      }
      const table = el("table.rst-grid");
      table.appendChild(el("thead", el("tr",
        el("th", {}, "Name"), el("th", {}, "Size"), el("th", {}, "Mode"), el("th", {}, "Modified"), el("th"))));
      const tbody = el("tbody");
      for (const f of files) tbody.appendChild(fileRow(f));
      table.appendChild(tbody);
      listing.replaceChildren(el("div.rst-grid__scroll", table));
    } catch (err) {
      listing.replaceChildren(EmptyState({
        icon: "plug-circle-xmark",
        title: "Node unreachable",
        description: `The file manager needs the wings daemon. ${(err as Error).message}`,
      }));
    }
  }

  function joinPath(name: string): string {
    return (dir === "/" ? "" : dir) + "/" + name;
  }

  function fileRow(f: FileEntry): HTMLElement {
    const menuBtn = IconButton("ellipsis", { size: "sm", variant: "ghost", title: "Actions" });
    attachMenu(menuBtn, () => fileMenu(f), "bottom-end");
    const row = el("tr.rst-grid__row.rst-files__row", {
      onclick: () => {
        if (!f.is_file) open(joinPath(f.name));
        else if (EDITABLE.test(f.mimetype) || f.size < 1024 * 512) openEditor(f);
        else notify.info("Binary file — use Download from the row menu.");
      },
    },
      el("td.rst-grid__cell", el("span.row", { style: { gap: "8px" } },
        icon(f.is_file ? iconFor(f) : "folder", { class: `rst-fileicon${f.is_file ? "" : " rst-fileicon--dir"}` }),
        el("span", { class: f.is_file ? "" : "strong" }, f.name),
      )),
      el("td.rst-grid__cell.mono.faint", f.is_file ? formatBytes(f.size) : "—"),
      el("td.rst-grid__cell.mono.faint", f.mode),
      el("td.rst-grid__cell.faint", localTime(f.modified_at)),
      el("td.rst-grid__cell", { onclick: (e: Event) => e.stopPropagation() }, menuBtn),
    );
    return row;
  }

  function fileMenu(f: FileEntry): MenuItem[] {
    const path = joinPath(f.name);
    const items: MenuItem[] = [{ header: f.name }];
    if (f.is_file) {
      items.push({ label: "Edit", icon: "pen", onSelect: () => openEditor(f) });
      items.push({
        label: "Download", icon: "download",
        onSelect: async () => {
          const res = await client.files.downloadURL(ctx.id, path);
          window.open((res.attributes as { url: string }).url, "_blank");
        },
      });
    }
    if (ctx.can("file.update")) {
      items.push({
        label: "Rename", icon: "i-cursor",
        onSelect: () => {
          const input = TextInput({ label: "New name", value: f.name, autofocus: true });
          openModal({
            title: `Rename ${f.name}`, icon: "i-cursor", body: input.el,
            actions: [
              { label: "Cancel" },
              { label: "Rename", variant: "primary", onClick: async () => {
                await client.files.rename(ctx.id, dir, f.name, input.value);
                open(dir);
              } },
            ],
          });
        },
      });
    }
    if (ctx.can("file.archive")) {
      if (f.is_file && /\.(zip|tar|gz|tgz|rar|7z)$/i.test(f.name)) {
        items.push({ label: "Unarchive", icon: "box-open", onSelect: async () => {
          await client.files.decompress(ctx.id, dir, f.name);
          notify.success("Extracting…");
          setTimeout(() => open(dir), 1200);
        } });
      }
      items.push({ label: "Archive", icon: "file-zipper", onSelect: async () => {
        await client.files.compress(ctx.id, dir, [f.name]);
        notify.success("Archive created");
        open(dir);
      } });
    }
    if (ctx.can("file.delete")) {
      items.push({ separator: true });
      items.push({
        label: "Delete", icon: "trash", danger: true,
        onSelect: async () => {
          if (!(await confirmModal({ title: "Delete", message: `Permanently delete ${f.name}?`, danger: true }))) return;
          await client.files.remove(ctx.id, dir, [f.name]);
          open(dir);
        },
      });
    }
    return items;
  }

  function openEditor(f: FileEntry) {
    const path = joinPath(f.name);
    const area = el("textarea.rst-codeblock", {
      style: { width: "100%", minHeight: "50vh", resize: "vertical", whiteSpace: "pre", background: "var(--gray-0)" },
      attrs: { spellcheck: "false", "aria-label": `Editing ${f.name}` },
    }) as HTMLTextAreaElement;
    area.value = "Loading…";
    client.files.contents(ctx.id, path)
      .then((text) => { area.value = text; })
      .catch((err) => { area.value = `# failed to load: ${err.message}`; });
    openModal({
      title: path, icon: "file-pen", width: 900,
      body: area,
      actions: [
        { label: "Close" },
        {
          label: "Save", variant: "primary", icon: "floppy-disk", closeOnClick: false,
          onClick: async () => {
            try {
              await client.files.write(ctx.id, path, area.value);
              notify.success("Saved");
            } catch (err) { notify.error(String((err as Error).message)); }
          },
        },
      ],
    });
  }

  const newFolderBtn = Button({
    label: "New folder", icon: "folder-plus", size: "sm",
    onClick: () => {
      const input = TextInput({ label: "Folder name", autofocus: true });
      openModal({
        title: "Create folder", icon: "folder-plus", body: input.el,
        actions: [
          { label: "Cancel" },
          { label: "Create", variant: "primary", onClick: async () => {
            await client.files.mkdir(ctx.id, dir, input.value);
            open(dir);
          } },
        ],
      });
    },
  });

  const newFileBtn = Button({
    label: "New file", icon: "file-circle-plus", size: "sm",
    onClick: () => {
      const input = TextInput({ label: "File name", autofocus: true });
      openModal({
        title: "Create file", icon: "file-circle-plus", body: input.el,
        actions: [
          { label: "Cancel" },
          { label: "Create", variant: "primary", onClick: async () => {
            await client.files.write(ctx.id, joinPath(input.value), "");
            open(dir);
          } },
        ],
      });
    },
  });

  const refreshBtn = IconButton("rotate", { size: "sm", variant: "ghost", title: "Refresh", onClick: () => open(dir) });

  open("/");
  return page("Files", { icon: "folder-open", actions: ctx.can("file.create") ? [newFileBtn, newFolderBtn, refreshBtn] : [refreshBtn] },
    el("div.rst-files__bar", crumb),
    listing,
  );
}

function iconFor(f: FileEntry): string {
  if (/\.(zip|tar|gz|tgz|rar|7z)$/i.test(f.name)) return "file-zipper";
  if (/\.(jar)$/i.test(f.name)) return "cube";
  if (/\.(png|jpe?g|gif|webp|svg)$/i.test(f.name)) return "file-image";
  if (/\.(json|ya?ml|toml|properties|conf|cfg|ini)$/i.test(f.name)) return "file-code";
  if (/\.(log|txt|md)$/i.test(f.name)) return "file-lines";
  return "file";
}
