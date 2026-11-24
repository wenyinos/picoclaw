export interface MemoryEntry {
  id: string
  realm: string
  layer: string
  scope: string
  audience: string
  subject?: string
  predicate?: string
  facet?: string
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
  sources?: {
    source_type: string
    session_key?: string
    created_at: number
    evidence_text?: string
    user_confirmed?: boolean
  }[]
  domain_id?: string
  row_revision?: number
  pinned_core?: boolean
  sensitivity?: string
  evidence_status?: string
  freshness?: string
  relationship_keys?: string[]
}

export interface MemoryForgetPreview {
  entry_id: string
  content_hash: string
  sources: number
  relations: number
  cluster_ids?: string[]
  summaries: number
  already_gone: boolean
  relationship_keys?: string[]
  ledger_seq?: number
  request_id?: string
}

export interface MemoryLedgerHealth {
  available: boolean
  path?: string
  ledger_uuid?: string
  key_id?: string
  pending?: number
  reason?: string
}

export interface MemoryDomain {
  domain_id: string
  owner_principal_id?: string
  scope_key?: string
  status: string
  policy_epoch: number
  created_at: number
  updated_at: number
}

export interface MemorySummaryClaim {
  claim_id: string
  text: string
  entry_ids: string[]
}

export interface MemorySummaryVersion {
  id: number
  cluster_id: string
  domain_id?: string
  version: number
  generation: number
  summary: string
  claims?: MemorySummaryClaim[]
  confidence: number
  status: string
  created_at: number
}

export interface MemoryCluster {
  id: string
  realm: string
  title: string
  topic?: string
  status: string
  created_at: number
  updated_at: number
  generation?: number
}

export interface MemoryCommitment {
  id: string
  domain_id?: string
  text: string
  status: string
  due_at?: number
  due_timezone?: string
  revision: number
  created_at: number
  updated_at: number
}

export interface MemoryJob {
  job_id: string
  kind: string
  state: string
  attempts: number
  priority: number
  last_error?: string
}

export interface MemoryChangePlan {
  plan_id: string
  action: string
  target_id: string
  expected_revision: number
  status: string
  expires_at: number
  created_at: number
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
  realm: string
  audience: string
  scope: string
  created_at: number
}

export interface MemoryMigrationStatus {
  schema_version: number
  current_version: number
  needs_migration: boolean
  needs_schema_migration: boolean
  needs_data_migration: boolean
  empty_predicate: number
  empty_relationship_facet: number
  relationship: number
  personas: number
  pinned_core: number
  relations: number
  fts_rows: number
  fts_tokenizer?: string
  total: number
  active: number
  db_path: string
  db_size: number
  backup?: string
  last_migration_at?: number
  domains?: number
  origin_events?: number
  jobs?: number
  dependencies?: number
}

export interface MemoryMigrationReport {
  previous_schema_version: number
  schema_version: number
  total: number
  active: number
  empty_predicate: number
  updated_predicates: number
  mapped_relationships: number
  mapped_predicates: number
  reclassified: number
  restored: number
  backfilled_sources: number
  dropped_orphan_sources: number
  dropped_orphan_supersedes: number
  fts_rows: number
  fts_tokenizer: string
  backup: string
  sources_rebuilt?: number
  domains_created?: number
  entries_with_domain?: number
}

export interface MemoryRepairFilter {
  realm?: string
  layer?: string
  status?: string
  audience?: string
  query?: string
}

export interface MemoryRepairTarget {
  realm: string
  layer: string
  audience?: string
}

export interface MemoryRepairPreview {
  count: number
  entries: MemoryEntry[]
}

export interface MemoryRepairResult {
  updated: number
  backup: string
}

