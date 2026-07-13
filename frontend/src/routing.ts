import { useCallback, useEffect, useState } from 'react'

export type Page = 'devices' | 'providers' | 'targets' | 'system'
const pages = new Set<Page>(['devices', 'providers', 'targets', 'system'])
function currentPage(): Page { const value = window.location.hash.replace(/^#\/?/, '').split('/')[0] as Page; return pages.has(value) ? value : 'devices' }

export function usePageRoute(): [Page, (page: Page) => void] {
  const [page, setPage] = useState(currentPage)
  useEffect(() => { if (!window.location.hash) window.history.replaceState(null, '', '#/devices'); const update = () => setPage(currentPage()); window.addEventListener('hashchange', update); return () => window.removeEventListener('hashchange', update) }, [])
  const navigate = useCallback((next: Page) => { if (currentPage() === next) return; window.location.hash = `/${next}` }, [])
  return [page, navigate]
}
