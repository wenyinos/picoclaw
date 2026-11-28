import { useEffect, useMemo, useRef, useState } from "react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  type MemoryChangePlan,
  type MemoryCluster,
  type MemoryCommitment,
  type MemoryDomain,
  type MemoryEntry,
  type MemoryJob,
  type MemoryLedgerHealth,
  type MemoryMigrationReport,
  type MemoryMigrationStatus,
  type MemoryOverview,
  type MemoryRecoveryStatus,
  type MemoryRepairFilter,
  type MemoryRepairPreview,
  type MemoryRepairTarget,
  type MemorySettings,
  type MemorySummaryVersion,
  type Persona,
  type RelationshipValue,
  memoryApi,
} from "@/lib/memory-api"

const TABS = [
  "overview",
  "migration",
  "entries",
  "review",
  "relationship",
  "governance",
  "settings",
  "repair",
] as const
type Tab = (typeof TABS)[number]

const TAB_LABELS: Record<Tab, string> = {
  overview: "概览",
  migration: "数据库迁移",
  entries: "记忆库",
  review: "审核",
  relationship: "关系与人格",
  governance: "治理与运行",
  settings: "设置",
  repair: "数据库修复",
}

const PAGE_SIZE = 100

function fmtDate(ms?: number) {
  if (!ms) return "-"
  return new Date(ms).toLocaleString()
}

function badge(value: string) {
  return (
    <span className="bg-muted text-muted-foreground inline-block max-w-full truncate rounded-full px-2 py-0.5 align-bottom text-[11px] whitespace-nowrap">
      {value}
    </span>
  )
}

export function MemoryPage() {
  const [tab, setTab] = useState<Tab>("overview")
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")

  return (
    <div className="min-h-0 flex-1 [scrollbar-gutter:stable] overflow-y-auto">
      <div className="mx-auto flex w-full max-w-6xl flex-col gap-6 p-6">
        <div>
          <h1 className="text-2xl font-semibold">记忆</h1>
          <p className="text-muted-foreground mt-1 text-sm">
            管理长期记忆、关系状态、角色设定和注入预算。
          </p>
        </div>

        <div className="flex flex-wrap gap-2 border-b pb-3">
          {TABS.map((t) => (
            <button
              key={t}
              onClick={() => setTab(t)}
              className={`rounded-md px-3 py-1.5 text-sm transition-colors ${
                tab === t
                  ? "bg-primary text-primary-foreground"
                  : "hover:bg-muted"
              }`}
            >
              {TAB_LABELS[t]}
            </button>
          ))}
        </div>

        {error && (
          <div className="rounded-md bg-red-500/10 p-3 text-sm text-red-500">
            {error}
          </div>
        )}
        {notice && (
          <div className="rounded-md bg-emerald-500/10 p-3 text-sm text-emerald-600">
            {notice}
          </div>
        )}

        {tab === "overview" && <OverviewTab onError={setError} />}
        {tab === "migration" && (
          <MigrationTab onError={setError} onNotice={setNotice} />
        )}
        {tab === "entries" && (
          <EntriesTab onError={setError} onNotice={setNotice} />
        )}
        {tab === "review" && (
          <ReviewTab onError={setError} onNotice={setNotice} />
        )}
        {tab === "relationship" && (
          <RelationshipTab onError={setError} onNotice={setNotice} />
        )}
        {tab === "governance" && (
          <GovernanceTab onError={setError} onNotice={setNotice} />
        )}
        {tab === "settings" && (
          <SettingsTab onError={setError} onNotice={setNotice} />
        )}
        {tab === "repair" && (
          <RepairTab onError={setError} onNotice={setNotice} />
        )}
      </div>
    </div>
  )
}

