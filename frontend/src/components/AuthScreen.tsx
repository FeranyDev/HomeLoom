import { useState, type FormEvent } from 'react'
import { ApiError } from '../api/client'
import { BrandMark } from './BrandMark'
import { HelpTooltip } from './HelpTooltip'

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
			<div className="auth-brand"><BrandMark /></div>
			<p className="eyebrow">HOMELOOM · ADMIN</p>
			<h1 id="auth-title"><HelpTooltip label={initialized ? '登录说明' : '创建管理员说明'} content={initialized ? '登录后可管理设备、来源和桥接。' : '创建此实例的管理员。密码至少 12 个字符，原文不会保存。'}>{initialized ? '登录' : '创建管理员'}</HelpTooltip></h1>
			<form onSubmit={(event) => void submit(event)}>
				<label><HelpTooltip label="登录限制说明" content="连续失败 5 次后，此客户端会锁定 5 分钟。">用户名</HelpTooltip><input aria-label="用户名" autoComplete="username" autoFocus value={username} onChange={(event) => setUsername(event.target.value)} minLength={3} maxLength={64} required /></label>
				<label>密码<input type="password" autoComplete={initialized ? 'current-password' : 'new-password'} value={password} onChange={(event) => setPassword(event.target.value)} minLength={12} maxLength={128} required /></label>
				{!initialized && <label>确认密码<input type="password" autoComplete="new-password" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} minLength={12} maxLength={128} required /></label>}
				{error && <p className="inline-error" role="alert">{error}</p>}
				<button disabled={submitting}>{submitting ? '处理中…' : initialized ? '登录' : '创建管理员'}</button>
			</form>

		</section>
	</main>
}
