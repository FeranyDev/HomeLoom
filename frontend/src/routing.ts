import { useCallback, useEffect, useState } from 'react'

export type Page = 'devices' | 'providers' | 'targets' | 'mapping' | 'system'
const pages = new Set<Page>(['devices', 'providers', 'targets', 'mapping', 'system'])
function routeName(): string { return window.location.hash.replace(/^#\/?/, '').split('/')[0] }
function currentPage(): Page { const value = routeName(); if (value === 'xiaomi') return 'providers'; return pages.has(value as Page) ? value as Page : 'devices' }

export function usePageRoute(): [Page, (page: Page) => void] {
  const [page, setPage] = useState(currentPage)
  useEffect(() => { if (!window.location.hash) window.history.replaceState(null, '', '#/devices'); else if (routeName() === 'xiaomi') window.history.replaceState(null, '', '#/providers'); const update = () => { if (routeName() === 'xiaomi') window.history.replaceState(null, '', '#/providers'); setPage(currentPage()) }; window.addEventListener('hashchange', update); return () => window.removeEventListener('hashchange', update) }, [])
  const navigate = useCallback((next: Page) => { if (currentPage() === next) return; window.location.hash = `/${next}` }, [])
  return [page, navigate]
}
