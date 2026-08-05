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
import { after, beforeEach, test } from 'node:test'

import type { AxiosAdapter } from 'axios'
import { Window } from 'happy-dom'

const domWindow = new Window()
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLButtonElement',
  'HTMLTableElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
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

const originalSetTimeout = globalThis.setTimeout
const originalClearTimeout = globalThis.clearTimeout
let nextTimerId = 1
const debounceTimers = new Map<number, () => void>()
globalThis.setTimeout = ((
  callback: TimerHandler,
  delay?: number,
  ...args: unknown[]
) => {
  if (delay !== 500) {
    return originalSetTimeout(callback, delay, ...args)
  }
  assert.ok(typeof callback === 'function')
  const timerCallback = callback
  const timerId = 1_000_000 + nextTimerId++
  debounceTimers.set(timerId, () => timerCallback(...args))
  return timerId
}) as typeof setTimeout
globalThis.clearTimeout = ((timerId: ReturnType<typeof setTimeout>) => {
  if (!debounceTimers.delete(Number(timerId))) {
    originalClearTimeout(timerId)
  }
}) as typeof clearTimeout

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { api } = await import('@/lib/api')
const { ApiKeysProvider } = await import('../api-keys-provider')
const { ApiKeysTable } = await import('../api-keys-table')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  fallbackLng: 'en',
  resources: {
    en: {
      translation: {
        Reset: 'Reset',
        'Filter by API key...': 'Filter by API key...',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const originalApiAdapter = api.defaults.adapter
const successfulApiAdapter: AxiosAdapter = async (config) => ({
  config,
  data: config.url?.includes('/api/user/self/groups')
    ? { success: true, data: {} }
    : { success: true, data: { items: [], total: 0 } },
  headers: {},
  status: 200,
  statusText: 'OK',
})
api.defaults.adapter = successfulApiAdapter

type ApiKeySearch = {
  page?: number
  pageSize?: number
  status?: string[]
  filter?: string
  token?: string
}

function normalizeSearch(search: Record<string, unknown>): ApiKeySearch {
  return {
    page: typeof search.page === 'number' ? search.page : undefined,
    pageSize: typeof search.pageSize === 'number' ? search.pageSize : undefined,
    status: Array.isArray(search.status)
      ? search.status.filter(
          (status): status is string => typeof status === 'string'
        )
      : [],
    filter: typeof search.filter === 'string' ? search.filter : '',
    token: typeof search.token === 'string' ? search.token : '',
  }
}

function createApiKeysTestRouter(initialEntry: string) {
  const rootRoute = createRootRoute({
    component: Outlet,
  })
  const authenticatedRoute = createRoute({
    getParentRoute: () => rootRoute,
    id: '_authenticated',
    component: Outlet,
  })
  const keysRoute = createRoute({
    getParentRoute: () => authenticatedRoute,
    path: 'keys',
    component: Outlet,
  })
  const keysIndexRoute = createRoute({
    getParentRoute: () => keysRoute,
    path: '/',
    validateSearch: normalizeSearch,
    component: () => (
      <ApiKeysProvider>
        <ApiKeysTable />
      </ApiKeysProvider>
    ),
  })

  return createRouter({
    routeTree: rootRoute.addChildren([
      authenticatedRoute.addChildren([keysRoute.addChildren([keysIndexRoute])]),
    ]),
    history: createMemoryHistory({ initialEntries: [initialEntry] }),
  })
}

function changeInputValue(input: HTMLInputElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)
  valueSetter.call(input, value)
  input.dispatchEvent(
    new domWindow.Event('input', { bubbles: true }) as unknown as Event
  )
}

function setNativeInputValue(input: HTMLInputElement, value: string) {
  const valueSetter = Object.getOwnPropertyDescriptor(
    domWindow.HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)
  valueSetter.call(input, value)
}

function dispatchCompositionEvent(
  input: HTMLInputElement,
  type: 'compositionstart' | 'compositionend'
) {
  input.dispatchEvent(
    new domWindow.Event(type, { bubbles: true }) as unknown as Event
  )
}

function flushDebounceTimers() {
  const callbacks = [...debounceTimers.values()]
  debounceTimers.clear()
  for (const callback of callbacks) {
    callback()
  }
}

async function renderApiKeys(initialEntry: string) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })
  const router = createApiKeysTestRouter(initialEntry)
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await router.load()
  await act(async () => {
    root.render(
      <QueryClientProvider client={queryClient}>
        <I18nextProvider i18n={i18n}>
          <RouterProvider router={router} />
        </I18nextProvider>
      </QueryClientProvider>
    )
    await Promise.resolve()
  })

  return { container, queryClient, root, router }
}

async function unmountApiKeys(
  rendered: Awaited<ReturnType<typeof renderApiKeys>>
) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
  rendered.queryClient.clear()
}

beforeEach(() => {
  debounceTimers.clear()
  domWindow.localStorage.clear()
})

after(() => {
  debounceTimers.clear()
  api.defaults.adapter = originalApiAdapter
  globalThis.setTimeout = originalSetTimeout
  globalThis.clearTimeout = originalClearTimeout
  domWindow.close()
  for (const [key, descriptor] of globalDescriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('API key token filter defers IME input until composition ends', async () => {
  const rendered = await renderApiKeys('/keys/')

  try {
    const tokenInput = rendered.container.querySelector<HTMLInputElement>(
      'input[aria-label="Filter by API key..."]'
    )
    assert.ok(tokenInput)

    await act(async () => {
      dispatchCompositionEvent(tokenInput, 'compositionstart')
      changeInputValue(tokenInput, '拼')
    })
    await act(async () => {
      flushDebounceTimers()
      await Promise.resolve()
    })

    assert.equal(rendered.router.state.location.search.token ?? '', '')

    await act(async () => {
      setNativeInputValue(tokenInput, '拼音')
      dispatchCompositionEvent(tokenInput, 'compositionend')
    })
    await act(async () => {
      flushDebounceTimers()
      await Promise.resolve()
    })

    assert.equal(rendered.router.state.location.search.token, '拼音')
  } finally {
    await unmountApiKeys(rendered)
  }
})

test('API key Reset clears the global name filter and a pending token edit', async () => {
  const rendered = await renderApiKeys('/keys/?filter=active')

  try {
    const nameInput = rendered.container.querySelector<HTMLInputElement>(
      'input[placeholder="Filter by name..."]'
    )
    const tokenInput = rendered.container.querySelector<HTMLInputElement>(
      'input[aria-label="Filter by API key..."]'
    )
    const resetButton = [...rendered.container.querySelectorAll('button')].find(
      (button) => button.textContent?.trim() === 'Reset'
    )
    assert.ok(nameInput)
    assert.ok(tokenInput)
    assert.ok(resetButton)
    assert.equal(nameInput.value, 'active')

    await act(async () => {
      changeInputValue(nameInput, 'pending-name')
    })
    await act(async () => {
      changeInputValue(tokenInput, 'pending-token')
    })
    await act(async () => {
      resetButton.click()
      await Promise.resolve()
    })
    await act(async () => {
      flushDebounceTimers()
      await Promise.resolve()
    })

    assert.equal(nameInput.value, '')
    assert.equal(tokenInput.value, '')
    assert.equal(rendered.router.state.location.search.filter ?? '', '')
    assert.equal(rendered.router.state.location.search.token ?? '', '')
  } finally {
    await unmountApiKeys(rendered)
  }
})