function GovernanceTab({
  onError,
  onNotice,
}: {
  onError: (v: string) => void
  onNotice: (v: string) => void
}) {
  const [health, setHealth] = useState<MemoryLedgerHealth | null>(null)
  const [recovery, setRecovery] = useState<MemoryRecoveryStatus | null>(null)
  const [plans, setPlans] = useState<MemoryChangePlan[]>([])
  const [jobStats, setJobStats] = useState<Record<string, number>>({})
  const [jobReceipts, setJobReceipts] = useState<
    Record<string, Record<string, number>>
  >({})
  const [jobs, setJobs] = useState<MemoryJob[]>([])
  const [domains, setDomains] = useState<MemoryDomain[]>([])
  const [commitments, setCommitments] = useState<MemoryCommitment[]>([])
  const [clusters, setClusters] = useState<MemoryCluster[]>([])
  const [summaries, setSummaries] = useState<
    Record<string, MemorySummaryVersion[]>
  >({})

  const load = async () => {
    const [h, r, p, j, d, c, cl] = await Promise.all([
      memoryApi.ledgerHealth(),
      memoryApi.recovery(),
      memoryApi.plans("pending"),
      memoryApi.jobs(),
      memoryApi.domains(),
      memoryApi.commitments(true),
      memoryApi.clusters(),
    ])
    setHealth(h)
    setRecovery(r)
    setPlans(p.plans)
    setJobs(j.jobs)
    setJobStats(j.stats)
    setJobReceipts(j.receipts ?? {})
    setDomains(d.domains)
    setCommitments(c.commitments)
    setClusters(cl.clusters)
  }

  useEffect(() => {
    load().catch((e: Error) => onError(e.message))
  }, [])

  const requeue = async (job: MemoryJob) => {
    if (
      !window.confirm(
        `为 ${job.job_id} 创建新的清理后继任务？原任务会保留为 superseded，清理回执和账本义务不变。`,
      )
    ) {
      return
    }
    try {
      const next = await memoryApi.requeueJob(job.job_id)
      onNotice(`已创建后继任务 ${next.job_id}`)
      await load()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  const approve = async (id: string) => {
    try {
      await memoryApi.approvePlan(id)
      onNotice(`计划 ${id} 已执行`)
      await load()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  const replay = async () => {
    try {
      const preview = await memoryApi.replayForgetPreview()
      const summary =
        `可执行 ${preview.eligible} 个意图，删除 ${preview.entries_removable} 条目标；` +
        `blocked ${preview.blocked}，未批准 ${preview.unapproved}，不支持 ${preview.unsupported}。`
      if (preview.eligible === 0) {
        onNotice(`没有可重放意图：${summary}`)
        return
      }
      if (
        !window.confirm(
          `${summary}\n\nblocked 意图不会执行，必须经新预览生成后继维护动作。确认执行可重放部分？`,
        )
      ) {
        return
      }
      const r = await memoryApi.replayForget()
      onNotice(
        `账本重放完成：删除 ${r.removed} 条复活数据，` +
          `blocked ${r.blocked}，未批准 ${r.unapproved}，不支持 ${r.unsupported}`,
      )
      await load()
    } catch (e) {
      onError((e as Error).message)
    }
  }

  const loadSummaries = async (clusterID: string) => {
    try {
      const r = await memoryApi.summaries(clusterID)
      setSummaries((prev) => ({ ...prev, [clusterID]: r.summaries }))
    } catch (e) {
      onError((e as Error).message)
    }
  }

  const generate = async (clusterID: string) => {
    try {
      await memoryApi.generateSummary(clusterID)
      onNotice("摘要已生成并绑定证据")
      await loadSummaries(clusterID)
    } catch (e) {
      onError((e as Error).message)
    }
  }

  return (
    <div className="flex flex-col gap-6">
      <section className="rounded-lg border p-4">
        <h2 className="mb-3 text-sm font-semibold">恢复门禁</h2>
        {!recovery ? (
          <p className="text-muted-foreground text-sm">加载中…</p>
        ) : recovery.ready ? (
          <p className="text-sm text-emerald-600">
            长期记忆已就绪：账本、清理回执和域注册检查通过。
          </p>
        ) : (
          <div className="flex flex-col gap-2 text-sm">
            <p className="text-amber-600">
              长期记忆已故障关闭；普通聊天可继续，但不会注入或写入长期记忆。
            </p>
            <div className="text-muted-foreground text-xs break-all">
              {recovery.issues?.join(" · ") || "unknown readiness issue"}
            </div>
            <div className="grid grid-cols-2 gap-1 text-xs sm:grid-cols-4">
              <span>pending intents: {recovery.pending_intents}</span>
              <span>blocked intents: {recovery.blocked_intents}</span>
              <span>pending cleanup: {recovery.pending_cleanup}</span>
              <span>blocked cleanup: {recovery.blocked_cleanup}</span>
              <span>cancelled cleanup: {recovery.inert_cleanup}</span>
              <span>orphan entries: {recovery.orphan_entries}</span>
              <span>schema: {recovery.schema_ready ? "ok" : "blocked"}</span>
              <span>ledger: {recovery.ledger_ready ? "ok" : "blocked"}</span>
            </div>
          </div>
        )}
      </section>

      <section className="rounded-lg border p-4">
        <h2 className="mb-3 text-sm font-semibold">遗忘账本</h2>
        {!health ? (
          <p className="text-muted-foreground text-sm">加载中…</p>
        ) : health.available ? (
          <div className="flex flex-col gap-2 text-sm">
            <div className="text-muted-foreground text-xs break-all">
              {health.path} · key {health.key_id}
            </div>
            <div>
              未 applied 账本意图：{health.pending ?? 0} 条；账本 UUID：
              {health.ledger_uuid}
            </div>
            <div className="text-muted-foreground text-xs">
              清理积压以下方 cleanup job 为准；主库 applied
              不等于会话副本已清除。
            </div>
            <div>
              <Button size="sm" variant="outline" onClick={replay}>
                重放账本（恢复后使用）
              </Button>
            </div>
          </div>
        ) : (
          <p className="text-sm text-red-500">
            账本不可用：{health.reason ?? "未打开"}。破坏性遗忘操作会故障关闭。
          </p>
        )}
      </section>

      <section className="rounded-lg border p-4">
        <h2 className="mb-3 text-sm font-semibold">待审批计划</h2>
        {plans.length === 0 ? (
          <p className="text-muted-foreground text-sm">没有待审批计划。</p>
        ) : (
          <div className="flex flex-col gap-2">
            {plans.map((p) => (
              <div
                key={p.plan_id}
                className="flex flex-wrap items-center justify-between gap-2 rounded-md border p-2 text-sm"
              >
                <div className="min-w-0">
                  <div className="truncate">
                    {p.action} → {p.target_id}
                  </div>
                  <div className="text-muted-foreground text-xs">
                    期望版本 r{p.expected_revision} · 过期{" "}
                    {fmtDate(p.expires_at)}
                  </div>
                </div>
                <Button size="sm" onClick={() => approve(p.plan_id)}>
                  批准并执行
                </Button>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="rounded-lg border p-4">
        <h2 className="mb-3 text-sm font-semibold">后台任务</h2>
        <div className="flex flex-wrap gap-2 text-sm">
          {Object.keys(jobStats).length === 0 ? (
            <span className="text-muted-foreground">没有任务。</span>
          ) : (
            Object.entries(jobStats).map(([state, n]) => (
              <span key={state} className="rounded-md border px-2 py-1">
                {state}: {n}
              </span>
            ))
          )}
        </div>
        {jobs.length > 0 && (
          <div className="mt-3 flex flex-col gap-2 text-sm">
            {jobs.map((job) => (
              <div key={job.job_id} className="rounded-md border p-2">
                <div className="break-all">
                  {job.kind} · {job.state} · attempts {job.attempts}
                </div>
                <div className="text-muted-foreground text-xs break-all">
                  {job.job_id}
                  {job.last_error ? ` · ${job.last_error}` : ""}
                </div>
                {jobReceipts[job.job_id] && (
                  <div className="text-muted-foreground mt-1 flex flex-wrap gap-2 text-xs">
                    {Object.entries(jobReceipts[job.job_id]).map(([state, count]) => (
                      <span key={state} className="rounded border px-1.5 py-0.5">
                        {state}: {count}
                      </span>
                    ))}
                  </div>
                )}
                {["blocked", "cancelled", "failed"].includes(job.state) && (
                  <Button
                    className="mt-2"
                    size="sm"
                    variant="outline"
                    onClick={() => requeue(job)}
                  >
                    创建后继清理任务
                  </Button>
                )}
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="rounded-lg border p-4">
        <h2 className="mb-3 text-sm font-semibold">记忆域</h2>
        {domains.length === 0 ? (
          <p className="text-muted-foreground text-sm">没有域。</p>
        ) : (
          <div className="flex flex-col gap-1 text-sm">
            {domains.map((d) => (
              <div key={d.domain_id} className="flex justify-between gap-2">
                <span className="truncate">{d.domain_id}</span>
                <span className="text-muted-foreground text-xs">
                  {d.status} · epoch {d.policy_epoch}
                </span>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="rounded-lg border p-4">
        <h2 className="mb-3 text-sm font-semibold">承诺</h2>
        {commitments.length === 0 ? (
          <p className="text-muted-foreground text-sm">没有承诺。</p>
        ) : (
          <div className="flex flex-col gap-1 text-sm">
            {commitments.map((c) => (
              <div key={c.id} className="flex justify-between gap-2">
                <span className="truncate">{c.text}</span>
                <span className="text-muted-foreground text-xs">
                  {c.status} · {fmtDate(c.due_at)}
                </span>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="rounded-lg border p-4">
        <h2 className="mb-3 text-sm font-semibold">阶段摘要</h2>
        {clusters.length === 0 ? (
          <p className="text-muted-foreground text-sm">没有主题/阶段簇。</p>
        ) : (
          <div className="flex flex-col gap-3">
            {clusters.map((c) => (
              <div key={c.id} className="rounded-md border p-3 text-sm">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="min-w-0">
                    <div className="truncate font-medium">{c.title}</div>
                    <div className="text-muted-foreground text-xs">
                      {c.id} · generation {c.generation ?? 1}
                    </div>
                  </div>
                  <div className="flex gap-2">
                    <Button
                      size="sm"
                      variant="outline"
                      onClick={() => loadSummaries(c.id)}
                    >
                      查看摘要
                    </Button>
                    <Button size="sm" onClick={() => generate(c.id)}>
                      生成摘要
                    </Button>
                  </div>
                </div>
                {(summaries[c.id] ?? []).length > 0 && (
                  <div className="mt-2 flex flex-col gap-1">
                    {(summaries[c.id] ?? []).map((s) => (
                      <div key={s.id} className="text-xs">
                        <span className="text-muted-foreground">
                          v{s.version} · {s.status}
                        </span>{" "}
                        {s.summary}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}

function OverviewTab({ onError }: { onError: (v: string) => void }) {
  const [overview, setOverview] = useState<MemoryOverview | null>(null)
  const [relationship, setRelationship] = useState<RelationshipValue[]>([])

  const load = () => {
    Promise.all([memoryApi.overview(), memoryApi.relationship()])
      .then(([o, r]) => {
        setOverview(o)
        setRelationship(r.values || [])
      })
      .catch((e: Error) => onError(e.message))
  }

  useEffect(load, [])

  const cards = useMemo(
    () => [
      { label: "总条目", value: overview?.total ?? 0 },
      { label: "生效", value: overview?.active ?? 0 },
      { label: "待审核", value: overview?.pending ?? 0 },
      { label: "已归档", value: overview?.archived ?? 0 },
      {
        label: "数据库大小",
        value: `${Math.round((overview?.db_size ?? 0) / 1024)} KB`,
      },
      { label: "版本号", value: overview?.revision ?? 0 },
    ],
    [overview],
  )

  return (
    <div className="flex flex-col gap-6">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-3">
        {cards.map((c) => (
          <div key={c.label} className="rounded-lg border p-4">
            <div className="text-muted-foreground text-xs">{c.label}</div>
            <div className="mt-1 text-2xl font-semibold">{c.value}</div>
          </div>
        ))}
      </div>

      <div className="rounded-lg border p-4">
        <h2 className="mb-3 text-sm font-semibold">关系状态</h2>
        {relationship.length === 0 ? (
          <p className="text-muted-foreground text-sm">
            尚未设置称呼、阶段或纪念日。
          </p>
        ) : (
          <ul className="grid gap-2 text-sm md:grid-cols-2">
            {relationship.map((r) => (
              <li
                key={r.key}
                className="flex justify-between gap-3 border-b pb-1"
              >
                <span className="text-muted-foreground">{r.key}</span>
                <span className="font-medium">{r.value}</span>
              </li>
            ))}
          </ul>
        )}
      </div>

      <div className="flex gap-2">
        <Button variant="outline" onClick={load}>
          刷新
        </Button>
        <Button
          variant="outline"
          onClick={() =>
            memoryApi
              .compact()
              .then((r) => alert(`已归档 ${r.archived} 条`))
              .catch((e: Error) => onError(e.message))
          }
        >
          归档过期记忆
        </Button>
      </div>
    </div>
  )
}

function EntriesTab({
  onError,
  onNotice,
}: {
  onError: (v: string) => void
  onNotice: (v: string) => void
}) {
  const [entries, setEntries] = useState<MemoryEntry[]>([])
  const [query, setQuery] = useState("")
  const [realm, setRealm] = useState("")
  const [layer, setLayer] = useState("")
  const [status, setStatus] = useState("")
  const [page, setPage] = useState(1)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(false)
  const [selected, setSelected] = useState<MemoryEntry | null>(null)
  const [draft, setDraft] = useState("")
  const [importance, setImportance] = useState(0.7)
  const [predicate, setPredicate] = useState("")
  const [facet, setFacet] = useState("")
  const listRef = useRef<HTMLDivElement>(null)

  const load = (
    nextPage = page,
    filters: { query: string; realm: string; layer: string; status: string } = {
      query,
      realm,
      layer,
      status,
    },
    scroll = nextPage !== page,
  ) => {
    const targetPage = Math.max(1, nextPage)
    const params: Record<string, string> = {
      limit: String(PAGE_SIZE + 1),
      offset: String((targetPage - 1) * PAGE_SIZE),
    }
    if (filters.query) params.q = filters.query
    if (filters.realm) params.realm = filters.realm
    if (filters.layer) params.layer = filters.layer
    if (filters.status) params.status = filters.status

    setLoading(true)
    memoryApi
      .entries(params)
      .then((r) => {
        const list = r.entries || []
        setEntries(list.slice(0, PAGE_SIZE))
        setHasMore(list.length > PAGE_SIZE)
        setPage(targetPage)
        if (scroll) {
          requestAnimationFrame(() =>
            listRef.current?.scrollIntoView({ block: "start" }),
          )
        }
      })
      .catch((e: Error) => onError(e.message))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    load(1)
  }, [])

  const select = (e: MemoryEntry) => {
    setSelected(e)
    setDraft(e.text)
    setImportance(e.importance)
    setPredicate(e.predicate || "")
    setFacet(e.facet || "")
  }

  return (
    <div className="grid gap-4 lg:grid-cols-[1fr_340px]">
      <div className="flex min-w-0 flex-col gap-3">
        <div className="flex flex-wrap gap-2">
          <Input
            placeholder="搜索内容"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter")
                load(1, { query, realm, layer, status }, true)
            }}
            className="w-48"
          />
          <select
            className="h-9 rounded-md border bg-transparent px-2 text-sm"
            value={realm}
            onChange={(e) => {
              const value = e.target.value
              setRealm(value)
              load(1, { query, realm: value, layer, status }, true)
            }}
          >
            <option value="">全部领域</option>
            <option value="real">real</option>
            <option value="fictional">fictional</option>
            <option value="work">work</option>
            <option value="unknown">unknown</option>
          </select>
          <select
            className="h-9 rounded-md border bg-transparent px-2 text-sm"
            value={layer}
            onChange={(e) => {
              const value = e.target.value
              setLayer(value)
              load(1, { query, realm, layer: value, status }, true)
            }}
          >
            <option value="">全部类型</option>
            <option value="profile">profile</option>
            <option value="relationship">relationship</option>
            <option value="episodic">episodic</option>
            <option value="procedural">procedural</option>
            <option value="boundary">boundary</option>
          </select>
          <select
            className="h-9 rounded-md border bg-transparent px-2 text-sm"
            value={status}
            onChange={(e) => {
              const value = e.target.value
              setStatus(value)
              load(1, { query, realm, layer, status: value }, true)
            }}
          >
            <option value="">全部状态</option>
            <option value="active">active</option>
            <option value="pending_review">pending</option>
            <option value="superseded">superseded</option>
            <option value="archived">archived</option>
          </select>
          <Button
            variant="outline"
            onClick={() => load(1, { query, realm, layer, status }, true)}
            disabled={loading}
          >
            搜索
          </Button>
        </div>

        <div ref={listRef} className="overflow-x-auto rounded-lg border">
          <table className="w-full table-fixed text-left text-sm">
            <colgroup>
              <col className="w-[44%]" />
              <col className="w-[30%]" />
              <col className="w-[10%]" />
              <col className="w-[16%]" />
            </colgroup>
            <thead className="bg-muted sticky top-0 z-10">
              <tr>
                <th className="p-2">内容</th>
                <th className="p-2">类型</th>
                <th className="p-2">重要度</th>
                <th className="p-2">状态</th>
              </tr>
            </thead>
            <tbody>
              {entries.map((e) => (
                <tr
                  key={e.id}
                  className="hover:bg-muted/40 cursor-pointer border-t"
                  onClick={() => select(e)}
                >
                  <td className="min-w-0 truncate p-2" title={e.text}>
                    {e.text}
                  </td>
                  <td className="min-w-0 p-2">
                    <div className="flex min-w-0 flex-wrap gap-1">
                      {badge(e.realm)}
                      {badge(e.layer)}
                      {e.predicate && e.predicate !== "待分类"
                        ? badge(e.predicate)
                        : null}
                      {e.facet ? badge(e.facet) : null}
                    </div>
                  </td>
                  <td className="p-2 whitespace-nowrap">
                    {e.importance.toFixed(2)}
                  </td>
                  <td className="p-2 whitespace-nowrap">{badge(e.status)}</td>
                </tr>
              ))}
              {entries.length === 0 && (
                <tr>
                  <td
                    className="text-muted-foreground p-4 text-center"
                    colSpan={4}
                  >
                    {loading ? "加载中…" : "没有匹配的记忆"}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
          <span className="text-muted-foreground">
            {loading ? "加载中…" : `每页 ${PAGE_SIZE} 条`}
          </span>
          <div className="flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              onClick={() => load(page - 1)}
              disabled={page <= 1 || loading}
            >
              上一页
            </Button>
            <span className="min-w-16 text-center">第 {page} 页</span>
            <Button
              variant="outline"
              size="sm"
              onClick={() => load(page + 1)}
              disabled={!hasMore || loading}
            >
              下一页
            </Button>
          </div>
        </div>
      </div>

      <div className="min-w-0 self-start rounded-lg border p-4 lg:sticky lg:top-4 lg:max-h-[calc(100vh-2rem)] lg:overflow-y-auto lg:overscroll-contain">
        {!selected ? (
          <p className="text-muted-foreground text-sm">
            选择左侧条目进行编辑。
          </p>
        ) : (
          <div className="flex flex-col gap-3">
            <div className="text-muted-foreground text-xs">{selected.id}</div>
            <Textarea
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              className="min-h-32"
            />
            <label className="text-sm">
              重要度
              <Input
                type="number"
                min={0}
                max={1}
                step={0.05}
                value={importance}
                onChange={(e) => setImportance(Number(e.target.value))}
                className="mt-1"
              />
            </label>
            <div className="grid grid-cols-2 gap-2">
              <label className="text-sm">
                predicate
                <Input
                  value={predicate}
                  onChange={(e) => setPredicate(e.target.value)}
                  className="mt-1"
                />
              </label>
              <label className="text-sm">
                facet
                <Input
                  value={facet}
                  onChange={(e) => setFacet(e.target.value)}
                  className="mt-1"
                />
              </label>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button
                onClick={() =>
                  memoryApi
                    .updateEntry(selected.id, {
                      text: draft,
                      importance,
                      predicate,
                      facet,
                    })
                    .then((e) => {
                      setSelected(e)
                      onNotice("已保存")
                      load(page)
                    })
                    .catch((err: Error) => onError(err.message))
                }
              >
                保存
              </Button>
              <Button
                variant="outline"
                onClick={() =>
                  memoryApi
                    .archiveEntry(selected.id)
                    .then(() => {
                      onNotice("已归档")
                      setSelected(null)
                      load(page)
                    })
                    .catch((err: Error) => onError(err.message))
                }
              >
                归档
              </Button>
              <Button
                variant="outline"
                onClick={() =>
                  memoryApi
                    .restoreEntry(selected.id)
                    .then(() => {
                      onNotice("已恢复")
                      load(page)
                    })
                    .catch((err: Error) => onError(err.message))
                }
              >
                恢复
              </Button>
            </div>
            <div className="text-muted-foreground text-xs">
              来源：{selected.sources?.[0]?.source_type || "-"} · 更新：
              {fmtDate(selected.updated_at)}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

function ReviewTab({
  onError,
  onNotice,
}: {
  onError: (v: string) => void
  onNotice: (v: string) => void
}) {
  const [entries, setEntries] = useState<MemoryEntry[]>([])

  const load = () =>
    memoryApi
      .review("pending")
      .then((r) => setEntries(r.entries || []))
      .catch((e: Error) => onError(e.message))

  useEffect(() => {
    void load()
  }, [])

  const resolve = (id: string, action: string) =>
    memoryApi
      .resolveReview(id, action)
      .then(() => {
        onNotice("已处理")
        load()
      })
      .catch((e: Error) => onError(e.message))

  return (
    <div className="flex flex-col gap-3">
      <p className="text-muted-foreground text-sm">
        待审核条目不会进入提示词。确认真实记忆、虚构设定或工作流程后再启用。
      </p>
      {entries.map((e) => (
        <div key={e.id} className="rounded-lg border p-4">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            {badge(e.realm)}
            {badge(e.layer)}
            <span className="text-muted-foreground">
              {fmtDate(e.created_at)}
            </span>
          </div>
          <p className="mt-2 text-sm">{e.text}</p>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button size="sm" onClick={() => resolve(e.id, "approve")}>
              确认为真实
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => resolve(e.id, "approve_fictional")}
            >
              标记虚构
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => resolve(e.id, "approve_work")}
            >
              标记工作
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => resolve(e.id, "archive")}
            >
              忽略
            </Button>
          </div>
        </div>
      ))}
      {entries.length === 0 && (
        <p className="text-muted-foreground text-sm">当前没有待审核记忆。</p>
      )}
    </div>
  )
}

function RelationshipTab({
  onError,
  onNotice,
}: {
  onError: (v: string) => void
  onNotice: (v: string) => void
}) {
  const [values, setValues] = useState<RelationshipValue[]>([])
  const [key, setKey] = useState("ai_name")
  const [value, setValue] = useState("")
  const [personas, setPersonas] = useState<Persona[]>([])
  const [active, setActive] = useState<Persona | null>(null)
  const [personaName, setPersonaName] = useState("PicoClaw")
  const [personaContent, setPersonaContent] = useState("")
  const [personaRealm, setPersonaRealm] = useState("real")
  const [personaAudience, setPersonaAudience] = useState("private")
  const [personaScope, setPersonaScope] = useState("user")

  const load = () => {
    memoryApi
      .relationship()
      .then((r) => setValues(r.values || []))
      .catch((e: Error) => onError(e.message))
    memoryApi
      .persona()
      .then((r) => {
        setPersonas(r.personas || [])
        setActive(r.active)
        if (r.active) {
          setPersonaName(r.active.name)
          setPersonaContent(r.active.content)
          setPersonaRealm(r.active.realm || "real")
          setPersonaAudience(r.active.audience || "private")
          setPersonaScope(r.active.scope || "user")
        }
      })
      .catch((e: Error) => onError(e.message))
  }

  useEffect(load, [])

  return (
    <div className="grid gap-6 lg:grid-cols-2">
      <div className="flex flex-col gap-3 rounded-lg border p-4">
        <h2 className="text-sm font-semibold">关系状态</h2>
        <ul className="flex flex-col gap-2 text-sm">
          {values.map((v) => (
            <li key={v.key} className="flex justify-between border-b pb-1">
              <span className="text-muted-foreground">{v.key}</span>
              <span>{v.value}</span>
            </li>
          ))}
          {values.length === 0 && (
            <li className="text-muted-foreground">尚未设置。</li>
          )}
        </ul>
        <div className="mt-2 flex gap-2">
          <Input
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="键"
          />
          <Input
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="值"
          />
          <Button
            onClick={() =>
              memoryApi
                .setRelationship(key, value)
                .then(() => {
                  setValue("")
                  onNotice("已更新关系状态")
                  load()
                })
                .catch((e: Error) => onError(e.message))
            }
          >
            保存
          </Button>
        </div>
      </div>

      <div className="flex flex-col gap-3 rounded-lg border p-4">
        <h2 className="text-sm font-semibold">角色设定</h2>
        <Input
          value={personaName}
          onChange={(e) => setPersonaName(e.target.value)}
        />
        <Textarea
          value={personaContent}
          onChange={(e) => setPersonaContent(e.target.value)}
          className="min-h-48"
          placeholder="人设、世界观、说话风格、边界……"
        />
        <div className="grid grid-cols-3 gap-2">
          <label className="text-muted-foreground text-xs">
            realm
            <select
              className="text-foreground mt-1 h-9 w-full rounded-md border bg-transparent px-2 text-sm"
              value={personaRealm}
              onChange={(e) => setPersonaRealm(e.target.value)}
            >
              <option value="real">real</option>
              <option value="fictional">fictional</option>
              <option value="work">work</option>
            </select>
          </label>
          <label className="text-muted-foreground text-xs">
            audience
            <select
              className="text-foreground mt-1 h-9 w-full rounded-md border bg-transparent px-2 text-sm"
              value={personaAudience}
              onChange={(e) => setPersonaAudience(e.target.value)}
            >
              <option value="private">private</option>
              <option value="all">all</option>
            </select>
          </label>
          <label className="text-muted-foreground text-xs">
            scope
            <Input
              className="mt-1"
              value={personaScope}
              onChange={(e) => setPersonaScope(e.target.value)}
            />
          </label>
        </div>
        <div className="flex gap-2">
          <Button
            onClick={() =>
              memoryApi
                .savePersona(
                  personaName,
                  personaContent,
                  true,
                  personaRealm,
                  personaAudience,
                  personaScope,
                )
                .then(() => {
                  onNotice("角色设定已保存并启用")
                  load()
                })
                .catch((e: Error) => onError(e.message))
            }
          >
            保存并启用
          </Button>
          {active && (
            <span className="text-muted-foreground self-center text-xs">
              当前版本 v{active.version}
            </span>
          )}
        </div>
        {personas.length > 1 && (
          <div className="text-muted-foreground text-xs">
            历史版本：{personas.map((p) => `v${p.version}`).join(", ")}
          </div>
        )}
      </div>
    </div>
  )
}

function MigrationTab({
  onError,
  onNotice,
}: {
  onError: (v: string) => void
  onNotice: (v: string) => void
}) {
  const [status, setStatus] = useState<MemoryMigrationStatus | null>(null)
  const [report, setReport] = useState<MemoryMigrationReport | null>(null)
  const [loading, setLoading] = useState(false)

  const load = () => {
    memoryApi
      .migrationStatus()
      .then(setStatus)
      .catch((e: Error) => onError(e.message))
  }

  useEffect(load, [])

  const applyMigration = () => {
    if (
      !window.confirm(
        "将把 memory.db 从 schema v3 单段升级到 v4：新增权限域、渠道绑定、处理许可、来源事件、依赖/任务/审批计划、摘要证据与承诺表，并逐字段重建 sources 保留全部证据。执行前会自动 VACUUM INTO 备份；v1/v2 数据库需要先用 0.6.x 升到 v3。继续吗？",
      )
    ) {
      return
    }
    setLoading(true)
    memoryApi
      .migrationApply()
      .then((r) => {
        setReport(r)
        onNotice(
          `迁移完成：schema v${r.previous_schema_version} → v${r.schema_version}，条目 ${r.total}（active ${r.active}），域 ${r.domains_created ?? 0} 个，来源 ${r.sources_rebuilt ?? 0} 行，备份：${r.backup}`,
        )
        load()
      })
      .catch((e: Error) => onError(e.message))
      .finally(() => setLoading(false))
  }

  const stats = [
    { label: "当前 schema", value: `v${status?.schema_version ?? "-"}` },
    { label: "目标 schema", value: `v${status?.current_version ?? "-"}` },
    { label: "总条目", value: status?.total ?? 0 },
    { label: "生效", value: status?.active ?? 0 },
    { label: "空 predicate", value: status?.empty_predicate ?? 0 },
    {
      label: "空 relationship facet",
      value: status?.empty_relationship_facet ?? 0,
    },
    { label: "relationship", value: status?.relationship ?? 0 },
    { label: "personas", value: status?.personas ?? 0 },
    { label: "pinned 核心", value: status?.pinned_core ?? 0 },
    { label: "关系边", value: status?.relations ?? 0 },
    {
      label: "FTS 行数",
      value: `${status?.fts_rows ?? 0}${status?.fts_tokenizer ? ` (${status.fts_tokenizer})` : ""}`,
    },
    {
      label: "数据库大小",
      value: `${Math.round((status?.db_size ?? 0) / 1024)} KB`,
    },
    { label: "记忆域", value: status?.domains ?? 0 },
    { label: "后台任务", value: status?.jobs ?? 0 },
  ]

  return (
    <div className="flex flex-col gap-4">
      <div className="rounded-lg border p-4">
        <h2 className="text-sm font-semibold">数据库一键迁移（v3 → v4）</h2>
        <p className="text-muted-foreground mt-1 text-sm">
          schema v4
          新增权限域与渠道绑定、处理用途许可、来源事件、依赖/任务/审批计划、
          摘要证据、承诺与主动回忆表，并为 entries/sources/relationship
          补充治理列。 sources
          会逐字段重建，证据原文、模型、置信度与确认标志全部保留；旧数据进入
          隔离域 legacy_unassigned，不猜测归属。只接受 v3 数据库：v1/v2 请先用
          0.6.x 升到 v3。迁移幂等、整段事务，执行前自动 VACUUM INTO 备份。
        </p>
        <div className="mt-3 grid grid-cols-2 gap-2 md:grid-cols-4">
          {stats.map((s) => (
            <div key={s.label} className="rounded-md border p-3">
              <div className="text-muted-foreground text-xs">{s.label}</div>
              <div className="mt-1 text-lg font-semibold">{s.value}</div>
            </div>
          ))}
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Button onClick={applyMigration} disabled={loading}>
            {loading ? "迁移中..." : "执行一键迁移"}
          </Button>
          {status && !status.needs_migration && (
            <span className="text-sm text-emerald-600">
              数据库 Schema 已是最新；仅在实际变更前创建新备份。
            </span>
          )}
          {status?.needs_migration && (
            <span className="text-muted-foreground text-sm">
              {status.needs_schema_migration ? "需要 schema 迁移/修复" : ""}
            </span>
          )}
          {status && status.empty_predicate > 0 && (
            <span className="text-muted-foreground text-sm">
              数据质量提示：{status.empty_predicate} 条空
              predicate（不阻塞迁移）
            </span>
          )}
        </div>
        <div className="text-muted-foreground mt-3 space-y-1 text-xs">
          <div className="break-all">数据库：{status?.db_path || "-"}</div>
          <div className="break-all">
            最近备份：{status?.backup || "暂无记录"}
          </div>
          {status?.last_migration_at ? (
            <div>最近迁移：{fmtDate(status.last_migration_at)}</div>
          ) : null}
        </div>
      </div>

      {report && (
        <div className="rounded-lg border p-4">
          <h3 className="text-sm font-semibold">迁移结果</h3>
          <div className="text-muted-foreground mt-2 grid gap-1 text-sm">
            <div>
              schema：v{report.previous_schema_version} → v
              {report.schema_version}
            </div>
            <div>
              条目：{report.total}（active {report.active}）
            </div>
            <div>来源：{report.sources_rebuilt ?? 0} 行</div>
            <div>
              已认领域条目：{report.entries_with_domain ?? 0} · 隔离域：
              {Math.max(0, report.total - (report.entries_with_domain ?? 0))}
            </div>
            <div>记忆域：{report.domains_created ?? 0} 个</div>
            <div className="break-all">备份：{report.backup}</div>
          </div>
        </div>
      )}

      <div className="rounded-lg border p-4">
        <h3 className="text-sm font-semibold">迁移后建议</h3>
        <ul className="text-muted-foreground mt-2 list-disc space-y-1 pl-5 text-sm">
          <li>
            旧条目进入隔离域
            legacy_unassigned；在认领或迁移到新域前，提示词检索默认读取该隔离域，仍可能被注入。
          </li>
          <li>
            forget-ledger.db 与 memory.db
            同目录，备份会同时保存两者；账本缺失或身份损坏时永久遗忘会故障关闭，不会静默新建。
          </li>
          <li>v1/v2 数据库不会被就地升级，先用 0.6.x 打开一次升到 v3。</li>
          <li>
            迁移可重复执行；需要迁移时会生成 VACUUM INTO
            主库与账本备份，重复执行零变更。
          </li>
        </ul>
      </div>
    </div>
  )
}

function RepairTab({
  onError,
  onNotice,
}: {
  onError: (v: string) => void
  onNotice: (v: string) => void
}) {
  const [overview, setOverview] = useState<MemoryOverview | null>(null)
  const [filter, setFilter] = useState<MemoryRepairFilter>({ status: "active" })
  const [target, setTarget] = useState<MemoryRepairTarget>({
    realm: "fictional",
    layer: "procedural",
  })
  const [preview, setPreview] = useState<MemoryRepairPreview | null>(null)
  const [result, setResult] = useState<{
    updated: number
    backup: string
  } | null>(null)
  const [loading, setLoading] = useState(false)

  const loadOverview = () => {
    memoryApi
      .overview()
      .then(setOverview)
      .catch((e: Error) => onError(e.message))
  }

  useEffect(loadOverview, [])

  const setFilterField = (key: keyof MemoryRepairFilter, value: string) => {
    setFilter({ ...filter, [key]: value })
    setPreview(null)
    setResult(null)
  }

  const setTargetField = (key: keyof MemoryRepairTarget, value: string) => {
    setTarget({ ...target, [key]: value })
    setResult(null)
  }

  const previewRepair = () => {
    setLoading(true)
    memoryApi
      .repairPreview(filter)
      .then(setPreview)
      .catch((e: Error) => onError(e.message))
      .finally(() => setLoading(false))
  }

  const applyRepair = () => {
    if (!preview || preview.count === 0) return
    if (
      !window.confirm(
        `将迁移 ${preview.count} 条记录到 ${target.realm}/${target.layer}，执行前会自动备份数据库。继续吗？`,
      )
    ) {
      return
    }
    setLoading(true)
    memoryApi
      .repairApply(filter, target)
      .then((r) => {
        setResult(r)
        setPreview(null)
        onNotice(`已迁移 ${r.updated} 条，备份：${r.backup}`)
        loadOverview()
      })
      .catch((e: Error) => onError(e.message))
      .finally(() => setLoading(false))
  }

  const stats = [
    { label: "总条目", value: overview?.total ?? 0 },
    { label: "生效", value: overview?.active ?? 0 },
    { label: "待审核", value: overview?.pending ?? 0 },
    { label: "已归档", value: overview?.archived ?? 0 },
    {
      label: "数据库大小",
      value: `${Math.round((overview?.db_size ?? 0) / 1024)} KB`,
    },
  ]

  return (
    <div className="flex flex-col gap-4">
      <div className="rounded-lg border p-4">
        <h2 className="text-sm font-semibold">数据库修复与批量迁移</h2>
        <p className="text-muted-foreground mt-1 text-sm">
          用于把历史数据批量迁移到正确的 realm / layer /
          audience。每次执行前会用 SQLite VACUUM INTO
          自动备份，只修改匹配记录，不删除数据。
        </p>
        <div className="mt-3 grid grid-cols-2 gap-2 md:grid-cols-5">
          {stats.map((s) => (
            <div key={s.label} className="rounded-md border p-3">
              <div className="text-muted-foreground text-xs">{s.label}</div>
              <div className="mt-1 text-lg font-semibold">{s.value}</div>
            </div>
          ))}
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-[1fr_320px]">
        <div className="rounded-lg border p-4">
          <h3 className="text-sm font-semibold">筛选要迁移的记录</h3>
          <div className="mt-3 grid gap-2 sm:grid-cols-2">
            <select
              className="h-9 rounded-md border bg-transparent px-2 text-sm"
              value={filter.realm || ""}
              onChange={(e) => setFilterField("realm", e.target.value)}
            >
              <option value="">全部 realm</option>
              <option value="real">real</option>
              <option value="fictional">fictional</option>
              <option value="work">work</option>
              <option value="unknown">unknown</option>
            </select>
            <select
              className="h-9 rounded-md border bg-transparent px-2 text-sm"
              value={filter.layer || ""}
              onChange={(e) => setFilterField("layer", e.target.value)}
            >
              <option value="">全部 layer</option>
              <option value="persona">persona</option>
              <option value="profile">profile</option>
              <option value="relationship">relationship</option>
              <option value="episodic">episodic</option>
              <option value="procedural">procedural</option>
              <option value="boundary">boundary</option>
            </select>
            <select
              className="h-9 rounded-md border bg-transparent px-2 text-sm"
              value={filter.status || ""}
              onChange={(e) => setFilterField("status", e.target.value)}
            >
              <option value="active">active</option>
              <option value="pending_review">pending_review</option>
              <option value="superseded">superseded</option>
              <option value="archived">archived</option>
              <option value="all">全部状态</option>
            </select>
            <select
              className="h-9 rounded-md border bg-transparent px-2 text-sm"
              value={filter.audience || ""}
              onChange={(e) => setFilterField("audience", e.target.value)}
            >
              <option value="">全部 audience</option>
              <option value="all">all</option>
              <option value="private">private</option>
            </select>
          </div>
          <Input
            className="mt-2"
            placeholder="可选：文本关键词，例如 姐姐 / 蓝色海豚"
            value={filter.query || ""}
            onChange={(e) => setFilterField("query", e.target.value)}
          />
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button
              variant="outline"
              onClick={previewRepair}
              disabled={loading}
            >
              预览匹配
            </Button>
            {preview && (
              <span className="text-muted-foreground text-sm">
                匹配 {preview.count} 条
              </span>
            )}
          </div>

          {preview && preview.entries.length > 0 && (
            <div className="mt-3 max-h-64 overflow-auto rounded-md border">
              <table className="w-full text-left text-xs">
                <thead className="bg-muted sticky top-0">
                  <tr>
                    <th className="p-2">内容</th>
                    <th className="p-2">分类</th>
                    <th className="p-2">状态</th>
                  </tr>
                </thead>
                <tbody>
                  {preview.entries.map((e) => (
                    <tr key={e.id} className="border-t">
                      <td className="max-w-[360px] truncate p-2">{e.text}</td>
                      <td className="p-2">
                        <div className="flex gap-1">
                          {badge(e.realm)}
                          {badge(e.layer)}
                          {badge(e.audience)}
                        </div>
                      </td>
                      <td className="p-2">{badge(e.status)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {preview && preview.entries.length === 0 && (
            <p className="text-muted-foreground mt-3 text-sm">没有匹配记录。</p>
          )}
        </div>

        <div className="rounded-lg border p-4">
          <h3 className="text-sm font-semibold">目标分类</h3>
          <div className="mt-3 flex flex-col gap-2">
            <label className="text-muted-foreground text-xs">
              realm
              <select
                className="text-foreground mt-1 h-9 w-full rounded-md border bg-transparent px-2 text-sm"
                value={target.realm}
                onChange={(e) => setTargetField("realm", e.target.value)}
              >
                <option value="real">real</option>
                <option value="fictional">fictional</option>
                <option value="work">work</option>
                <option value="unknown">unknown</option>
              </select>
            </label>
            <label className="text-muted-foreground text-xs">
              layer
              <select
                className="text-foreground mt-1 h-9 w-full rounded-md border bg-transparent px-2 text-sm"
                value={target.layer}
                onChange={(e) => setTargetField("layer", e.target.value)}
              >
                <option value="persona">persona</option>
                <option value="profile">profile</option>
                <option value="relationship">relationship</option>
                <option value="episodic">episodic</option>
                <option value="procedural">procedural</option>
                <option value="boundary">boundary</option>
              </select>
            </label>
            <label className="text-muted-foreground text-xs">
              audience
              <select
                className="text-foreground mt-1 h-9 w-full rounded-md border bg-transparent px-2 text-sm"
                value={target.audience || ""}
                onChange={(e) => setTargetField("audience", e.target.value)}
              >
                <option value="">保留原 audience</option>
                <option value="all">all</option>
                <option value="private">private</option>
              </select>
            </label>
          </div>
          <Button
            className="mt-4 w-full"
            onClick={applyRepair}
            disabled={loading || !preview || preview.count === 0}
          >
            执行迁移
          </Button>
          {result && (
            <p className="mt-3 text-xs break-all text-emerald-600">
              已迁移 {result.updated} 条；备份：{result.backup}
            </p>
          )}
          <p className="text-muted-foreground mt-3 text-xs">
            建议先用关键词缩小范围，预览确认后再执行。迁移不会删除记录，可用备份回滚。
          </p>
        </div>
      </div>
    </div>
  )
}

function SettingsTab({
  onError,
  onNotice,
}: {
  onError: (v: string) => void
  onNotice: (v: string) => void
}) {
  const [settings, setSettings] = useState<MemorySettings | null>(null)

  useEffect(() => {
    memoryApi
      .settings()
      .then(setSettings)
      .catch((e: Error) => onError(e.message))
  }, [])

  if (!settings) return <p className="text-muted-foreground text-sm">加载中…</p>

  const update = <K extends keyof MemorySettings>(
    key: K,
    value: MemorySettings[K],
  ) => setSettings({ ...settings, [key]: value })

  return (
    <div className="flex max-w-2xl flex-col gap-4">
      <label className="flex items-center justify-between rounded-lg border p-3 text-sm">
        <span>启用记忆</span>
        <input
          type="checkbox"
          checked={settings.enabled}
          onChange={(e) => update("enabled", e.target.checked)}
        />
      </label>
      <label className="flex items-center justify-between rounded-lg border p-3 text-sm">
        <span>自动抽取</span>
        <input
          type="checkbox"
          checked={settings.auto_extract}
          onChange={(e) => update("auto_extract", e.target.checked)}
        />
      </label>
      <label className="flex items-center justify-between rounded-lg border p-3 text-sm">
        <span>注入虚构/角色扮演记忆</span>
        <input
          type="checkbox"
          checked={settings.inject_fictional}
          onChange={(e) => update("inject_fictional", e.target.checked)}
        />
      </label>
      <label className="flex items-center justify-between rounded-lg border p-3 text-sm">
        <span>自动抽取需人工审核</span>
        <input
          type="checkbox"
          checked={settings.review_required}
          onChange={(e) => update("review_required", e.target.checked)}
        />
      </label>
      <label className="text-sm">
        注入预算（tokens）
        <Input
          type="number"
          value={settings.budget_tokens ?? 1200}
          onChange={(e) => update("budget_tokens", Number(e.target.value))}
          className="mt-1"
        />
      </label>
      <div className="rounded-lg border p-3 text-sm">
        <div className="mb-1 font-medium">渠道记忆可见性</div>
        <p className="text-muted-foreground mb-2 text-xs">
          默认所有渠道都能读取私密记忆；取消勾选即排除该渠道。群聊/公开会话始终不可见。
        </p>
        <div className="flex flex-wrap gap-2">
          {(settings.available_channels ?? ["pico"]).map((ch) => {
            const excluded =
              settings.private_channel_exclusions?.includes(ch) ?? false
            return (
              <label
                key={ch}
                className="flex items-center gap-1.5 rounded-md border px-3 py-1.5"
              >
                <input
                  type="checkbox"
                  checked={!excluded}
                  onChange={(e) => {
                    const prev = settings.private_channel_exclusions ?? []
                    update(
                      "private_channel_exclusions",
                      e.target.checked
                        ? prev.filter((v) => v !== ch)
                        : [...prev, ch],
                    )
                  }}
                />
                <span>{ch}</span>
              </label>
            )
          })}
        </div>
      </div>
      <label className="text-sm">
        事件记忆保留天数
        <Input
          type="number"
          value={settings.episodic_retention_days ?? 365}
          onChange={(e) =>
            update("episodic_retention_days", Number(e.target.value))
          }
          className="mt-1"
        />
      </label>
      <Button
        onClick={() =>
          memoryApi
            .saveSettings(settings)
            .then(() => onNotice("设置已保存"))
            .catch((e: Error) => onError(e.message))
        }
      >
        保存设置
      </Button>

      <div className="mt-4 rounded-lg border p-4">
        <h2 className="text-sm font-semibold">一次性导入（仅 CLI）</h2>
        <p className="text-muted-foreground mt-1 text-xs">
          为避免 dashboard 读取任意宿主机路径，旧 workspace 导入改为维护命令；导入内容默认为 private、待审核。
        </p>
        <pre className="bg-muted mt-2 overflow-x-auto rounded p-2 text-xs">
          picoclaw memory import --from /path/to/old-workspace
        </pre>
      </div>
    </div>
  )
}