export interface MemorySettings {
  enabled: boolean
  db_path?: string
  budget_tokens?: number
  auto_extract: boolean
  extract_model?: string
  private_channel_exclusions?: string[]
  private_channels?: string[]
  available_channels?: string[]
  inject_fictional: boolean
  review_required: boolean
  episodic_retention_days?: number
  sensitivity_capture?: string
  ledger_path?: string
  summary_enabled?: boolean
  llm_extraction_enabled?: boolean
  semantic_retrieval_enabled?: boolean
  proactive_recall_enabled?: boolean
  proactive_daily_limit?: number
  proactive_topic_cooldown_days?: number
  allowed_chat_recipients?: string[]
  allow_tool_forwarding?: boolean
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
    request<{ status: string }>(`/api/memory/entries/${id}/archive`, {
      method: "POST",
    }),
  restoreEntry: (id: string) =>
    request<{ status: string }>(`/api/memory/entries/${id}/restore`, {
      method: "POST",
    }),
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
    request<{ personas: Persona[]; active: Persona | null }>(
      "/api/memory/persona",
    ),
  savePersona: (
    name: string,
    content: string,
    activate = true,
    realm = "real",
    audience = "private",
    scope = "user",
  ) =>
    request<Persona>("/api/memory/persona", {
      method: "PUT",
      body: JSON.stringify({
        id: "default",
        name,
        content,
        activate,
        realm,
        audience,
        scope,
      }),
    }),
  settings: () => request<MemorySettings>("/api/memory/settings"),
  saveSettings: (settings: MemorySettings) =>
    request<MemorySettings>("/api/memory/settings", {
      method: "PUT",
      body: JSON.stringify(settings),
    }),
  repairPreview: (filter: MemoryRepairFilter) =>
    request<MemoryRepairPreview>("/api/memory/repair/preview", {
      method: "POST",
      body: JSON.stringify(filter),
    }),
  repairApply: (filter: MemoryRepairFilter, target: MemoryRepairTarget) =>
    request<MemoryRepairResult>("/api/memory/repair/apply", {
      method: "POST",
      body: JSON.stringify({ filter, target }),
    }),
  migrationStatus: () =>
    request<MemoryMigrationStatus>("/api/memory/migration/status"),
  migrationApply: () =>
    request<MemoryMigrationReport>("/api/memory/migration/apply", {
      method: "POST",
    }),
  compact: () =>
    request<{ archived: number }>("/api/memory/compact", { method: "POST" }),
  importWorkspace: (workspace: string) =>
    request<Record<string, number>>("/api/memory/import", {
      method: "POST",
      body: JSON.stringify({ workspace }),
    }),

  // V4 governance and long-term understanding.
  inspectEntry: (id: string) =>
    request<{
      entry: MemoryEntry
      sources: MemoryEntry["sources"]
      relations: unknown[]
      operations: unknown[]
      clusters: string[]
    }>(`/api/memory/entries/${id}/inspect`),
  correctEntry: (id: string, body: Record<string, unknown>) =>
    request<{ action: string; entry?: MemoryEntry }>(
      `/api/memory/entries/${id}/correct`,
      { method: "POST", body: JSON.stringify(body) },
    ),
  forgetEntry: (id: string, dryRun = true, expectedRevision = 0) =>
    request<MemoryForgetPreview>(`/api/memory/entries/${id}/forget`, {
      method: "POST",
      body: JSON.stringify({
        dry_run: dryRun,
        confirm: !dryRun,
        expected_revision: expectedRevision,
      }),
    }),
  domains: () => request<{ domains: MemoryDomain[] }>("/api/memory/domains"),
  ledgerHealth: () => request<MemoryLedgerHealth>("/api/memory/ledger/health"),
  replayForget: () =>
    request<{ removed: number }>("/api/memory/ledger/replay", {
      method: "POST",
    }),
  clusters: () =>
    request<{ clusters: MemoryCluster[] }>("/api/memory/clusters"),
  createCluster: (body: Partial<MemoryCluster>) =>
    request<MemoryCluster>("/api/memory/clusters", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  addClusterMembers: (id: string, entryIds: string[]) =>
    request<{ added: number }>(`/api/memory/clusters/${id}/members`, {
      method: "POST",
      body: JSON.stringify({ entry_ids: entryIds }),
    }),
  generateSummary: (id: string) =>
    request<MemorySummaryVersion>(`/api/memory/clusters/${id}/summaries`, {
      method: "POST",
    }),
  summaries: (id: string) =>
    request<{ summaries: MemorySummaryVersion[] }>(
      `/api/memory/clusters/${id}/summaries`,
    ),
  summaryEvidence: (id: number) =>
    request<{ entries: MemoryEntry[] }>(`/api/memory/summaries/${id}/evidence`),
  commitments: (includeClosed = false) =>
    request<{ commitments: MemoryCommitment[] }>(
      `/api/memory/commitments?include_closed=${includeClosed}`,
    ),
  createCommitment: (body: Partial<MemoryCommitment>) =>
    request<MemoryCommitment>("/api/memory/commitments", {
      method: "POST",
      body: JSON.stringify(body),
    }),
  setCommitmentStatus: (id: string, status: string, evidence: string) =>
    request<MemoryCommitment>(`/api/memory/commitments/${id}/status`, {
      method: "POST",
      body: JSON.stringify({ status, evidence }),
    }),
  jobs: () =>
    request<{ jobs: MemoryJob[]; stats: Record<string, number> }>(
      "/api/memory/jobs",
    ),
  plans: (status = "pending") =>
    request<{ plans: MemoryChangePlan[] }>(
      `/api/memory/plans?status=${status}`,
    ),
  approvePlan: (id: string) =>
    request<MemoryChangePlan>(`/api/memory/plans/${id}/approve`, {
      method: "POST",
    }),
}
