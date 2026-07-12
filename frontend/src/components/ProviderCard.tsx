import type { Provider } from '../types/provider'

export function ProviderCard({ provider, onEdit, onDelete }: { provider: Provider; onEdit: (provider: Provider) => void; onDelete: (provider: Provider) => void }) {
  const features = Object.entries(provider.capabilities || {}).filter(([, enabled]) => enabled).map(([name]) => name)
  return <article className="provider-card">
    <div className="device-card__topline"><span className={`status-dot ${provider.status === 'running' ? 'is-online' : ''}`} />{provider.status}<span className="provider">{provider.type}</span></div>
    <p className="target-id">{provider.id}</p><h2>{provider.name}</h2>
    <div className="capability-list">{features.length ? features.map((item) => <span key={item}>{item}</span>) : <span>未运行</span>}</div>
    {provider.error && <p className="inline-error">{provider.error}</p>}
    <div className="target-actions"><button onClick={() => onEdit(provider)}>编辑</button><button className="is-danger" onClick={() => onDelete(provider)}>删除</button></div>
  </article>
}
