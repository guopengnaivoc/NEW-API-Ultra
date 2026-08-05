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

import { api } from '@/lib/api'

const domWindow = new Window()
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'Storage',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'localStorage',
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

const bunTestModule: string = 'bun:test'
const { mock } = await import(bunTestModule)
const { act, createElement } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')

const flowRows = [
  {
    user_id: 1,
    username: 'alice',
    token_id: 11,
    token_name: 'primary',
    use_group: 'vip',
    model_name: 'gpt-4.1',
    quota: 100,
    token_used: 40,
    count: 2,
  },
]

mock.module('@visactor/react-vchart', () => ({
  VChart: () => createElement('div', { 'data-chart-rendered': 'true' }),
}))
mock.module('@/lib/use-chart-theme', () => ({
  useChartTheme: () => ({
    resolvedTheme: 'light',
    retryTheme: () => undefined,
    themeError: new Error('transient theme failure'),
    themeReady: false,
  }),
}))

const { FlowCharts } = await import('../flow-charts')

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
const originalGet = api.get

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((promiseResolve) => {
    resolve = promiseResolve
  })
  return { promise, resolve }
}

after(() => {
  mock.restore()
  domWindow.close()
  for (const [key, descriptor] of globalDescriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('keeps the flow loading skeleton visible until data finishes despite a theme failure', async () => {
  const response = deferred<{
    data: { success: boolean; data: typeof flowRows }
  }>()
  api.get = (async (url: string) => {
    assert.equal(url, '/api/data/flow/self')
    return response.promise
  }) as typeof api.get
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  })
  const queryReady = new Promise<void>((resolve) => {
    const unsubscribe = queryClient.getQueryCache().subscribe((event) => {
      if (event.query.state.status !== 'success') return
      unsubscribe()
      resolve()
    })
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <FlowCharts />
          </I18nextProvider>
        </QueryClientProvider>
      )
    })

    assert.ok(container.querySelector('[data-slot="skeleton"]'))
    assert.equal(container.querySelector('[role="alert"]'), null)

    await act(async () => {
      response.resolve({
        data: {
          success: true,
          data: flowRows,
        },
      })
      await queryReady
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <FlowCharts sensitiveVisible />
          </I18nextProvider>
        </QueryClientProvider>
      )
    })

    assert.ok(container.querySelector('[role="alert"]'))
  } finally {
    await act(async () => root.unmount())
    container.remove()
    queryClient.clear()
    api.get = originalGet
  }
})
