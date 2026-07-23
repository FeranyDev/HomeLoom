import { useCallback, useEffect, useMemo, useState } from 'react'
import { createMappingProfile, deleteMappingProfile, importMappingProfiles, listMappingProfiles, updateMappingProfile } from '../api/mapping'
import type { MappingProfile, MappingProfileInfo } from '../types/mapping'
import { ApiError } from '../api/client'
import { profileKindLabel, transformTypeLabel, valueTypeLabel } from '../presentationLabels'
import { ProfileVisualEditor } from './ProfileVisualEditor'

interface ProfileAPI {
  list: () => Promise<MappingProfileInfo[]>
  create: (profile: MappingProfile) => Promise<MappingProfileInfo>
  update: (id: string, profile: MappingProfile) => Promise<MappingProfileInfo>
  remove: (id: string) => Promise<void>
  importMany: (profiles: MappingProfile[]) => Promise<MappingProfileInfo[]>
}

const defaultAPI: ProfileAPI = { list: listMappingProfiles, create: createMappingProfile, update: updateMappingProfile, remove: deleteMappingProfile, importMany: importMappingProfiles }
const starterProfile: MappingProfile = { schemaVersion: 1, id: 'custom-profile', version: 1, kind: 'capability', inputType: 'number', outputType: 'number', transforms: [{ type: 'scale', factor: 1, offset: 0 }] }

function errorText(cause: unknown, fallback: string): string {
  if (cause instanceof ApiError) {
    const details = Object.entries(cause.fields).map(([field, message]) => `${field}: ${message}`).join('；')
    return details ? `${cause.message}；${details}` : cause.message
  }
  return cause instanceof Error ? cause.message : fallback
}

function editableProfile(item: MappingProfileInfo): MappingProfile {
  const profile = { ...item, version: item.version + 1 } as MappingProfile & { builtIn?: boolean }
  delete profile.builtIn
  return profile
}

const profileKindOrder: Record<MappingProfileInfo['kind'], number> = { provider: 0, capability: 1, target: 2 }

export function ProfileManager({ api = defaultAPI, onChanged }: { api?: ProfileAPI; onChanged?: () => void }) {
  const [profiles, setProfiles] = useState<MappingProfileInfo[]>([])
  const [editingID, setEditingID] = useState<string | null>(null)
  const [mode, setMode] = useState<'profile' | 'import' | null>(null)
  const [document, setDocument] = useState('')
  const [draft, setDraft] = useState<MappingProfile>(starterProfile)
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const sortedProfiles = useMemo(() => [...profiles].sort((left, right) =>
    Number(left.builtIn) - Number(right.builtIn)
    || profileKindOrder[left.kind] - profileKindOrder[right.kind]
    || left.id.localeCompare(right.id),
  ), [profiles])
  const refresh = useCallback(async () => { try { setProfiles(await api.list()); setError(null) } catch (cause) { setError(errorText(cause, '加载 Profile 失败')) } }, [api])
  useEffect(() => { void refresh() }, [refresh])
  const openNew = () => { setEditingID(null); setMode('profile'); setDraft(starterProfile); setError(null) }
  const openEdit = (item: MappingProfileInfo) => { setEditingID(item.id); setMode('profile'); setDraft(editableProfile(item)); setError(null) }
  const openImport = () => { setEditingID(null); setMode('import'); setDocument(JSON.stringify({ profiles: [starterProfile] }, null, 2)); setError(null) }
  const saveProfile = async (profile: MappingProfile) => {
    setSaving(true); setError(null)
    try {
      if (editingID) await api.update(editingID, profile)
      else await api.create(profile)
      setMode(null); setEditingID(null); await refresh(); onChanged?.()
    } catch (cause) { setError(errorText(cause, '保存 Profile 失败')) } finally { setSaving(false) }
  }
  const importProfiles = async () => {
    setSaving(true); setError(null)
    try {
      const parsed = JSON.parse(document) as MappingProfile | { profiles?: MappingProfile[] } | MappingProfile[]
      const items = Array.isArray(parsed) ? parsed : 'profiles' in parsed ? parsed.profiles ?? [] : []
      await api.importMany(items)
      setMode(null); await refresh(); onChanged?.()
    } catch (cause) { setError(errorText(cause, '导入 Profile 失败')) } finally { setSaving(false) }
  }
  const remove = async (item: MappingProfileInfo) => { if (!window.confirm(`删除 Profile“${item.id}”？`)) return; try { await api.remove(item.id); await refresh(); onChanged?.() } catch (cause) { setError(errorText(cause, '删除 Profile 失败')) } }

  return <section className="profile-manager">
    <div className="profile-heading"><div><p className="eyebrow">数据库转换配置（DATABASE PROFILES）</p><h3>转换配置（Profile）管理</h3><p>用可复用的转换流水线连接提供端属性、统一模型与消费端属性。保存后热更新到内存快照，无需重启 Provider。</p></div><div><button className="add-button" onClick={openNew}>＋ 新建转换配置</button><button onClick={openImport}>导入 JSON</button><a href="/api/v1/mapping/profiles/export" download>导出用户 Profile</a></div></div>
    {error && <p className="field-error" role="alert">{error}</p>}
    {mode === 'profile' && <ProfileVisualEditor key={`${editingID ?? 'new'}-${draft.version}`} initialProfile={draft} editing={editingID !== null} saving={saving} onClose={() => setMode(null)} onSave={saveProfile} />}
    {mode === 'import' && <div className="profile-editor profile-importer"><div><strong>批量导入</strong><button aria-label="关闭 Profile 编辑器" onClick={() => setMode(null)}>×</button></div><textarea aria-label="Profile JSON" rows={18} value={document} onChange={(event) => setDocument(event.target.value)} spellCheck={false} /><p>批量导入会在单个事务中全部成功或全部失败；导入后立即热更新。</p><button className="add-button" disabled={saving} onClick={() => void importProfiles()}>{saving ? '导入中…' : '验证并导入'}</button></div>}
    <div className="profile-list-heading"><strong>已保存配置</strong><span>用户配置优先；同类配置按用途与标识排序</span></div>
    <div className="profile-list">{sortedProfiles.map((item) => <article key={item.id}><span>{item.builtIn ? '内置模板' : '用户配置'} · {profileKindLabel(item.kind)} · 版本（v）{item.version}</span><strong>{item.id}</strong><code>{valueTypeLabel(item.inputType)} → {valueTypeLabel(item.outputType)}</code><small>{item.transforms.map((transform) => transformTypeLabel(transform.type)).join(' → ') || '恒等转换（identity）'}</small><div>{item.builtIn ? <i>内置只读</i> : <><button onClick={() => openEdit(item)}>编辑</button><button onClick={() => void remove(item)}>删除</button></>}</div></article>)}</div>
  </section>
}
