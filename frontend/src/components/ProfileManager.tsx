import { useCallback, useEffect, useState } from 'react'
import { createMappingProfile, deleteMappingProfile, importMappingProfiles, listMappingProfiles, updateMappingProfile } from '../api/mapping'
import type { MappingProfile, MappingProfileInfo } from '../types/mapping'
import { ApiError } from '../api/client'
import { profileKindLabel, transformTypeLabel, valueTypeLabel } from '../presentationLabels'

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

export function ProfileManager({ api = defaultAPI, onChanged }: { api?: ProfileAPI; onChanged?: () => void }) {
  const [profiles, setProfiles] = useState<MappingProfileInfo[]>([])
  const [editingID, setEditingID] = useState<string | null>(null)
  const [mode, setMode] = useState<'profile' | 'import' | null>(null)
  const [document, setDocument] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const refresh = useCallback(async () => { try { setProfiles(await api.list()); setError(null) } catch (cause) { setError(errorText(cause, '加载 Profile 失败')) } }, [api])
  useEffect(() => { void refresh() }, [refresh])
  const openNew = () => { setEditingID(null); setMode('profile'); setDocument(JSON.stringify(starterProfile, null, 2)); setError(null) }
  const openEdit = (item: MappingProfileInfo) => { setEditingID(item.id); setMode('profile'); setDocument(JSON.stringify(editableProfile(item), null, 2)); setError(null) }
  const openImport = () => { setEditingID(null); setMode('import'); setDocument(JSON.stringify({ profiles: [starterProfile] }, null, 2)); setError(null) }
  const save = async () => {
    setSaving(true); setError(null)
    try {
      const parsed = JSON.parse(document) as MappingProfile | { profiles?: MappingProfile[] } | MappingProfile[]
      if (mode === 'import') {
        const items = Array.isArray(parsed) ? parsed : 'profiles' in parsed ? parsed.profiles ?? [] : []
        await api.importMany(items)
      } else if (editingID) {
        await api.update(editingID, parsed as MappingProfile)
      } else {
        await api.create(parsed as MappingProfile)
      }
      setMode(null); setEditingID(null); await refresh(); onChanged?.()
    } catch (cause) { setError(errorText(cause, '保存 Profile 失败')) } finally { setSaving(false) }
  }
  const remove = async (item: MappingProfileInfo) => { if (!window.confirm(`删除 Profile“${item.id}”？`)) return; try { await api.remove(item.id); await refresh(); onChanged?.() } catch (cause) { setError(errorText(cause, '删除 Profile 失败')) } }

  return <section className="profile-manager">
    <div className="profile-heading"><div><p className="eyebrow">数据库转换配置（DATABASE PROFILES）</p><h3>转换配置（Profile）管理</h3><p>用户转换配置（Profile）存储在 SQLite；保存、导入或删除后，按标识（ID）预览立即使用最新快照。</p></div><div><button onClick={openNew}>＋ 新建</button><button onClick={openImport}>导入 JSON</button><a href="/api/v1/mapping/profiles/export" download>导出用户 Profile</a></div></div>
    {error && <p className="field-error" role="alert">{error}</p>}
    <div className="profile-list">{profiles.map((item) => <article key={item.id}><span>{profileKindLabel(item.kind)} · 版本（v）{item.version}</span><strong>{item.id}</strong><code>{valueTypeLabel(item.inputType)} → {valueTypeLabel(item.outputType)}</code><small>{item.transforms.map((transform) => transformTypeLabel(transform.type)).join(' → ') || '恒等转换（identity）'}</small><div>{item.builtIn ? <i>内置只读</i> : <><button onClick={() => openEdit(item)}>编辑</button><button onClick={() => void remove(item)}>删除</button></>}</div></article>)}</div>
    {mode && <div className="profile-editor"><div><strong>{mode === 'import' ? '批量导入' : editingID ? `编辑 ${editingID}` : '新建 Profile'}</strong><button aria-label="关闭 Profile 编辑器" onClick={() => setMode(null)}>×</button></div><textarea aria-label="Profile JSON" rows={18} value={document} onChange={(event) => setDocument(event.target.value)} spellCheck={false} /><p>更新已有 Profile 时 version 必须递增；批量导入会在单个事务中全部成功或全部失败。</p><button className="add-button" disabled={saving} onClick={() => void save()}>{saving ? '保存中…' : mode === 'import' ? '验证并导入' : '保存并热更新'}</button></div>}
  </section>
}
