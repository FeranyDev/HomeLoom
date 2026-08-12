import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

afterEach(cleanup)

class TestEventSource {
  onerror: ((event: Event) => void) | null = null
  constructor(public readonly url: string) {}
  addEventListener() {}
  close() {}
}

if (!globalThis.EventSource) Object.defineProperty(globalThis, 'EventSource', { value: TestEventSource, configurable: true })

// Node 26 exposes an undefined global localStorage when no storage file is configured.
// Define browser-shaped storage for cross-tab handoff tests without accessing that global.
const localStorageEntries = new Map<string, string>()
Object.defineProperty(window, 'localStorage', {
  configurable: true,
  value: {
    get length() { return localStorageEntries.size },
    clear: () => localStorageEntries.clear(),
    getItem: (key: string) => localStorageEntries.get(key) ?? null,
    key: (index: number) => [...localStorageEntries.keys()][index] ?? null,
    removeItem: (key: string) => localStorageEntries.delete(key),
    setItem: (key: string, value: string) => localStorageEntries.set(key, String(value)),
  } satisfies Storage,
})
