import { useEffect, useMemo, useState } from "react"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  memoryApi,
  type MemoryEntry,
  type MemoryOverview,
  type MemorySettings,
  type Persona,
  type RelationshipValue,
} from "@/lib/memory-api"

const TABS = ["overview", "entries", "review", "relationship", "settings"] as const
type Tab = (typeof TABS)[number]

const TAB_LABELS: Record<Tab, string> = {
  overview: "概览",
  entries: "记忆库",
  review: "审核",
  relationship: "关系与人格",
  settings: "设置",
}

function fmtDate(ms?: number) {
  if (!ms) return "-"
  return new Date(ms).toLocaleString()
}

function badge(value: string) {
  return (
    <span className="rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">
      {value}
    </span>
  )
}

export function MemoryPage() {
  const [tab, setTab] = useState<Tab>("overview")
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")

  return (
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
              tab === t ? "bg-primary text-primary-foreground" : "hover:bg-muted"
            }`}
          >
            {TAB_LABELS[t]}
          </button>
        ))}
      </div>

      {error && <div className="rounded-md bg-red-500/10 p-3 text-sm text-red-500">{error}</div>}
      {notice && (
        <div className="rounded-md bg-emerald-500/10 p-3 text-sm text-emerald-600">{notice}</div>
      )}

      {tab === "overview" && <OverviewTab onError={setError} />}
      {tab === "entries" && <EntriesTab onError={setError} onNotice={setNotice} />}
      {tab === "review" && <ReviewTab onError={setError} onNotice={setNotice} />}
      {tab === "relationship" && <RelationshipTab onError={setError} onNotice={setNotice} />}
      {tab === "settings" && <SettingsTab onError={setError} onNotice={setNotice} />}
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
      { label: "数据库大小", value: `${Math.round((overview?.db_size ?? 0) / 1024)} KB` },
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
          <p className="text-muted-foreground text-sm">尚未设置称呼、阶段或纪念日。</p>
        ) : (
          <ul className="grid gap-2 text-sm md:grid-cols-2">
            {relationship.map((r) => (
              <li key={r.key} className="flex justify-between gap-3 border-b pb-1">
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
  const [selected, setSelected] = useState<MemoryEntry | null>(null)
  const [draft, setDraft] = useState("")
  const [importance, setImportance] = useState(0.7)

  const load = () => {
    const params: Record<string, string> = { limit: "200" }
    if (query) params.q = query
    if (realm) params.realm = realm
    if (layer) params.layer = layer
    if (status) params.status = status
    memoryApi
      .entries(params)
      .then((r) => setEntries(r.entries || []))
      .catch((e: Error) => onError(e.message))
  }

  useEffect(load, [])

  const select = (e: MemoryEntry) => {
    setSelected(e)
    setDraft(e.text)
    setImportance(e.importance)
  }

  return (
    <div className="grid gap-4 lg:grid-cols-[1fr_340px]">
      <div className="flex flex-col gap-3">
        <div className="flex flex-wrap gap-2">
          <Input
            placeholder="搜索内容"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="w-48"
          />
          <select
            className="h-9 rounded-md border bg-transparent px-2 text-sm"
            value={realm}
            onChange={(e) => setRealm(e.target.value)}
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
            onChange={(e) => setLayer(e.target.value)}
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
            onChange={(e) => setStatus(e.target.value)}
          >
            <option value="">全部状态</option>
            <option value="active">active</option>
            <option value="pending_review">pending</option>
            <option value="superseded">superseded</option>
            <option value="archived">archived</option>
          </select>
          <Button variant="outline" onClick={load}>
            搜索
          </Button>
        </div>

        <div className="max-h-[520px] overflow-auto rounded-lg border">
          <table className="w-full text-left text-sm">
            <thead className="bg-muted/50 sticky top-0">
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
                  <td className="max-w-[420px] truncate p-2">{e.text}</td>
                  <td className="p-2">
                    <div className="flex gap-1">
                      {badge(e.realm)}
                      {badge(e.layer)}
                    </div>
                  </td>
                  <td className="p-2">{e.importance.toFixed(2)}</td>
                  <td className="p-2">{badge(e.status)}</td>
                </tr>
              ))}
              {entries.length === 0 && (
                <tr>
                  <td className="text-muted-foreground p-4 text-center" colSpan={4}>
                    没有匹配的记忆
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="rounded-lg border p-4">
        {!selected ? (
          <p className="text-muted-foreground text-sm">选择左侧条目进行编辑。</p>
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
            <div className="flex flex-wrap gap-2">
              <Button
                onClick={() =>
                  memoryApi
                    .updateEntry(selected.id, { text: draft, importance })
                    .then((e) => {
                      setSelected(e)
                      onNotice("已保存")
                      load()
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
                      load()
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
                      load()
                    })
                    .catch((err: Error) => onError(err.message))
                }
              >
                恢复
              </Button>
            </div>
            <div className="text-muted-foreground text-xs">
              来源：{selected.sources?.[0]?.source_type || "-"} · 更新：{fmtDate(selected.updated_at)}
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
            <span className="text-muted-foreground">{fmtDate(e.created_at)}</span>
          </div>
          <p className="mt-2 text-sm">{e.text}</p>
          <div className="mt-3 flex flex-wrap gap-2">
            <Button size="sm" onClick={() => resolve(e.id, "approve")}>
              确认为真实
            </Button>
            <Button size="sm" variant="outline" onClick={() => resolve(e.id, "approve_fictional")}>
              标记虚构
            </Button>
            <Button size="sm" variant="outline" onClick={() => resolve(e.id, "approve_work")}>
              标记工作
            </Button>
            <Button size="sm" variant="outline" onClick={() => resolve(e.id, "archive")}>
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
          {values.length === 0 && <li className="text-muted-foreground">尚未设置。</li>}
        </ul>
        <div className="mt-2 flex gap-2">
          <Input value={key} onChange={(e) => setKey(e.target.value)} placeholder="键" />
          <Input value={value} onChange={(e) => setValue(e.target.value)} placeholder="值" />
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
        <Input value={personaName} onChange={(e) => setPersonaName(e.target.value)} />
        <Textarea
          value={personaContent}
          onChange={(e) => setPersonaContent(e.target.value)}
          className="min-h-48"
          placeholder="人设、世界观、说话风格、边界……"
        />
        <div className="flex gap-2">
          <Button
            onClick={() =>
              memoryApi
                .savePersona(personaName, personaContent, true)
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

function SettingsTab({
  onError,
  onNotice,
}: {
  onError: (v: string) => void
  onNotice: (v: string) => void
}) {
  const [settings, setSettings] = useState<MemorySettings | null>(null)
  const [importPath, setImportPath] = useState("")

  useEffect(() => {
    memoryApi
      .settings()
      .then(setSettings)
      .catch((e: Error) => onError(e.message))
  }, [])

  if (!settings) return <p className="text-muted-foreground text-sm">加载中…</p>

  const update = <K extends keyof MemorySettings>(key: K, value: MemorySettings[K]) =>
    setSettings({ ...settings, [key]: value })

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
      <label className="text-sm">
        私有渠道（逗号分隔）
        <Input
          value={(settings.private_channels || []).join(",")}
          onChange={(e) =>
            update(
              "private_channels",
              e.target.value
                .split(",")
                .map((v) => v.trim())
                .filter(Boolean),
            )
          }
          className="mt-1"
        />
      </label>
      <label className="text-sm">
        事件记忆保留天数
        <Input
          type="number"
          value={settings.episodic_retention_days ?? 365}
          onChange={(e) => update("episodic_retention_days", Number(e.target.value))}
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
        <h2 className="text-sm font-semibold">一次性导入（0.4 切换期）</h2>
        <p className="text-muted-foreground mt-1 text-xs">
          从旧 workspace 导入 task-records 和会话摘要，结果进入审核队列。
        </p>
        <div className="mt-2 flex gap-2">
          <Input
            placeholder="/home/user/.picoclaw/workspace2"
            value={importPath}
            onChange={(e) => setImportPath(e.target.value)}
          />
          <Button
            variant="outline"
            onClick={() =>
              memoryApi
                .importWorkspace(importPath)
                .then((r) => onNotice(`导入完成：${JSON.stringify(r)}`))
                .catch((e: Error) => onError(e.message))
            }
          >
            导入
          </Button>
        </div>
      </div>
    </div>
  )
}
