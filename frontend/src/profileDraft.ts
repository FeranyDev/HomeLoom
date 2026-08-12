import type { ValueType } from './types/device'
import type { MappingProfile, MappingTransform } from './types/mapping'

export type ProfileDraftSeed = {
  stage: 'provider' | 'consumer'
  inputType: ValueType
  outputType: ValueType
  sourceEnum?: string[]
  targetEnum?: string[]
  sourceLabel?: string
  targetLabel?: string
  reason?: string
}

const STORAGE_KEY = 'homeloom:profile-draft'
const HANDOFF_KEY_PREFIX = `${STORAGE_KEY}:handoff:`
const HANDOFF_PARAMETER = 'profile-draft'

type StoredProfileDraft = {
  draft: MappingProfile
  createdAt: number
}

function slugPart(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 24) || 'value'
}

function suggestedId(seed: ProfileDraftSeed): string {
  const kind = seed.stage === 'provider' ? 'provider' : 'target'
  if (seed.inputType === 'enum' && seed.outputType === 'enum') {
    return `${kind}-${slugPart(seed.sourceLabel ?? 'source')}-to-${slugPart(seed.targetLabel ?? 'target')}`
  }
  return `${kind}-${seed.inputType}-to-${seed.outputType}`
}

function defaultEnumValues(sourceEnum: string[] = [], targetEnum: string[] = []): Record<string, string> {
  const values: Record<string, string> = {}
  const remainingTargets = [...targetEnum]
  const normalize = (value: string) => value.trim().toLowerCase().replace(/[\s_]+/g, '-').replace(/-+/g, '-').replace(/^-|-$/g, '')
  for (const source of sourceEnum) {
    const exact = remainingTargets.find((item) => item === source)
    if (exact) {
      values[source] = exact
      remainingTargets.splice(remainingTargets.indexOf(exact), 1)
      continue
    }
    const canonical = remainingTargets.find((item) => normalize(item) === normalize(source))
    if (canonical) {
      values[source] = canonical
      remainingTargets.splice(remainingTargets.indexOf(canonical), 1)
      continue
    }
    values[source] = remainingTargets[0] ?? source
    if (remainingTargets.length > 0) remainingTargets.shift()
  }
  return values
}

function defaultTransforms(seed: ProfileDraftSeed): MappingTransform[] {
  if (seed.inputType === 'enum' && seed.outputType === 'enum') {
    return [{ type: 'enum', values: defaultEnumValues(seed.sourceEnum, seed.targetEnum) }]
  }
  if (seed.inputType === 'bool' && seed.outputType === 'bool') {
    return [{ type: 'invert' }]
  }
  if (seed.inputType === 'int' && seed.outputType === 'number') {
    return [{ type: 'int-number' }]
  }
  if ((seed.inputType === 'int' || seed.inputType === 'number') && (seed.outputType === 'int' || seed.outputType === 'number')) {
    return [{ type: 'scale', factor: 1, offset: 0 }]
  }
  if (seed.inputType === 'bool' && seed.outputType === 'enum') {
    return [{ type: 'bool-enum', trueValue: seed.targetEnum?.[0] ?? 'active', falseValue: seed.targetEnum?.[1] ?? 'inactive' }]
  }
  if (seed.inputType === 'enum' && seed.outputType === 'bool') {
    return [{ type: 'enum-bool', trueValue: seed.sourceEnum?.[0] ?? 'active', falseValue: seed.sourceEnum?.[1] ?? 'inactive' }]
  }
  return []
}

export function buildProfileDraft(seed: ProfileDraftSeed): MappingProfile {
  return {
    schemaVersion: 1,
    id: suggestedId(seed),
    version: 1,
    kind: seed.stage === 'provider' ? 'provider' : 'target',
    inputType: seed.inputType,
    outputType: seed.outputType,
    transforms: defaultTransforms(seed),
  }
}

export function storeProfileDraft(seed: ProfileDraftSeed): MappingProfile {
  const draft = buildProfileDraft(seed)
  window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ draft, createdAt: Date.now() } satisfies StoredProfileDraft))
  return draft
}

function parseStoredDraft(raw: string | null): MappingProfile | null {
  if (!raw) return null
  try {
    const parsed = JSON.parse(raw) as { draft?: MappingProfile }
    if (!parsed.draft || typeof parsed.draft !== 'object') return null
    return parsed.draft
  } catch {
    return null
  }
}

function removeHandoffParameter() {
  const url = new URL(window.location.href)
  url.searchParams.delete(HANDOFF_PARAMETER)
  window.history.replaceState(null, '', `${url.pathname}${url.search}${url.hash}`)
}

function handoffID(): string {
  return globalThis.crypto?.randomUUID?.() ?? `${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`
}

export function consumeProfileDraft(): MappingProfile | null {
  const handoff = new URLSearchParams(window.location.search).get(HANDOFF_PARAMETER)
  if (handoff) {
    const key = `${HANDOFF_KEY_PREFIX}${handoff}`
    const draft = parseStoredDraft(window.localStorage.getItem(key))
    window.localStorage.removeItem(key)
    removeHandoffParameter()
    return draft
  }
  const raw = window.sessionStorage.getItem(STORAGE_KEY)
  window.sessionStorage.removeItem(STORAGE_KEY)
  return parseStoredDraft(raw)
}

export function openProfileWorkbench(seed: ProfileDraftSeed, options?: { newTab?: boolean }) {
  if (options?.newTab === false) {
    storeProfileDraft(seed)
    const target = '#/mapping/profiles'
    window.location.hash = target
    return
  }
  const draft = buildProfileDraft(seed)
  const key = `${HANDOFF_KEY_PREFIX}${handoffID()}`
  window.localStorage.setItem(key, JSON.stringify({ draft, createdAt: Date.now() } satisfies StoredProfileDraft))
  const handoff = key.slice(HANDOFF_KEY_PREFIX.length)
  window.open(`?${HANDOFF_PARAMETER}=${encodeURIComponent(handoff)}#/mapping/profiles`, '_blank', 'noopener,noreferrer')
}
