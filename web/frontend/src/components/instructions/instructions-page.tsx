import { IconPlus, IconTrash } from "@tabler/icons-react"
import { useEffect, useMemo, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  type InstructionPreset,
  type InstructionsDocument,
  INSTRUCTIONS_LIMITS,
  fetchInstructions,
  saveInstructions,
} from "@/api/instructions"
import { Field, SwitchCardField } from "@/components/shared-form"
import { Button } from "@/components/ui/button"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Textarea } from "@/components/ui/textarea"

const EMPTY_DOCUMENT: InstructionsDocument = {
  enabled: false,
  active: "",
  presets: [],
}

function serialize(doc: InstructionsDocument): string {
  return JSON.stringify({
    enabled: doc.enabled,
    active: doc.active,
    presets: doc.presets.map((preset) => ({
      id: preset.id,
      name: preset.name,
      prompt: preset.prompt,
    })),
  })
}

function normalizeDocument(raw: InstructionsDocument): InstructionsDocument {
  const presets = Array.isArray(raw.presets)
    ? raw.presets.map((preset) => ({
        id: preset.id,
        name: preset.name ?? "",
        prompt: preset.prompt ?? "",
      }))
    : []
  const active = presets.some((preset) => preset.id === raw.active)
    ? raw.active
    : (presets[0]?.id ?? "")
  return {
    enabled: raw.enabled === true,
    active,
    presets,
    path: raw.path,
    limits: raw.limits,
  }
}

function nextPresetID(existing: InstructionPreset[]): string {
  const used = new Set(existing.map((preset) => preset.id))
  let index = existing.length + 1
  let candidate = `preset-${index}`
  while (used.has(candidate)) {
    index += 1
    candidate = `preset-${index}`
  }
  return candidate
}

function runeLength(value: string): number {
  return Array.from(value).length
}

// validationError returns a translation key suffix, or "" when valid.
function validationError(doc: InstructionsDocument): string {
  const limits = doc.limits ?? INSTRUCTIONS_LIMITS
  if (doc.presets.length > limits.max_presets) {
    return "max_presets"
  }
  for (const preset of doc.presets) {
    if (preset.name.trim() === "") {
      return "name_required"
    }
    if (preset.prompt.trim() === "") {
      return "prompt_required"
    }
    if (runeLength(preset.prompt) > limits.max_prompt_runes) {
      return "prompt_too_long"
    }
  }
  if (doc.enabled && !doc.presets.some((preset) => preset.id === doc.active)) {
    return "active_required"
  }
  return ""
}

