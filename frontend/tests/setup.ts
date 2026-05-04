import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach, beforeAll, vi } from 'vitest'

// Browser API stubs that jsdom does not provide but shadcn/Radix
// primitives (Dialog, Select) expect on mount.
beforeAll(() => {
  if (!globalThis.HTMLElement.prototype.hasPointerCapture) {
    globalThis.HTMLElement.prototype.hasPointerCapture = () => false
  }
  if (!globalThis.HTMLElement.prototype.scrollIntoView) {
    globalThis.HTMLElement.prototype.scrollIntoView = () => {}
  }
  Object.defineProperty(window, 'scrollTo', {
    writable: true,
    value: () => {},
  })
  if (!globalThis.ResizeObserver) {
    globalThis.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    } as unknown as typeof ResizeObserver
  }
  if (!window.matchMedia) {
    Object.defineProperty(window, 'matchMedia', {
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    })
  }
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})
