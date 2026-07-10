/**
 * Typed client for the Roost API. All endpoints return Pterodactyl-shaped
 * envelopes: lists as {object:"list",data:[{object,attributes}]}, resources
 * as {object,attributes}, errors as {errors:[{code,status,detail}]}.
 */

export interface ApiItem<T = Record<string, unknown>> {
  object: string;
  attributes: T;
}

export interface ApiList<T = Record<string, unknown>> {
  object: "list";
  data: ApiItem<T>[];
  meta?: { pagination?: { total: number } };
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body !== undefined ? { "Content-Type": "application/json" } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  let json: any = null;
  try { json = text ? JSON.parse(text) : null; } catch { /* non-JSON */ }
  if (!res.ok) {
    const detail = json?.errors?.[0]?.detail ?? `HTTP ${res.status}`;
    throw new ApiError(res.status, detail);
  }
  return json as T;
}

export const http = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  put: <T>(path: string, body?: unknown) => request<T>("PUT", path, body),
  patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),
};

/** Unwraps list envelopes into plain attribute arrays. */
export function unwrap<T>(list: { data?: Array<{ attributes: unknown }> }): T[] {
  return (list.data ?? []).map((d) => d.attributes as T);
}

// ---- auth ----

export const auth = {
  login: (user: string, password: string, captcha_tokens?: Record<string, string>) =>
    http.post<{ data: { complete: boolean; confirmation_token?: string; user?: any } }>(
      "/auth/login", { user, password, captcha_tokens }),
  checkpoint: (confirmation_token: string, code: string, recovery = false) =>
    http.post<{ data: { complete: boolean; user?: any } }>("/auth/login/checkpoint",
      recovery
        ? { confirmation_token, recovery_token: code }
        : { confirmation_token, authentication_code: code }),
  logout: () => http.post("/auth/logout"),
  forgot: (email: string) => http.post("/auth/password", { email }),
  reset: (email: string, token: string, password: string) =>
    http.post("/auth/password/reset", { email, token, password }),
};

// ---- client area ----

