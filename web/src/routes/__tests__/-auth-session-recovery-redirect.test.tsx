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

const sourceHref = 'https://dashboard.example.com/usage-logs?channel=7#row-9'
const domWindow = new Window({ url: sourceHref })
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MessageEvent',
  'MutationObserver',
  'ResizeObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
  'scrollTo',
] as const
const originalGlobals = new Map<
  (typeof domGlobalKeys)[number],
  PropertyDescriptor | undefined
>()
for (const key of domGlobalKeys) {
  originalGlobals.set(key, Object.getOwnPropertyDescriptor(globalThis, key))
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

type MessageListener = (event: MessageEvent<unknown>) => void

class FakeBroadcastChannel {
  static instances: FakeBroadcastChannel[] = []

  readonly name: string
  private readonly messageListeners = new Set<MessageListener>()

  constructor(name: string) {
    this.name = name
    FakeBroadcastChannel.instances.push(this)
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    if (type !== 'message' || typeof listener !== 'function') return
    this.messageListeners.add(listener as MessageListener)
  }

  removeEventListener(
    type: string,
    listener: EventListenerOrEventListenerObject
  ) {
    if (type !== 'message' || typeof listener !== 'function') return
    this.messageListeners.delete(listener as MessageListener)
  }

  postMessage(_message: unknown) {}

  close() {
    this.messageListeners.clear()
  }

  dispatch(data: unknown) {
    const event = new domWindow.MessageEvent('message', {
      data,
    }) as unknown as MessageEvent<unknown>
    for (const listener of this.messageListeners) {
      listener(event)
    }
  }
}

const broadcastChannelDescriptor = Object.getOwnPropertyDescriptor(
  globalThis,
  'BroadcastChannel'
)
Object.defineProperty(globalThis, 'BroadcastChannel', {
  configurable: true,
  value: FakeBroadcastChannel,
})

const fetchDescriptor = Object.getOwnPropertyDescriptor(globalThis, 'fetch')
Object.defineProperty(globalThis, 'fetch', {
  configurable: true,
  value: async () =>
    new Response(
      JSON.stringify({
        success: true,
        data: {},
      }),
      {
        headers: { 'Content-Type': 'application/json' },
        status: 200,
      }
    ),
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const {
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { Route } = await import('../__root')
const { useAuthStore } = await import('@/stores/auth-store')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function createRootTestRouter() {
  const RootComponent = Route.options.component
  assert.ok(RootComponent)

  const rootRoute = createRootRoute({
    component: RootComponent,
  })
  const usageLogsRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: 'usage-logs',
    component: () => null,
  })
  const signInRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: 'sign-in',
    validateSearch: (search: Record<string, unknown>) => ({
      redirect:
        typeof search.redirect === 'string' ? search.redirect : undefined,
    }),
    component: () => null,
  })

  return createRouter({
    routeTree: rootRoute.addChildren([usageLogsRoute, signInRoute]),
    history: createMemoryHistory({
      initialEntries: ['/usage-logs?channel=7#row-9'],
    }),
  })
}

after(() => {
  useAuthStore.getState().auth.reset('idle')
  domWindow.close()
  if (fetchDescriptor) {
    Object.defineProperty(globalThis, 'fetch', fetchDescriptor)
  } else {
    Reflect.deleteProperty(globalThis, 'fetch')
  }
  if (broadcastChannelDescriptor) {
    Object.defineProperty(
      globalThis,
      'BroadcastChannel',
      broadcastChannelDescriptor
    )
  } else {
    Reflect.deleteProperty(globalThis, 'BroadcastChannel')
  }
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('a matching cross-tab sign-out preserves the receiving tab route', async () => {
  useAuthStore.getState().auth.setBundle({
    access_token: 'access-token',
    token_type: 'Bearer',
    access_expires_at: 1_900_000_000,
    user: { id: 1, username: 'test-user', role: 1 },
    session: {
      sid: 'session-a',
      current: true,
      login_method: 'password',
      ip: '127.0.0.1',
      user_agent: 'test',
      created_at: 1,
      last_active_at: 1,
      expires_at: 1_900_000_000,
    },
  })

  const queryClient = new QueryClient()
  queryClient.setQueryData(['private'], 'secret')
  const router = createRootTestRouter()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await router.load()
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>
      )
      await Promise.resolve()
    })

    const subscriber = FakeBroadcastChannel.instances.find(
      (channel) => channel.name === 'new-api:auth-session'
    )
    assert.ok(subscriber)

    await act(async () => {
      subscriber.dispatch({
        kind: 'signed_out',
        sid: 'session-a',
        source: 'remote-tab',
        nonce: 'remote-event',
        timestamp: Date.now(),
      })
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.equal(useAuthStore.getState().auth.user, null)
    assert.equal(queryClient.getQueryData(['private']), undefined)
    assert.equal(router.state.location.pathname, '/sign-in')
    assert.equal(
      (router.state.location.search as { redirect?: string }).redirect,
      '/usage-logs?channel=7#row-9'
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
    queryClient.clear()
    useAuthStore.getState().auth.reset('idle')
  }
})
