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
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ seed, draft, createdAt: Date.now() }))
  return draft
}

export function consumeProfileDraft(): MappingProfile | null {
  const raw = sessionStorage.getItem(STORAGE_KEY)
  if (!raw) return null
  sessionStorage.removeItem(STORAGE_KEY)
  try {
    const parsed = JSON.parse(raw) as { draft?: MappingProfile }
    if (!parsed.draft || typeof parsed.draft !== 'object') return null
    return parsed.draft
  } catch {
    return null
  }
}

export function openProfileWorkbench(seed: ProfileDraftSeed, options?: { newTab?: boolean }) {
  storeProfileDraft(seed)
  const target = '#/mapping/profiles'
  if (options?.newTab === false) {
    window.location.hash = target
    return
  }
  window.open(target, '_blank', 'noopener,noreferrer')
}
