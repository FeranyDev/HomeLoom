export function confirmTargetDeletion(name: string): boolean {
  return window.confirm(`确定删除“${name}”吗？配对资料目录不会自动删除。`)
}

export function confirmProviderDeletion(name: string): boolean {
  return window.confirm(`确定删除“${name}”吗？其设备将立即离线。`)
}

export function confirmExactPhrase(message: string, phrase: string): string | null {
	return window.prompt(`${message}\n请输入 ${phrase} 继续。`) === phrase ? phrase : null
}
