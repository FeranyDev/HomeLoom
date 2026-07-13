import { useState } from 'react'
import type { PropertyDefinition, PropertyValue } from '../types/device'

function initial(value: PropertyValue): string { if (value.int !== undefined) return String(value.int); if (value.number !== undefined) return String(value.number); return value.string ?? '' }

export function PropertyControl({ definition, value, onWrite }: { definition: PropertyDefinition; value: PropertyValue; onWrite: (value: PropertyValue) => Promise<void> }) {
  const [draft, setDraft] = useState(initial(value)); const [pending, setPending] = useState(false); const [error, setError] = useState<string | null>(null)
  const write = async (next: PropertyValue) => { setPending(true); setError(null); try { await onWrite(next) } catch (cause) { setError(cause instanceof Error ? cause.message : '写入失败') } finally { setPending(false) } }
	const writeDraft = () => {
		if (definition.type === 'int') {
			const parsed = Number(draft)
			if (!Number.isSafeInteger(parsed)) { setError('请输入有效整数'); return }
			void write({ type: 'int', int: parsed }); return
		}
		if (definition.type === 'number') { void write({ type: 'number', number: Number(draft) }); return }
		void write({ type: definition.type, string: draft })
	}
  if (!definition.writable) return null
  return <div className="property-control">{definition.type === 'bool' ? <button disabled={pending} onClick={() => void write({ type: 'bool', bool: !value.bool })}>{pending ? '写入中…' : value.bool ? '设为 false' : '设为 true'}</button> : <><label>{definition.type === 'enum' ? <select value={draft} onChange={(event) => setDraft(event.target.value)}>{definition.enum?.map((item) => <option key={item}>{item}</option>)}</select> : <input type={definition.type === 'number' || definition.type === 'int' ? 'number' : 'text'} min={definition.min} max={definition.max} step={definition.type === 'int' ? definition.step ?? 1 : definition.step} value={draft} onChange={(event) => setDraft(event.target.value)} />}</label><button disabled={pending || draft === ''} onClick={writeDraft}>{pending ? '写入中…' : '写入属性'}</button></>}{error && <small>{error}</small>}</div>
}
