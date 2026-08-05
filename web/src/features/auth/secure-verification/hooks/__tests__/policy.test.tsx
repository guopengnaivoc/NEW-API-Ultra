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
import { after, afterEach, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'

import { api } from '@/lib/api'

const domWindow = new Window({
  url: 'https://dashboard.example.com/channels',
})
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

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const i18next = (await import('i18next')).default
const { useSecureVerification } = await import('../use-secure-verification')

await i18next.init({
  lng: 'en',
  resources: {
    en: {
      translation: {},
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const originalGet = api.get
const originalPost = api.post
const postCalls: Array<{ body: unknown; url: string }> = []
let pendingAction: Promise<void> | null = null
let passwordAvailable = true
let twoFAAvailable = false

function verificationRequiredError(): Error {
  return Object.assign(new Error('Secure verification is required'), {
    response: {
      status: 403,
      data: {
        code: 'SECURITY_PROOF_REQUIRED',
        message: 'Secure verification is required',
      },
    },
  })
}

function HookHarness(props: {
  protectedCall: (proofToken?: string) => Promise<unknown>
}) {
  const verification = useSecureVerification({ autoReset: false })
  const [outcome, setOutcome] = useState('')

  const track = (action: Promise<unknown>) => {
    pendingAction = action.then(
      () => {
        setOutcome('resolved')
      },
      (error: unknown) => {
        setOutcome(error instanceof Error ? error.message : String(error))
      }
    )
  }

  return (
    <>
      <button
        type='button'
        data-action='with-channel-verification'
        onClick={() => {
          track(
            verification.withVerification(props.protectedCall, {
              scope: 'channel.key.read',
              target: '17',
              allowedMethods: ['passkey', '2fa'],
              preferredMethod: 'passkey',
              description: 'Channel verification unavailable',
            })
          )
        }}
      >
        Reveal channel key
      </button>
      <button
        type='button'
        data-action='start-2fa'
        onClick={() => {
          track(
            verification.startVerification(props.protectedCall, {
              scope: 'channel.key.read',
              target: '17',
              allowedMethods: ['2fa'],
              preferredMethod: '2fa',
            })
          )
        }}
      >
        Start 2FA
      </button>
      <button
        type='button'
        data-action='force-password'
        onClick={() => {
          verification.switchMethod('password')
          verification.setCode('password-secret')
        }}
      >
        Switch to password
      </button>
      <button
        type='button'
        data-action='execute'
        onClick={() => {
          track(verification.executeVerification())
        }}
      >
        Execute verification
      </button>
      <output data-state='open'>{String(verification.open)}</output>
      <output data-state='method'>{verification.currentMethod ?? ''}</output>
      <output data-state='outcome'>{outcome}</output>
    </>
  )
}

function findAction(container: HTMLElement, action: string): HTMLButtonElement {
  const button = container.querySelector<HTMLButtonElement>(
    `[data-action="${action}"]`
  )
  assert.ok(button)
  return button
}

async function clickAndWait(button: HTMLButtonElement): Promise<void> {
  pendingAction = null
  await act(async () => {
    button.click()
    assert.ok(pendingAction)
    await pendingAction
  })
}

async function renderHarness(
  protectedCall: (proofToken?: string) => Promise<unknown>
) {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  await act(async () => {
    root.render(<HookHarness protectedCall={protectedCall} />)
    await Promise.resolve()
  })
  return { container, root }
}

beforeEach(() => {
  postCalls.length = 0
  pendingAction = null
  passwordAvailable = true
  twoFAAvailable = false
  api.get = (async (url: string) => {
    if (url === '/api/user/2fa/status') {
      return {
        data: {
          success: true,
          data: {
            enabled: twoFAAvailable,
            has_password: passwordAvailable,
          },
        },
      }
    }
    if (url === '/api/user/passkey') {
      return {
        data: {
          success: true,
          data: { enabled: false },
        },
      }
    }
    throw new Error(`Unexpected GET ${url}`)
  }) as typeof api.get
  api.post = (async (url: string, body?: unknown) => {
    postCalls.push({ url, body })
    if (url === '/api/verify') {
      return {
        data: {
          success: true,
          data: {
            proof_token: 'proof-password',
            expires_at: 4102444800,
            method: 'password',
            scope: 'channel.key.read',
            target: '17',
          },
        },
      }
    }
    throw new Error(`Unexpected POST ${url}`)
  }) as typeof api.post
})

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
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

test('password-only channel reveal fails closed without opening or executing verification', async () => {
  const protectedCalls: Array<string | undefined> = []
  const rendered = await renderHarness(async (proofToken?: string) => {
    protectedCalls.push(proofToken)
    throw verificationRequiredError()
  })

  await clickAndWait(
    findAction(rendered.container, 'with-channel-verification')
  )

  assert.equal(
    rendered.container.querySelector('[data-state="open"]')?.textContent,
    'false'
  )
  assert.equal(
    rendered.container.querySelector('[data-state="method"]')?.textContent,
    ''
  )
  assert.deepEqual(protectedCalls, [undefined])
  assert.deepEqual(postCalls, [])

  await act(async () => rendered.root.unmount())
})

test('execution revalidates the configured method policy before requesting proof', async () => {
  twoFAAvailable = true
  const protectedCalls: Array<string | undefined> = []
  const rendered = await renderHarness(async (proofToken?: string) => {
    protectedCalls.push(proofToken)
    return { success: true }
  })

  await clickAndWait(findAction(rendered.container, 'start-2fa'))
  assert.equal(
    rendered.container.querySelector('[data-state="open"]')?.textContent,
    'true'
  )
  assert.equal(
    rendered.container.querySelector('[data-state="method"]')?.textContent,
    '2fa'
  )

  await act(async () => {
    findAction(rendered.container, 'force-password').click()
  })
  assert.equal(
    rendered.container.querySelector('[data-state="method"]')?.textContent,
    'password'
  )

  await clickAndWait(findAction(rendered.container, 'execute'))

  assert.equal(
    rendered.container.querySelector('[data-state="outcome"]')?.textContent,
    'Unsupported verification method: password'
  )
  assert.deepEqual(postCalls, [])
  assert.deepEqual(protectedCalls, [])

  await act(async () => rendered.root.unmount())
})
