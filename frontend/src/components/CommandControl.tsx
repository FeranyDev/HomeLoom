import { useState } from 'react'
import type { CommandDefinition, PropertyValue } from '../types/device'

function parameterValue(type: string, raw: string): PropertyValue {
  if (type === 'bool') return { type: 'bool', bool: raw === 'true' }
	if (type === 'int') return { type: 'int', int: Number.parseInt(raw, 10) }
  if (type === 'number') return { type: 'number', number: Number(raw) }
  return { type: type as 'string' | 'enum', string: raw }
}

export function CommandControl({ definition, onExecute }: { definition: CommandDefinition; onExecute: (parameters: Record<string, PropertyValue>, idempotencyKey: string) => Promise<void> }) {
  const [drafts, setDrafts] = useState<Record<string, string>>(() => Object.fromEntries(definition.parameters?.map((parameter) => [parameter.id, parameter.type === 'bool' ? 'false' : '']) ?? []))
  const [pending, setPending] = useState(false)
	const [error, setError] = useState<string | null>(null)
	const [idempotencyKey, setIdempotencyKey] = useState<string | null>(null)
  const execute = async () => {
    const parameters: Record<string, PropertyValue> = {}
    for (const parameter of definition.parameters ?? []) {
      const raw = drafts[parameter.id] ?? ''
      if (parameter.required && raw === '') { setError(`${parameter.name}不能为空`); return }
			if (parameter.type === 'int' && raw !== '' && !Number.isSafeInteger(Number(raw))) { setError(`${parameter.name}必须是整数`); return }
      if (raw !== '') parameters[parameter.id] = parameterValue(parameter.type, raw)
    }
		const key = idempotencyKey ?? crypto.randomUUID(); setIdempotencyKey(key); setPending(true); setError(null)
		try { await onExecute(parameters, key); setIdempotencyKey(null) } catch (cause) { setError(cause instanceof Error ? cause.message : '命令执行失败') } finally { setPending(false) }
  }
  return <div className="command-control"><div><strong>{definition.name}</strong><code>{definition.id}</code><small>{definition.idempotent ? '可安全重放' : '禁止自动重试'}</small></div>{definition.parameters?.map((parameter) => <label key={parameter.id}><span>{parameter.name}{parameter.required ? ' *' : ''}</span>{parameter.type === 'bool' ? <select value={drafts[parameter.id]} onChange={(event) => setDrafts((current) => ({ ...current, [parameter.id]: event.target.value }))}><option value="false">false</option><option value="true">true</option></select> : <input type={parameter.type === 'number' || parameter.type === 'int' ? 'number' : 'text'} step={parameter.type === 'int' ? 1 : undefined} value={drafts[parameter.id] ?? ''} onChange={(event) => setDrafts((current) => ({ ...current, [parameter.id]: event.target.value }))} />}</label>)}<button disabled={pending} onClick={() => void execute()}>{pending ? '执行中…' : '执行命令'}</button>{error && <small>{error}</small>}</div>
}
