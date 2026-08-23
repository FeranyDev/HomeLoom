import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'

function safeLink(value?: string): string | undefined {
  if (!value) return undefined
  try {
    const base = typeof window === 'undefined' ? 'https://homeloom.local' : window.location.origin
    const url = new URL(value, base)
    return url.protocol === 'https:' || url.protocol === 'http:' || url.protocol === 'mailto:' ? value : undefined
  } catch {
    return undefined
  }
}

export function MarkdownMessage({ content }: { content: string }) {
  return <div className="markdown-message">
    <ReactMarkdown remarkPlugins={[remarkGfm]} skipHtml components={{
      a: ({ href, children }) => {
        const safeHref = safeLink(href)
        return safeHref ? <a href={safeHref} target="_blank" rel="noreferrer">{children}</a> : <>{children}</>
      },
    }}>
      {content}
    </ReactMarkdown>
  </div>
}
