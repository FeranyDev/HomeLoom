import { useState } from 'react'
import type { CommandDefinition, PropertyValue } from '../types/device'

function parameterValue(type: string, raw: string): PropertyValue {
  if (type === 'bool') return { type: 'bool', bool: raw === 'true' }
  if (type === 'number') return { type: 'number', number: Number(raw) }
  return { type: type as 'string' | 'enum', string: raw }
}

export function CommandControl({ definition, onExecute }: { definition: CommandDefinition; onExecute: (parameters: Record<string, PropertyValue>) => Promise<void> }) {
  const [drafts, setDrafts] = useState<Record<string, string>>(() => Object.fromEntries(definition.parameters?.map((parameter) => [parameter.id, parameter.type === 'bool' ? 'false' : '']) ?? []))
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const execute = async () => {
    const parameters: Record<string, PropertyValue> = {}
    for (const parameter of definition.parameters ?? []) {
      const raw = drafts[parameter.id] ?? ''
      if (parameter.required && raw === '') { setError(`${parameter.name}不能为空`); return }
      if (raw !== '') parameters[parameter.id] = parameterValue(parameter.type, raw)
    }
    setPending(true); setError(null)
    try { await onExecute(parameters) } catch (cause) { setError(cause instanceof Error ? cause.message : '命令执行失败') } finally { setPending(false) }
  }
  return <div className="command-control"><div><strong>{definition.name}</strong><code>{definition.id}</code></div>{definition.parameters?.map((parameter) => <label key={parameter.id}><span>{parameter.name}{parameter.required ? ' *' : ''}</span>{parameter.type === 'bool' ? <select value={drafts[parameter.id]} onChange={(event) => setDrafts((current) => ({ ...current, [parameter.id]: event.target.value }))}><option value="false">false</option><option value="true">true</option></select> : <input type={parameter.type === 'number' ? 'number' : 'text'} value={drafts[parameter.id] ?? ''} onChange={(event) => setDrafts((current) => ({ ...current, [parameter.id]: event.target.value }))} />}</label>)}<button disabled={pending} onClick={() => void execute()}>{pending ? '执行中…' : '执行命令'}</button>{error && <small>{error}</small>}</div>
}
