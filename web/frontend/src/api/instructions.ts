import { launcherFetch } from "@/api/http"

export interface InstructionPreset {
  id: string
  name: string
  prompt: string
}

export interface InstructionsLimits {
  max_presets: number
  max_prompt_runes: number
  max_name_runes: number
}

export interface InstructionsDocument {
  enabled: boolean
  active: string
  presets: InstructionPreset[]
  path?: string
  limits?: InstructionsLimits
}

export interface InstructionsPayload {
  enabled: boolean
  active: string
  presets: InstructionPreset[]
}

export const INSTRUCTIONS_LIMITS: InstructionsLimits = {
  max_presets: 10,
  max_prompt_runes: 8000,
  max_name_runes: 60,
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
  if (!res.ok) {
    let message = `API error: ${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as {
        error?: string
        errors?: string[]
      }
      if (Array.isArray(body.errors) && body.errors.length > 0) {
        message = body.errors.join("; ")
      } else if (typeof body.error === "string" && body.error.trim() !== "") {
        message = body.error
      }
    } catch {
      // Keep the fallback message when the body is not JSON.
    }
    throw new Error(message)
  }
  if (res.status === 204) {
    return undefined as T
  }
  return (await res.json()) as T
}

export function fetchInstructions(): Promise<InstructionsDocument> {
  return request<InstructionsDocument>("/api/instructions")
}

export function saveInstructions(
  payload: InstructionsPayload,
): Promise<{ status: string; path: string }> {
  return request<{ status: string; path: string }>("/api/instructions", {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  })
}
