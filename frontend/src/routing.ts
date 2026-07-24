import { useCallback, useEffect, useState } from 'react'

export type Page = 'devices' | 'providers' | 'targets' | 'mapping' | 'system'
export type MappingSection = 'models' | 'profiles'

const pages = new Set<Page>(['devices', 'providers', 'targets', 'mapping', 'system'])
const mappingSections = new Set<MappingSection>(['models', 'profiles'])

function routeParts(): string[] {
  return window.location.hash.replace(/^#\/?/, '').split('/').filter(Boolean)
}

function routeName(): string {
  return routeParts()[0] ?? ''
}

function currentPage(): Page {
  const value = routeName()
  if (value === 'xiaomi') return 'providers'
  return pages.has(value as Page) ? value as Page : 'devices'
}

function currentMappingSection(): MappingSection {
  const section = routeParts()[1]
  return mappingSections.has(section as MappingSection) ? section as MappingSection : 'models'
}

function hashFor(page: Page, mappingSection?: MappingSection): string {
  if (page === 'mapping') return `#/mapping/${mappingSection ?? currentMappingSection()}`
  return `#/${page}`
}

export function usePageRoute(): [Page, (page: Page, options?: { mappingSection?: MappingSection }) => void] {
  const [page, setPage] = useState(currentPage)
  useEffect(() => {
    if (!window.location.hash) window.history.replaceState(null, '', '#/devices')
    else if (routeName() === 'xiaomi') window.history.replaceState(null, '', '#/providers')
    const update = () => {
      if (routeName() === 'xiaomi') window.history.replaceState(null, '', '#/providers')
      setPage(currentPage())
    }
    window.addEventListener('hashchange', update)
    return () => window.removeEventListener('hashchange', update)
  }, [])
  const navigate = useCallback((next: Page, options?: { mappingSection?: MappingSection }) => {
    const target = hashFor(next, options?.mappingSection)
    if (window.location.hash === target) return
    window.location.hash = target
  }, [])
  return [page, navigate]
}

export function useMappingSection(): [MappingSection, (section: MappingSection) => void] {
  const [section, setSection] = useState(currentMappingSection)
  useEffect(() => {
    const update = () => setSection(currentMappingSection())
    window.addEventListener('hashchange', update)
    return () => window.removeEventListener('hashchange', update)
  }, [])
  const navigate = useCallback((next: MappingSection) => {
    const target = `#/mapping/${next}`
    if (window.location.hash === target) return
    window.location.hash = target
  }, [])
  return [section, navigate]
}
