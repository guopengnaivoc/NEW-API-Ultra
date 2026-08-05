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

const domWindow = new Window({
  url: 'https://dashboard.example.com/',
})
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLFormElement',
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

Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
  configurable: true,
  value: () => undefined,
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const {
  Outlet,
  RouterProvider,
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
} = await import('@tanstack/react-router')
const { Route: legacyResetRoute } = await import('../(auth)/reset')
const { Route: canonicalResetRoute } = await import('../(auth)/user/reset')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'auth.resetPasswordConfirm.backToLogin': 'Return to login',
        'auth.resetPasswordConfirm.confirm': 'Confirm reset password',
        'auth.resetPasswordConfirm.description':
          'Enter the code from your email and choose a new password.',
        'Back to login': 'Back to login',
        'Confirm password': 'Confirm password',
        'Enter password (8-20 characters)': 'Enter password (8-20 characters)',
        'Invalid reset link, please request a new password reset.':
          'Invalid reset link, please request a new password reset.',
        Logo: 'Logo',
        'New password': 'New password',
        'Reset password': 'Reset password',
        'Verification code': 'Verification code',
        'Waiting for email...': 'Waiting for email...',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type ResetRouteCase = {
  component: NonNullable<typeof legacyResetRoute.options.component>
  path: '/reset' | '/user/reset'
}

function createResetRouteRouter(routeCase: ResetRouteCase, entry: string) {
  const rootRoute = createRootRoute({
    component: Outlet,
  })
  const authRoute = createRoute({
    getParentRoute: () => rootRoute,
    id: '/(auth)',
    component: Outlet,
  })
  const resetRoute = createRoute({
    getParentRoute: () => authRoute,
    path: routeCase.path.slice(1),
    component: routeCase.component,
  })

  return createRouter({
    routeTree: rootRoute.addChildren([authRoute.addChildren([resetRoute])]),
    history: createMemoryHistory({ initialEntries: [entry] }),
  })
}

after(() => {
  domWindow.close()
  for (const [key, descriptor] of originalGlobals) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

for (const routeCase of [
  {
    component: legacyResetRoute.options.component,
    path: '/reset',
  },
  {
    component: canonicalResetRoute.options.component,
    path: '/user/reset',
  },
] as const) {
  test(`${routeCase.path} ignores legacy email and token query credentials`, async () => {
    assert.ok(routeCase.component)
    const email = 'legacy-address@example.com'
    const token = 'legacy-query-reset-token'
    const router = createResetRouteRouter(
      {
        ...routeCase,
        component: routeCase.component,
      },
      `${routeCase.path}?email=${encodeURIComponent(email)}&token=${encodeURIComponent(token)}`
    )
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    try {
      await router.load()
      await act(async () => {
        root.render(
          <I18nextProvider i18n={i18n}>
            <RouterProvider router={router} />
          </I18nextProvider>
        )
        await Promise.resolve()
      })

      assert.equal(container.textContent?.includes(email), false)
      assert.equal(container.textContent?.includes(token), false)
      assert.equal(
        container.querySelector<HTMLInputElement>('input[name="token"]')?.value,
        ''
      )
    } finally {
      await act(async () => root.unmount())
      container.remove()
    }
  })
}
