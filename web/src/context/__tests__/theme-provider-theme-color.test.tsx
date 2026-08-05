/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import assert from 'node:assert/strict'
import { after, test } from 'node:test'

import { Window } from 'happy-dom'

const domWindow = new Window({ url: 'https://dashboard.example.com/' })
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const
const globalDescriptors = new Map<
  (typeof domGlobalKeys)[number],
  PropertyDescriptor | undefined
>()
for (const key of domGlobalKeys) {
  globalDescriptors.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

class TestMediaQueryList {
  readonly media = '(prefers-color-scheme: dark)'
  readonly listeners = new Set<EventListener>()
  onchange: ((event: MediaQueryListEvent) => void) | null = null
  matches = true

  addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    if (type === 'change' && typeof listener === 'function') {
      this.listeners.add(listener)
    }
  }

  removeEventListener(
    type: string,
    listener: EventListenerOrEventListenerObject
  ) {
    if (type === 'change' && typeof listener === 'function') {
      this.listeners.delete(listener)
    }
  }

  addListener(listener: (event: MediaQueryListEvent) => void) {
    this.listeners.add(listener as EventListener)
  }

  removeListener(listener: (event: MediaQueryListEvent) => void) {
    this.listeners.delete(listener as EventListener)
  }

  dispatchEvent() {
    return true
  }

  setMatches(matches: boolean) {
    this.matches = matches
    const event = { matches, media: this.media } as MediaQueryListEvent
    this.onchange?.(event)
    for (const listener of this.listeners) {
      listener(event as unknown as Event)
    }
  }
}

const mediaQuery = new TestMediaQueryList()
const matchMediaDescriptor = Object.getOwnPropertyDescriptor(
  domWindow,
  'matchMedia'
)
Object.defineProperty(domWindow, 'matchMedia', {
  configurable: true,
  value: () => mediaQuery,
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { ThemeProvider, useTheme } = await import('../theme-provider')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function ThemeProbe() {
  const { resolvedTheme, setTheme } = useTheme()

  return (
    <>
      <output data-resolved-theme>{resolvedTheme}</output>
      <button type='button' onClick={() => setTheme('dark')}>
        Use dark
      </button>
    </>
  )
}

function getThemeColor(): string | null {
  return (
    document
      .querySelector<HTMLMetaElement>('meta[name="theme-color"]')
      ?.getAttribute('content') ?? null
  )
}

after(() => {
  domWindow.close()
  if (matchMediaDescriptor) {
    Object.defineProperty(domWindow, 'matchMedia', matchMediaDescriptor)
  } else {
    Reflect.deleteProperty(domWindow, 'matchMedia')
  }
  for (const [key, descriptor] of globalDescriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('resolved system theme drives root class and browser theme color', async () => {
  document.cookie = 'theme-color-test=; path=/; max-age=0'
  document.documentElement.classList.remove('light', 'dark')
  document.head.innerHTML = '<meta name="theme-color" content="#fff">'
  mediaQuery.matches = true
  mediaQuery.listeners.clear()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <ThemeProvider defaultTheme='system' storageKey='theme-color-test'>
          <ThemeProbe />
        </ThemeProvider>
      )
      await Promise.resolve()
    })

    const resolvedTheme = () =>
      container.querySelector('[data-resolved-theme]')?.textContent
    assert.equal(resolvedTheme(), 'dark')
    assert.equal(document.documentElement.classList.contains('dark'), true)
    assert.equal(getThemeColor(), '#020817')
    assert.equal(mediaQuery.listeners.size, 1)

    await act(async () => {
      mediaQuery.setMatches(false)
      await Promise.resolve()
    })
    assert.equal(resolvedTheme(), 'light')
    assert.equal(document.documentElement.classList.contains('light'), true)
    assert.equal(getThemeColor(), '#fff')

    const useDark = container.querySelector<HTMLButtonElement>('button')
    assert.ok(useDark)
    await act(async () => {
      useDark.click()
      await Promise.resolve()
    })
    assert.equal(resolvedTheme(), 'dark')
    assert.equal(document.documentElement.classList.contains('dark'), true)
    assert.equal(getThemeColor(), '#020817')

    await act(async () => {
      mediaQuery.setMatches(false)
      await Promise.resolve()
    })
    assert.equal(resolvedTheme(), 'dark')
    assert.equal(document.documentElement.classList.contains('dark'), true)
    assert.equal(getThemeColor(), '#020817')

    await act(async () => root.unmount())
    assert.equal(mediaQuery.listeners.size, 0)
  } finally {
    if (container.isConnected) {
      await act(async () => root.unmount())
    }
    container.remove()
    document.documentElement.classList.remove('light', 'dark')
    document.head.innerHTML = ''
  }
})