export function InstructionsPage() {
  const { t } = useTranslation()
  const [doc, setDoc] = useState<InstructionsDocument>(EMPTY_DOCUMENT)
  const [baseline, setBaseline] = useState<InstructionsDocument>(EMPTY_DOCUMENT)
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    fetchInstructions()
      .then((raw) => {
        if (cancelled) {
          return
        }
        const next = normalizeDocument(raw)
        setDoc(next)
        setBaseline(next)
        setError("")
      })
      .catch((err: unknown) => {
        if (cancelled) {
          return
        }
        setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false)
        }
      })
    return () => {
      cancelled = true
    }
  }, [])

  const limits = doc.limits ?? INSTRUCTIONS_LIMITS
  const isDirty = serialize(doc) !== serialize(baseline)
  const currentValidation = useMemo(() => validationError(doc), [doc])

  const updatePreset = (id: string, patch: Partial<InstructionPreset>) => {
    setDoc((prev) => ({
      ...prev,
      presets: prev.presets.map((preset) =>
        preset.id === id ? { ...preset, ...patch } : preset,
      ),
    }))
  }

  const addPreset = () => {
    setDoc((prev) => {
      if (prev.presets.length >= limits.max_presets) {
        return prev
      }
      const created: InstructionPreset = {
        id: nextPresetID(prev.presets),
        name: t("pages.instructions.new_preset"),
        prompt: "",
      }
      return {
        ...prev,
        presets: [...prev.presets, created],
        active: prev.active === "" ? created.id : prev.active,
      }
    })
  }

  const removePreset = (id: string) => {
    setDoc((prev) => {
      const presets = prev.presets.filter((preset) => preset.id !== id)
      return {
        ...prev,
        presets,
        active: prev.active === id ? (presets[0]?.id ?? "") : prev.active,
      }
    })
  }

  const handleSave = async () => {
    if (currentValidation !== "") {
      setError(
        t(`pages.instructions.errors.${currentValidation}`, {
          max: limits.max_presets,
          limit: limits.max_prompt_runes,
        }),
      )
      return
    }

    setSaving(true)
    setError("")
    setNotice("")
    try {
      await saveInstructions({
        enabled: doc.enabled,
        active: doc.active,
        presets: doc.presets,
      })
      setBaseline(doc)
      setNotice(t("pages.instructions.saved"))
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  const handleReset = () => {
    setDoc(baseline)
    setError("")
    setNotice("")
  }

  if (loading) {
    return (
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="text-muted-foreground mx-auto w-full max-w-4xl p-6 text-sm">
          {t("pages.instructions.loading")}
        </div>
      </div>
    )
  }

  return (
    <div className="min-h-0 flex-1 [scrollbar-gutter:stable] overflow-y-auto">
      <div className="mx-auto flex w-full max-w-4xl flex-col gap-6 p-6">
        <div>
          <h1 className="text-2xl font-semibold">
            {t("pages.instructions.title")}
          </h1>
          <p className="text-muted-foreground mt-1 text-sm">
            {t("pages.instructions.description")}
          </p>
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

        <Card size="sm">
          <CardHeader className="border-border border-b">
            <CardTitle>{t("pages.instructions.section_general")}</CardTitle>
            <CardDescription>
              {t("pages.instructions.section_general_hint")}
            </CardDescription>
          </CardHeader>
          <CardContent className="pt-0">
            <div className="divide-border/70 divide-y">
              <SwitchCardField
                label={t("pages.instructions.enabled")}
                hint={t("pages.instructions.enabled_hint")}
                layout="setting-row"
                checked={doc.enabled}
                onCheckedChange={(checked) =>
                  setDoc((prev) => ({ ...prev, enabled: checked }))
                }
              />
              <Field
                label={t("pages.instructions.active")}
                hint={t("pages.instructions.active_hint")}
                layout="setting-row"
              >
                <Select
                  value={doc.active}
                  onValueChange={(value) =>
                    setDoc((prev) => ({ ...prev, active: value }))
                  }
                  disabled={doc.presets.length === 0}
                >
                  <SelectTrigger className="h-9">
                    <SelectValue
                      placeholder={t("pages.instructions.active_placeholder")}
                    />
                  </SelectTrigger>
                  <SelectContent>
                    {doc.presets.map((preset) => (
                      <SelectItem key={preset.id} value={preset.id}>
                        {preset.name.trim() === "" ? preset.id : preset.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </div>
          </CardContent>
        </Card>

        <Card size="sm">
          <CardHeader className="border-border border-b">
            <div className="flex flex-wrap items-start justify-between gap-3">
              <div>
                <CardTitle>{t("pages.instructions.presets")}</CardTitle>
                <CardDescription>
                  {t("pages.instructions.presets_hint", {
                    max: limits.max_presets,
                  })}
                </CardDescription>
              </div>
              <Button
                variant="outline"
                onClick={addPreset}
                disabled={doc.presets.length >= limits.max_presets}
              >
                <IconPlus className="size-4" />
                {t("pages.instructions.add_preset")}
              </Button>
            </div>
          </CardHeader>
          <CardContent className="pt-4">
            {doc.presets.length === 0 ? (
              <p className="text-muted-foreground text-sm">
                {t("pages.instructions.empty")}
              </p>
            ) : (
              <div className="flex flex-col gap-4">
                {doc.presets.map((preset) => {
                  const used = runeLength(preset.prompt)
                  return (
                    <div
                      key={preset.id}
                      className="border-border/70 rounded-lg border p-4"
                    >
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0 flex-1">
                          <Field label={t("pages.instructions.preset_name")}>
                            <Input
                              value={preset.name}
                              onChange={(event) =>
                                updatePreset(preset.id, {
                                  name: event.target.value,
                                })
                              }
                            />
                          </Field>
                        </div>
                        <Button
                          variant="ghost"
                          className="mt-6 p-2"
                          onClick={() => removePreset(preset.id)}
                          aria-label={t("pages.instructions.remove_preset")}
                        >
                          <IconTrash className="size-4" />
                        </Button>
                      </div>
                      <div className="mt-3">
                        <Field
                          label={t("pages.instructions.preset_prompt")}
                          hint={t("pages.instructions.preset_prompt_hint", {
                            used,
                            limit: limits.max_prompt_runes,
                          })}
                        >
                          <Textarea
                            value={preset.prompt}
                            rows={6}
                            className="font-mono text-xs"
                            onChange={(event) =>
                              updatePreset(preset.id, {
                                prompt: event.target.value,
                              })
                            }
                          />
                        </Field>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </CardContent>
        </Card>

        <div className="flex flex-wrap items-center gap-3">
          <Button
            onClick={handleSave}
            disabled={!isDirty || saving || currentValidation !== ""}
          >
            {saving ? t("common.saving") : t("common.save")}
          </Button>
          <Button
            variant="outline"
            onClick={handleReset}
            disabled={!isDirty || saving}
          >
            {t("common.reset")}
          </Button>
          {doc.path && (
            <span className="text-muted-foreground text-xs break-all">
              {t("pages.instructions.storage", { path: doc.path })}
            </span>
          )}
        </div>

        <p className="text-muted-foreground text-xs">
          {t("pages.instructions.effect_hint")}
        </p>
      </div>
    </div>
  )
}
