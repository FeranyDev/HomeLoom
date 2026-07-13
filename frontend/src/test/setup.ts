import '@testing-library/jest-dom/vitest'

class TestEventSource {
  onerror: ((event: Event) => void) | null = null
  constructor(public readonly url: string) {}
  addEventListener() {}
  close() {}
}

if (!globalThis.EventSource) Object.defineProperty(globalThis, 'EventSource', { value: TestEventSource, configurable: true })
