/** Trigger a browser download of in-memory text content. */
export function download(filename: string, content: string, mime = "text/plain"): void {
  const blob = new Blob([content], { type: `${mime};charset=utf-8` });
  const url = URL.createObjectURL(blob);
  triggerDownload(url, filename);
  URL.revokeObjectURL(url);
}

/** Trigger a browser download straight from a URL — the browser streams the
 *  response to disk, so the file is never buffered in JS (any size). */
export function downloadUrl(url: string, filename?: string): void {
  triggerDownload(url, filename);
}

function triggerDownload(url: string, filename?: string): void {
  const a = document.createElement("a");
  a.href = url;
  if (filename) a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
}
