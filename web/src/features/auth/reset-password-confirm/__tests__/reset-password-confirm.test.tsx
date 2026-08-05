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
import { after, afterEach, test } from 'node:test'

import { Window } from 'happy-dom'

import { api } from '@/lib/api'

const domWindow = new Window({ url: 'https://dashboard.example.com/reset' })
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

const copiedValues: string[] = []
Object.defineProperty(navigator, 'clipboard', {
  configurable: true,
  value: {
    writeText: async (value: string) => {
      copiedValues.push(value)
    },
  },
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
const { ResetPasswordConfirm } = await import('../index')

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
        'auth.resetPasswordConfirm.success':
          'Your password has been reset successfully',
        'Back to login': 'Back to login',
        'Confirm password': 'Confirm password',
        'Enter password (8-20 characters)': 'Enter password (8-20 characters)',
        Logo: 'Logo',
        'New password': 'New password',
        "Passwords don't match.": "Passwords don't match.",
        'Please confirm your password': 'Please confirm your password',
        'Please enter the verification code':
          'Please enter the verification code',
        'Please enter your password': 'Please enter your password',
        'Password must be at most 20 characters long':
          'Password must be at most 20 characters long',
        'Password must be between 8 and 20 characters':
          'Password must be between 8 and 20 characters',
        'Password must be at most 72 UTF-8 bytes':
          'Password must be at most 72 UTF-8 bytes',
        'Reset password': 'Reset password',
        'Verification code': 'Verification code',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const originalPost = api.post
type PostCall = {
  body: unknown
  config: unknown
  url: string
}
const postCalls: PostCall[] = []

type RenderedReset = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
  router: ReturnType<typeof createResetRouter>
}

function createResetRouter() {
  const rootRoute = createRootRoute({
    component: Outlet,
  })
  const resetRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: ResetPasswordConfirm,
  })
  const signInRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/sign-in',
    component: () => null,
  })

  return createRouter({
    routeTree: rootRoute.addChildren([resetRoute, signInRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
}

async function renderReset(): Promise<RenderedReset> {
  const router = createResetRouter()
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await router.load()
  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <RouterProvider router={router} />
      </I18nextProvider>
    )
    await Promise.resolve()
  })

  return { container, root, router }
}

async function unmountReset(rendered: RenderedReset) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

function findInput(
  container: HTMLElement,
  name: 'confirmPassword' | 'password' | 'token'
): HTMLInputElement {
  const input = container.querySelector<HTMLInputElement>(
    `input[name="${name}"]`
  )
  assert.ok(input)
  return input
}

