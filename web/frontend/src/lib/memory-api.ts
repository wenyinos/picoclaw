export interface MemoryEntry {
  id: string
  realm: string
  layer: string
  scope: string
  audience: string
  subject?: string
  predicate?: string
  value?: string
  text: string
  importance: number
  emotional_weight: number
  confidence: number
  status: string
  expires_at?: number
  created_at: number
  updated_at: number
  occurrences: number
  sources?: { source_type: string; session_key?: string; created_at: number }[]
}

export interface MemoryOverview {
  total: number
  active: number
  pending: number
  superseded: number
  archived: number
  injected_tokens: number
  revision: number
  db_size: number
}

export interface RelationshipValue {
  key: string
  value: string
  valid_from: number
  confidence: number
}

export interface Persona {
  id: string
  name: string
  version: number
  content: string
  active: boolean
  created_at: number
}

export interface MemorySettings {
  enabled: boolean
  db_path?: string
  budget_tokens?: number
  auto_extract: boolean
  extract_model?: string
  private_channels?: string[]
  inject_fictional: boolean
  review_required: boolean
  episodic_retention_days?: number
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { "Content-Type": "application/json", ...(init?.headers || {}) },
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || `request failed: ${res.status}`)
  }
  return (await res.json()) as T
}

export const memoryApi = {
  overview: () => request<MemoryOverview>("/api/memory/overview"),
  entries: (params: Record<string, string> = {}) => {
    const q = new URLSearchParams(params).toString()
    return request<{ entries: MemoryEntry[] }>(`/api/memory/entries?${q}`)
  },
  addEntry: (body: Partial<MemoryEntry>) =>
    request<{ action: string; entry?: MemoryEntry }>("/api/memory/entries", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  updateEntry: (id: string, body: Partial<MemoryEntry>) =>
    request<MemoryEntry>(`/api/memory/entries/${id}`, {
      method: "PUT",
      body: JSON.stringify(body),
    }),
  archiveEntry: (id: string) =>
    request<{ status: string }>(`/api/memory/entries/${id}/archive`, { method: "POST" }),
  restoreEntry: (id: string) =>
    request<{ status: string }>(`/api/memory/entries/${id}/restore`, { method: "POST" }),
  review: (kind = "pending") =>
    request<{ entries: MemoryEntry[] }>(`/api/memory/review?kind=${kind}`),
  resolveReview: (id: string, action: string, targetId?: string) =>
    request<{ status: string }>(`/api/memory/review/${id}/resolve`, {
      method: "POST",
      body: JSON.stringify({ action, target_id: targetId }),
    }),
  relationship: () =>
    request<{ values: RelationshipValue[] }>("/api/memory/relationship"),
  setRelationship: (key: string, value: string, confidence = 0.9) =>
    request<{ status: string }>("/api/memory/relationship", {
      method: "PUT",
      body: JSON.stringify({ key, value, confidence }),
    }),
  persona: () =>
    request<{ personas: Persona[]; active: Persona | null }>("/api/memory/persona"),
  savePersona: (name: string, content: string, activate = true) =>
    request<Persona>("/api/memory/persona", {
      method: "PUT",
      body: JSON.stringify({ id: "default", name, content, activate }),
    }),
  settings: () => request<MemorySettings>("/api/memory/settings"),
  saveSettings: (settings: MemorySettings) =>
    request<MemorySettings>("/api/memory/settings", {
      method: "PUT",
      body: JSON.stringify(settings),
    }),
  compact: () => request<{ archived: number }>("/api/memory/compact", { method: "POST" }),
  importWorkspace: (workspace: string) =>
    request<Record<string, number>>("/api/memory/import", {
      method: "POST",
      body: JSON.stringify({ workspace }),
    }),
}
