import { Component, type ErrorInfo, type ReactNode } from 'react'

export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state = { error: null as Error | null }
  static getDerivedStateFromError(error: Error) { return { error } }
  componentDidCatch(error: Error, info: ErrorInfo) { console.error('HomeLoom UI crashed', error, info.componentStack) }
  render() { if (this.state.error) return <main className="fatal-error"><p className="eyebrow">UI ERROR</p><h1>控制台遇到了问题。</h1><p>{this.state.error.message}</p><div><button onClick={() => this.setState({ error: null })}>重试渲染</button><button onClick={() => window.location.reload()}>重新载入</button></div></main>; return this.props.children }
}
