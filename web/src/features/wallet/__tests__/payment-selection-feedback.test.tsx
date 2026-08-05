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
domWindow.document.write(
  '<!doctype html><html><head></head><body></body></html>'
)
Object.defineProperty(domWindow.document, 'compatMode', {
  configurable: true,
  value: 'CSS1Compat',
})
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'localStorage',
  'Storage',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLInputElement',
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
  'Image',
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
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { toast } = await import('sonner')
const { api } = await import('@/lib/api')
const { Wallet } = await import('../index')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: { en: { translation: {} } },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const originalGet = api.get
const originalPost = api.post
const originalToastError = toast.error

function successfulTopupInfo() {
  return {
    data: {
      success: true,
      data: {
        amount_options: [],
        discount: {},
        enable_online_topup: true,
        enable_stripe_topup: true,
        min_topup: 10,
        pay_methods: [
          {
            min_topup: 10,
            name: 'Stripe',
            type: 'stripe',
          },
        ],
        stripe_min_topup: 10,
      },
    },
  }
}

after(() => {
  api.get = originalGet
  api.post = originalPost
  toast.error = originalToastError
  domWindow.close()
  for (const [key, descriptor] of globalDescriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('reports a current payment calculation failure to the user', async () => {
  const errorMessages: string[] = []
  const apiCalls: string[] = []
  toast.error = ((message: string | number) => {
    errorMessages.push(String(message))
    return String(errorMessages.length)
  }) as typeof toast.error
  api.get = (async (url: string) => {
    apiCalls.push(`GET ${url}`)
    if (url === '/api/user/topup/info') {
      return successfulTopupInfo()
    }
    if (url === '/api/status') {
      return { data: { data: { price: 1 } } }
    }
    if (url === '/api/subscription/plans') {
      return { data: { success: true, data: [] } }
    }
    if (url === '/api/subscription/self') {
      return {
        data: {
          success: true,
          data: {
            all_subscriptions: [],
            billing_preference: 'wallet_first',
            subscriptions: [],
          },
        },
      }
    }
    return { data: { success: false } }
  }) as typeof api.get
  api.post = (async (url: string) => {
    apiCalls.push(`POST ${url}`)
    if (url === '/api/user/stripe/amount') {
      return { data: { success: true, data: '0' } }
    }
    return { data: { success: false } }
  }) as typeof api.post

  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  })
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <Wallet />
          </I18nextProvider>
        </QueryClientProvider>
      )
      await Promise.resolve()
      await Promise.resolve()
      await Promise.resolve()
    })

    const stripe = container.querySelector<HTMLButtonElement>(
      'button[aria-label="Stripe"]'
    )
    assert.ok(stripe)
    assert.equal(stripe.disabled, false)
    errorMessages.length = 0

    await act(async () => {
      stripe.click()
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.deepEqual(errorMessages, ['Payment request failed'])
    assert.equal(
      apiCalls.filter((call) => call === 'POST /api/user/stripe/amount').length,
      2
    )
  } finally {
    await act(async () => root.unmount())
    container.remove()
    queryClient.clear()
    api.get = originalGet
    api.post = originalPost
    toast.error = originalToastError
  }
})
