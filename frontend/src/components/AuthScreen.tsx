import { useState, type FormEvent } from 'react'
import { ApiError } from '../api/client'

interface Props {
	initialized: boolean
	onSubmit: (username: string, password: string) => Promise<void>
}

export function AuthScreen({ initialized, onSubmit }: Props) {
	const [username, setUsername] = useState('admin')
	const [password, setPassword] = useState('')
	const [confirmation, setConfirmation] = useState('')
	const [error, setError] = useState<string | null>(null)
	const [submitting, setSubmitting] = useState(false)

	async function submit(event: FormEvent) {
		event.preventDefault()
		if (!initialized && password !== confirmation) {
			setError('两次输入的密码不一致')
			return
		}
		setSubmitting(true)
		setError(null)
		try {
			await onSubmit(username, password)
		} catch (cause) {
			if (cause instanceof ApiError && Object.keys(cause.fields).length > 0) {
				setError(Object.values(cause.fields).join('；'))
			} else {
				setError(cause instanceof Error ? cause.message : '认证失败')
			}
		} finally {
			setSubmitting(false)
		}
	}

	return <main className="auth-shell">
		<section className="auth-card" aria-labelledby="auth-title">
			<p className="eyebrow">HOMELOOM · ADMIN</p>
			<h1 id="auth-title">{initialized ? '欢迎回来。' : '先系好第一根线。'}</h1>
			<p>{initialized ? '登录后管理设备、Provider 与桥接配置。' : '创建本机唯一的管理员账户。配置和登录会话将保存在 SQLite 中。'}</p>
			<form onSubmit={(event) => void submit(event)}>
				<label>用户名<input autoComplete="username" autoFocus value={username} onChange={(event) => setUsername(event.target.value)} minLength={3} maxLength={64} required /></label>
				<label>密码<input type="password" autoComplete={initialized ? 'current-password' : 'new-password'} value={password} onChange={(event) => setPassword(event.target.value)} minLength={12} maxLength={128} required /></label>
				{!initialized && <label>确认密码<input type="password" autoComplete="new-password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} minLength={12} maxLength={128} required /></label>}
				{error && <p className="inline-error" role="alert">{error}</p>}
				<button disabled={submitting}>{submitting ? '处理中…' : initialized ? '登录' : '创建管理员'}</button>
			</form>
			<small>{initialized ? '连续失败 5 次后，同一客户端将暂时锁定 5 分钟。' : '密码至少 12 个字符；HomeLoom 不保存密码明文。'}</small>
		</section>
	</main>
}