function setInputValue(input: HTMLInputElement, value: string): void {
  const setter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  setter.call(input, value)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

async function fillResetForm(
  rendered: RenderedReset,
  values: { confirmPassword: string; password: string; token: string }
) {
  await act(async () => {
    setInputValue(findInput(rendered.container, 'token'), values.token)
    setInputValue(findInput(rendered.container, 'password'), values.password)
    setInputValue(
      findInput(rendered.container, 'confirmPassword'),
      values.confirmPassword
    )
  })
}

async function submitResetForm(rendered: RenderedReset) {
  const form = rendered.container.querySelector<HTMLFormElement>('form')
  assert.ok(form)
  await act(async () => {
    form.requestSubmit()
    await Promise.resolve()
    await Promise.resolve()
  })
}

afterEach(() => {
  api.post = originalPost
  postCalls.length = 0
  copiedValues.length = 0
  document.body.replaceChildren()
})

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

test('reset password schema requires a reset code and password confirmation', async () => {
  const constants = (await import('../../constants')) as unknown as {
    resetPasswordFormSchema?: {
      safeParse: (value: unknown) => { success: boolean }
    }
  }
  assert.ok(constants.resetPasswordFormSchema)

  assert.equal(
    constants.resetPasswordFormSchema.safeParse({
      token: '',
      password: '',
      confirmPassword: '',
    }).success,
    false
  )
})

test('reset password schema accepts exactly 8 and 20 characters but rejects 7 and 21', async () => {
  const constants = (await import('../../constants')) as unknown as {
    resetPasswordFormSchema?: {
      safeParse: (value: unknown) => { success: boolean }
    }
  }
  assert.ok(constants.resetPasswordFormSchema)
  const parse = (password: string) =>
    constants.resetPasswordFormSchema?.safeParse({
      token: 'a'.repeat(43),
      password,
      confirmPassword: password,
    }).success

  assert.equal(parse('p'.repeat(7)), false)
  assert.equal(parse('p'.repeat(8)), true)
  assert.equal(parse('p'.repeat(20)), true)
  assert.equal(parse('p'.repeat(21)), false)
})

test('reset password schema counts Unicode code points and enforces the UTF-8 byte limit', async () => {
  const constants = (await import('../../constants')) as unknown as {
    resetPasswordFormSchema?: {
      safeParse: (value: unknown) => { success: boolean }
    }
  }
  assert.ok(constants.resetPasswordFormSchema)
  const parse = (password: string) =>
    constants.resetPasswordFormSchema?.safeParse({
      token: 'a'.repeat(43),
      password,
      confirmPassword: password,
    }).success

  assert.equal(parse('😀'.repeat(4)), false)
  assert.equal(parse('😀'.repeat(8)), true)
  assert.equal(parse('😀'.repeat(11)), true)
  assert.equal(parse('😀'.repeat(18)), true)
  assert.equal(parse('😀'.repeat(19)), false)
})

test('reset password schema rejects a confirmation that differs from the chosen password', async () => {
  const constants = (await import('../../constants')) as unknown as {
    resetPasswordFormSchema?: {
      safeParse: (value: unknown) => { success: boolean }
    }
  }
  assert.ok(constants.resetPasswordFormSchema)

  assert.equal(
    constants.resetPasswordFormSchema.safeParse({
      token: 'a'.repeat(43),
      password: 'chosen-password',
      confirmPassword: 'other-password',
    }).success,
    false
  )
})

test('ResetPasswordConfirm renders code and new-password fields without email or generated-password output', async () => {
  const rendered = await renderReset()

  try {
    assert.ok(findInput(rendered.container, 'token'))
    assert.ok(findInput(rendered.container, 'password'))
    assert.ok(findInput(rendered.container, 'confirmPassword'))
    assert.equal(rendered.container.querySelector('input[type="email"]'), null)
    assert.equal(
      findInput(rendered.container, 'token').autocomplete,
      'one-time-code'
    )
    assert.equal(
      findInput(rendered.container, 'password').autocomplete,
      'new-password'
    )
    assert.equal(
      findInput(rendered.container, 'confirmPassword').autocomplete,
      'new-password'
    )
  } finally {
    await unmountReset(rendered)
  }
})

for (const invalid of [
  {
    name: 'a missing reset code',
    token: '',
    password: 'valid-password',
    confirmPassword: 'valid-password',
  },
  {
    name: 'a 7-character password',
    token: 'a'.repeat(43),
    password: 'p'.repeat(7),
    confirmPassword: 'p'.repeat(7),
  },
  {
    name: 'a 21-character password',
    token: 'a'.repeat(43),
    password: 'p'.repeat(21),
    confirmPassword: 'p'.repeat(21),
  },
  {
    name: 'a mismatched confirmation',
    token: 'a'.repeat(43),
    password: 'valid-password',
    confirmPassword: 'other-password',
  },
]) {
  test(`ResetPasswordConfirm with ${invalid.name} does not call the reset API`, async () => {
    api.post = (async (url: string, body?: unknown, config?: unknown) => {
      postCalls.push({ url, body, config })
      return { data: { success: true, message: '' } }
    }) as typeof api.post
    const rendered = await renderReset()

    try {
      await fillResetForm(rendered, invalid)
      await submitResetForm(rendered)

      assert.deepEqual(postCalls, [])
    } finally {
      await unmountReset(rendered)
    }
  })
}

test('ResetPasswordConfirm submits only token and password for a 43-character base64url code', async () => {
  api.post = (async (url: string, body?: unknown, config?: unknown) => {
    postCalls.push({ url, body, config })
    return { data: { success: true, message: '' } }
  }) as typeof api.post
  const rendered = await renderReset()
  const token = 'AbCdEf0123456789_-AbCdEf0123456789_-AbCdEf0'

  try {
    assert.equal(token.length, 43)
    await fillResetForm(rendered, {
      token,
      password: 'chosen-password',
      confirmPassword: 'chosen-password',
    })
    await submitResetForm(rendered)

    assert.deepEqual(postCalls, [
      {
        url: '/api/user/reset',
        body: { token, password: 'chosen-password' },
        config: undefined,
      },
    ])
  } finally {
    await unmountReset(rendered)
  }
})

test('ResetPasswordConfirm retains the entered code and password after a failed request', async () => {
  api.post = (async () => {
    throw new Error('temporary failure')
  }) as typeof api.post
  const rendered = await renderReset()
  const token = 'a'.repeat(43)

  try {
    await fillResetForm(rendered, {
      token,
      password: 'chosen-password',
      confirmPassword: 'chosen-password',
    })
    await submitResetForm(rendered)

    assert.equal(findInput(rendered.container, 'token').value, token)
    assert.equal(
      findInput(rendered.container, 'password').value,
      'chosen-password'
    )
    assert.equal(
      findInput(rendered.container, 'confirmPassword').value,
      'chosen-password'
    )
  } finally {
    await unmountReset(rendered)
  }
})

test('ResetPasswordConfirm success clears secrets without copying or rendering the submitted password', async () => {
  api.post = (async () => ({
    data: { success: true, message: 'Password reset successfully' },
  })) as typeof api.post
  const rendered = await renderReset()
  const token = 'a'.repeat(43)
  const password = 'chosen-password'

  try {
    await fillResetForm(rendered, {
      token,
      password,
      confirmPassword: password,
    })
    await submitResetForm(rendered)

    assert.equal(rendered.container.querySelector('input'), null)
    assert.equal(
      rendered.container.textContent?.includes(
        'Your password has been reset successfully'
      ),
      true
    )
    assert.equal(rendered.container.textContent?.includes(token), false)
    assert.equal(rendered.container.textContent?.includes(password), false)
    assert.deepEqual(copiedValues, [])
  } finally {
    await unmountReset(rendered)
  }
})

test('ResetPasswordConfirm success action navigates to sign-in', async () => {
  api.post = (async () => ({
    data: { success: true, message: 'Password reset successfully' },
  })) as typeof api.post
  const rendered = await renderReset()

  try {
    await fillResetForm(rendered, {
      token: 'a'.repeat(43),
      password: 'chosen-password',
      confirmPassword: 'chosen-password',
    })
    await submitResetForm(rendered)
    const button = [
      ...rendered.container.querySelectorAll<HTMLButtonElement>('button'),
    ].find((candidate) => candidate.textContent?.includes('Return to login'))
    assert.ok(button)

    await act(async () => {
      button.click()
      await Promise.resolve()
    })

    assert.equal(rendered.router.state.location.pathname, '/sign-in')
  } finally {
    await unmountReset(rendered)
  }
})
