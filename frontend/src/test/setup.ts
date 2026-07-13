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