export const client = {
  account: () => http.get<ApiItem>("/api/client/account"),
  servers: (type = "") => http.get<ApiList>(`/api/client${type ? `?type=${type}` : ""}`),
  server: (id: string) => http.get<ApiItem & { meta: any }>(`/api/client/servers/${id}`),
  resources: (id: string) => http.get<ApiItem>(`/api/client/servers/${id}/resources`),
  websocket: (id: string) => http.get<{ data: { token: string; socket: string } }>(`/api/client/servers/${id}/websocket`),
  power: (id: string, signal: string) => http.post(`/api/client/servers/${id}/power`, { signal }),
  command: (id: string, command: string) => http.post(`/api/client/servers/${id}/command`, { command }),
  activity: (id: string) => http.get<ApiList>(`/api/client/servers/${id}/activity`),

  files: {
    list: (id: string, dir: string) => http.get<ApiList>(`/api/client/servers/${id}/files/list?directory=${encodeURIComponent(dir)}`),
    contents: async (id: string, file: string): Promise<string> => {
      const res = await fetch(`/api/client/servers/${id}/files/contents?file=${encodeURIComponent(file)}`);
      if (!res.ok) throw new ApiError(res.status, `Failed to load file (HTTP ${res.status})`);
      return res.text();
    },
    write: (id: string, file: string, content: string) =>
      fetch(`/api/client/servers/${id}/files/write?file=${encodeURIComponent(file)}`, { method: "POST", body: content })
        .then((r) => { if (!r.ok) throw new ApiError(r.status, "Failed to save file"); }),
    rename: (id: string, root: string, from: string, to: string) =>
      http.put(`/api/client/servers/${id}/files/rename`, { root, files: [{ from, to }] }),
    copy: (id: string, location: string) => http.post(`/api/client/servers/${id}/files/copy`, { location }),
    remove: (id: string, root: string, files: string[]) => http.post(`/api/client/servers/${id}/files/delete`, { root, files }),
    mkdir: (id: string, root: string, name: string) => http.post(`/api/client/servers/${id}/files/create-folder`, { root, name }),
    compress: (id: string, root: string, files: string[]) => http.post<ApiItem>(`/api/client/servers/${id}/files/compress`, { root, files }),
    decompress: (id: string, root: string, file: string) => http.post(`/api/client/servers/${id}/files/decompress`, { root, file }),
    downloadURL: (id: string, file: string) => http.get<ApiItem>(`/api/client/servers/${id}/files/download?file=${encodeURIComponent(file)}`),
  },

  databases: {
    list: (id: string) => http.get<ApiList>(`/api/client/servers/${id}/databases?include=password`),
    create: (id: string, database: string, remote: string) => http.post<ApiItem>(`/api/client/servers/${id}/databases`, { database, remote }),
    rotate: (id: string, dbId: string) => http.post<ApiItem>(`/api/client/servers/${id}/databases/${dbId}/rotate-password`),
    remove: (id: string, dbId: string) => http.del(`/api/client/servers/${id}/databases/${dbId}`),
  },

  schedules: {
    list: (id: string) => http.get<ApiList>(`/api/client/servers/${id}/schedules`),
    create: (id: string, body: unknown) => http.post<ApiItem>(`/api/client/servers/${id}/schedules`, body),
    update: (id: string, sid: number, body: unknown) => http.post<ApiItem>(`/api/client/servers/${id}/schedules/${sid}`, body),
    remove: (id: string, sid: number) => http.del(`/api/client/servers/${id}/schedules/${sid}`),
    execute: (id: string, sid: number) => http.post(`/api/client/servers/${id}/schedules/${sid}/execute`),
    createTask: (id: string, sid: number, body: unknown) => http.post<ApiItem>(`/api/client/servers/${id}/schedules/${sid}/tasks`, body),
    updateTask: (id: string, sid: number, tid: number, body: unknown) => http.post<ApiItem>(`/api/client/servers/${id}/schedules/${sid}/tasks/${tid}`, body),
    removeTask: (id: string, sid: number, tid: number) => http.del(`/api/client/servers/${id}/schedules/${sid}/tasks/${tid}`),
  },

  network: {
    list: (id: string) => http.get<ApiList>(`/api/client/servers/${id}/network/allocations`),
    create: (id: string) => http.post<ApiItem>(`/api/client/servers/${id}/network/allocations`),
    notes: (id: string, aid: number, notes: string) => http.post<ApiItem>(`/api/client/servers/${id}/network/allocations/${aid}`, { notes }),
    primary: (id: string, aid: number) => http.post<ApiItem>(`/api/client/servers/${id}/network/allocations/${aid}/primary`),
    remove: (id: string, aid: number) => http.del(`/api/client/servers/${id}/network/allocations/${aid}`),
  },

  subusers: {
    list: (id: string) => http.get<ApiList>(`/api/client/servers/${id}/users`),
    create: (id: string, email: string, permissions: string[]) => http.post<ApiItem>(`/api/client/servers/${id}/users`, { email, permissions }),
    update: (id: string, uuid: string, permissions: string[]) => http.post<ApiItem>(`/api/client/servers/${id}/users/${uuid}`, { permissions }),
    remove: (id: string, uuid: string) => http.del(`/api/client/servers/${id}/users/${uuid}`),
  },

  backups: {
    list: (id: string) => http.get<ApiList>(`/api/client/servers/${id}/backups`),
    create: (id: string, body: unknown) => http.post<ApiItem>(`/api/client/servers/${id}/backups`, body),
    lock: (id: string, uuid: string) => http.post<ApiItem>(`/api/client/servers/${id}/backups/${uuid}/lock`),
    restore: (id: string, uuid: string, truncate: boolean) => http.post(`/api/client/servers/${id}/backups/${uuid}/restore`, { truncate }),
    remove: (id: string, uuid: string) => http.del(`/api/client/servers/${id}/backups/${uuid}`),
    downloadURL: (id: string, uuid: string) => http.get<ApiItem>(`/api/client/servers/${id}/backups/${uuid}/download`),
  },

  startup: {
    get: (id: string) => http.get<ApiList & { meta: any }>(`/api/client/servers/${id}/startup`),
    setVariable: (id: string, key: string, value: string) => http.put<ApiItem>(`/api/client/servers/${id}/startup/variable`, { key, value }),
  },

  settings: {
    rename: (id: string, name: string, description?: string) => http.post(`/api/client/servers/${id}/settings/rename`, { name, description }),
    reinstall: (id: string) => http.post(`/api/client/servers/${id}/settings/reinstall`),
    dockerImage: (id: string, docker_image: string) => http.put(`/api/client/servers/${id}/settings/docker-image`, { docker_image }),
  },

  permissions: () => http.get<ApiItem<{ permissions: string[] }>>("/api/client/permissions"),

  accountApi: {
    updateEmail: (email: string, password: string) => http.put("/api/client/account/email", { email, password }),
    updatePassword: (current_password: string, password: string) => http.put("/api/client/account/password", { current_password, password }),
    twoFactorSetup: () => http.get<{ data: { image_url_data: string; secret: string } }>("/api/client/account/two-factor"),
    twoFactorEnable: (code: string, password: string) => http.post<ApiItem<{ tokens: string[] }>>("/api/client/account/two-factor", { code, password }),
    twoFactorDisable: (password: string) => http.post("/api/client/account/two-factor/disable", { password }),
    activity: () => http.get<ApiList>("/api/client/account/activity"),
    apiKeys: () => http.get<ApiList>("/api/client/account/api-keys"),
    createApiKey: (description: string, allowed_ips: string[]) =>
      http.post<ApiItem & { meta: { secret_token: string } }>("/api/client/account/api-keys", { description, allowed_ips }),
    deleteApiKey: (identifier: string) => http.del(`/api/client/account/api-keys/${identifier}`),
    sshKeys: () => http.get<ApiList>("/api/client/account/ssh-keys"),
    createSshKey: (name: string, public_key: string) => http.post<ApiItem>("/api/client/account/ssh-keys", { name, public_key }),
    deleteSshKey: (fingerprint: string) => http.post("/api/client/account/ssh-keys/remove", { fingerprint }),
  },
};

