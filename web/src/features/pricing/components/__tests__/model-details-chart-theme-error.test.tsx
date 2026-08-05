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

const domWindow = new Window()
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

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ThemeManager } = await import('@visactor/vchart')
const { ThemeProvider } = await import('@/context/theme-provider')
const { LatencyTrendChart } = await import('../model-details-charts')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Failed to load': 'Failed to load',
        'Please try again later.': 'Please try again later.',
        Retry: 'Retry',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

after(() => {
  domWindow.close()
  for (const [key, descriptor] of globalDescriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('a chart theme failure exposes translated in-place recovery', async () => {
  const originalSetCurrentTheme = ThemeManager.setCurrentTheme
  let applicationAttempts = 0
  ThemeManager.setCurrentTheme = () => {
    applicationAttempts += 1
    throw new Error('transient theme application failure')
  }

  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <I18nextProvider i18n={i18n}>
          <ThemeProvider
            defaultTheme='dark'
            storageKey='chart-theme-caller-test'
          >
            <LatencyTrendChart
              series={[
                {
                  timestamp: '2026-07-28T00:00:00Z',
                  group: 'default',
                  ttft_ms: 120,
                },
              ]}
            />
          </ThemeProvider>
        </I18nextProvider>
      )
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    const alert = container.querySelector<HTMLElement>('[role="alert"]')
    const retry = container.querySelector<HTMLButtonElement>('button')
    assert.ok(alert)
    assert.match(alert.textContent ?? '', /Failed to load/)
    assert.match(alert.textContent ?? '', /Please try again later\./)
    assert.ok(retry)
    assert.equal(retry.textContent, 'Retry')
    assert.equal(applicationAttempts, 1)

    await act(async () => {
      retry.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.equal(applicationAttempts, 2)
  } finally {
    await act(async () => root.unmount())
    container.remove()
    ThemeManager.setCurrentTheme = originalSetCurrentTheme
  }
})