// ---- application (admin) area ----

export const admin = {
  overview: () => http.get<Record<string, unknown>>("/api/application/overview"),
  settings: () => http.get<Record<string, string>>("/api/application/settings"),
  saveSettings: (body: Record<string, string>) => http.patch("/api/application/settings", body),

  users: {
    list: (filter = "") => http.get<ApiList>(`/api/application/users${filter ? `?filter=${encodeURIComponent(filter)}` : ""}`),
    create: (body: unknown) => http.post<ApiItem>("/api/application/users", body),
    update: (id: number, body: unknown) => http.patch<ApiItem>(`/api/application/users/${id}`, body),
    remove: (id: number) => http.del(`/api/application/users/${id}`),
  },

  locations: {
    list: () => http.get<ApiList>("/api/application/locations"),
    create: (short: string, long: string) => http.post<ApiItem>("/api/application/locations", { short, long }),
    update: (id: number, body: unknown) => http.patch<ApiItem>(`/api/application/locations/${id}`, body),
    remove: (id: number) => http.del(`/api/application/locations/${id}`),
  },

  nodes: {
    list: () => http.get<ApiList>("/api/application/nodes"),
    get: (id: number) => http.get<ApiItem>(`/api/application/nodes/${id}`),
    create: (body: unknown) => http.post<ApiItem>("/api/application/nodes", body),
    update: (id: number, body: unknown) => http.patch<ApiItem>(`/api/application/nodes/${id}`, body),
    remove: (id: number) => http.del(`/api/application/nodes/${id}`),
    configuration: (id: number) => http.get<Record<string, unknown>>(`/api/application/nodes/${id}/configuration`),
    resetToken: (id: number) => http.post<ApiItem>(`/api/application/nodes/${id}/reset-token`),
    allocations: (id: number) => http.get<ApiList>(`/api/application/nodes/${id}/allocations`),
    createAllocations: (id: number, ip: string, ports: string[], alias?: string) =>
      http.post(`/api/application/nodes/${id}/allocations`, { ip, ports, alias }),
    removeAllocation: (id: number, aid: number) => http.del(`/api/application/nodes/${id}/allocations/${aid}`),
  },

  servers: {
    list: (filter = "") => http.get<ApiList>(`/api/application/servers${filter ? `?filter=${encodeURIComponent(filter)}` : ""}`),
    get: (id: number) => http.get<ApiItem>(`/api/application/servers/${id}`),
    create: (body: unknown) => http.post<ApiItem>("/api/application/servers", body),
    details: (id: number, body: unknown) => http.patch<ApiItem>(`/api/application/servers/${id}/details`, body),
    build: (id: number, body: unknown) => http.patch<ApiItem>(`/api/application/servers/${id}/build`, body),
    startup: (id: number, body: unknown) => http.patch<ApiItem>(`/api/application/servers/${id}/startup`, body),
    suspend: (id: number) => http.post(`/api/application/servers/${id}/suspend`),
    unsuspend: (id: number) => http.post(`/api/application/servers/${id}/unsuspend`),
    reinstall: (id: number) => http.post(`/api/application/servers/${id}/reinstall`),
    remove: (id: number, force = false) => http.del(`/api/application/servers/${id}${force ? "/force" : ""}`),
  },

  nests: {
    list: () => http.get<ApiList>("/api/application/nests"),
    create: (name: string, description: string) => http.post<ApiItem>("/api/application/nests", { name, description }),
    update: (id: number, body: unknown) => http.patch<ApiItem>(`/api/application/nests/${id}`, body),
    remove: (id: number) => http.del(`/api/application/nests/${id}`),
    eggs: (id: number) => http.get<ApiList>(`/api/application/nests/${id}/eggs`),
    egg: (id: number, eid: number) => http.get<ApiItem>(`/api/application/nests/${id}/eggs/${eid}`),
    updateEgg: (id: number, eid: number, body: unknown) => http.patch<ApiItem>(`/api/application/nests/${id}/eggs/${eid}`, body),
    removeEgg: (id: number, eid: number) => http.del(`/api/application/nests/${id}/eggs/${eid}`),
    importEgg: (id: number, doc: unknown) => http.post<ApiItem>(`/api/application/nests/${id}/eggs/import`, doc),
    importEggURL: (id: number, url: string) => http.post<ApiItem>(`/api/application/nests/${id}/eggs/import-url`, { url }),
    exportURL: (id: number, eid: number) => `/api/application/nests/${id}/eggs/${eid}/export`,
    createVariable: (id: number, eid: number, body: unknown) => http.post<ApiItem>(`/api/application/nests/${id}/eggs/${eid}/variables`, body),
    updateVariable: (id: number, eid: number, vid: number, body: unknown) => http.patch<ApiItem>(`/api/application/nests/${id}/eggs/${eid}/variables/${vid}`, body),
    removeVariable: (id: number, eid: number, vid: number) => http.del(`/api/application/nests/${id}/eggs/${eid}/variables/${vid}`),
  },

  databaseHosts: {
    list: () => http.get<ApiList>("/api/application/database-hosts"),
    create: (body: unknown) => http.post<ApiItem>("/api/application/database-hosts", body),
    update: (id: number, body: unknown) => http.patch<ApiItem>(`/api/application/database-hosts/${id}`, body),
    remove: (id: number) => http.del(`/api/application/database-hosts/${id}`),
  },

  mounts: {
    list: () => http.get<ApiList>("/api/application/mounts"),
    create: (body: unknown) => http.post<ApiItem>("/api/application/mounts", body),
    update: (id: number, body: unknown) => http.patch<ApiItem>(`/api/application/mounts/${id}`, body),
    remove: (id: number) => http.del(`/api/application/mounts/${id}`),
  },

  webhooks: {
    list: () => http.get<{ data: Array<{ id: number; url: string; events: string[] }> }>("/api/application/webhooks"),
    save: (hooks: Array<{ url: string; events: string[] }>) => http.put("/api/application/webhooks", hooks),
  },

  tls: {
    get: () => http.get<{
      enabled: boolean; domain: string; email: string; staging: boolean; active: boolean;
      certificate_issued?: boolean; expires_at?: string; days_remaining?: number; error?: string;
    }>("/api/application/tls"),
    save: (body: { enabled: boolean; domain: string; email: string; staging: boolean }) =>
      http.put<{ restart_required: boolean }>("/api/application/tls", body),
    request: () => http.post<{ certificate_issued: boolean; expires_at: string }>("/api/application/tls/request"),
  },

  captcha: {
    list: () => http.get<{ data: Array<{ id: number; provider: string; mode: string; site_key: string; secret?: string }> }>("/api/application/captcha"),
    save: (layers: Array<{ provider: string; mode: string; site_key: string; secret: string }>) => http.put("/api/application/captcha", layers),
  },
};
