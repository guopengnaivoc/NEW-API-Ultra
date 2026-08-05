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

import { api, type ApiRequestConfig } from '@/lib/api'

const domWindow = new Window({
  url: 'https://dashboard.example.com/channels',
})
const testDocument = domWindow.document as unknown as Document
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'localStorage',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLFormElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'FocusEvent',
  'KeyboardEvent',
  'MouseEvent',
  'PointerEvent',
  'customElements',
  'MutationObserver',
  'ResizeObserver',
  'matchMedia',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const
type DomGlobalDescriptors = Map<
  (typeof domGlobalKeys)[number],
  PropertyDescriptor | undefined
>

function captureDomGlobals(): DomGlobalDescriptors {
  return new Map(
    domGlobalKeys.map((key) => [
      key,
      Object.getOwnPropertyDescriptor(globalThis, key),
    ])
  )
}

function installDomGlobals(): void {
  for (const key of domGlobalKeys) {
    Object.defineProperty(globalThis, key, {
      configurable: true,
      value: domWindow[key],
    })
  }
}

function restoreDomGlobals(descriptors: DomGlobalDescriptors): void {
  for (const [key, descriptor] of descriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
}

const importTimeGlobals = captureDomGlobals()
installDomGlobals()
Object.defineProperty(HTMLElement.prototype, 'scrollIntoView', {
  configurable: true,
  value: () => undefined,
})
Object.defineProperty(domWindow, 'PublicKeyCredential', {
  configurable: true,
  value: class {
    static async isConditionalMediationAvailable() {
      return true
    }
  },
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { QueryClient, QueryClientProvider } =
  await import('@tanstack/react-query')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { ROLE } = await import('@/lib/roles')
const { useAuthStore } = await import('@/stores/auth-store')
const { channelsQueryKeys } = await import('../../../lib')
const { channelSchema } = await import('../../../types')
const { ChannelsProvider } = await import('../../channels-provider')
const { ChannelMutateDrawer } = await import('../channel-mutate-drawer')
restoreDomGlobals(importTimeGlobals)

const i18n = createInstance()
await i18n.use(initReactI18next).init({
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
const originalUser = useAuthStore.getState().auth.user
let activeTestGlobals: DomGlobalDescriptors | null = null

type PostCall = {
  body: unknown
  config: ApiRequestConfig | undefined
  url: string
}

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

function findButton(label: string): HTMLButtonElement | undefined {
  return [
    ...testDocument.body.querySelectorAll<HTMLButtonElement>('button'),
  ].find((button) => button.textContent?.trim() === label)
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

beforeEach(() => {
  activeTestGlobals = captureDomGlobals()
  installDomGlobals()
  testDocument.body.replaceChildren()
})

afterEach(() => {
  api.get = originalGet
  api.post = originalPost
  useAuthStore.getState().auth.setUser(originalUser)
  testDocument.body.replaceChildren()
  assert.ok(activeTestGlobals)
  restoreDomGlobals(activeTestGlobals)
  activeTestGlobals = null
})

after(() => {
  domWindow.close()
})

test('channel reveal offers only Passkey and 2FA and binds proof to the selected endpoint', async () => {
  const channel = channelSchema.parse({
    id: 17,
    type: 1,
    key: '',
    status: 1,
    name: 'channel-17',
    created_time: 1,
    test_time: 1,
    response_time: 1,
    balance_updated_time: 1,
    models: 'gpt-test',
  })
  const postCalls: PostCall[] = []
  const unexpectedGets: string[] = []
  let verificationMethodReads = 0
  let resolveVerificationMethodsReady: () => void
  const verificationMethodsReady = new Promise<void>((resolve) => {
    resolveVerificationMethodsReady = resolve
  })
  let resolveProofRetryReady: () => void
  const proofRetryReady = new Promise<void>((resolve) => {
    resolveProofRetryReady = resolve
  })

  api.get = (async (url: string) => {
    if (url === '/api/user/2fa/status') {
      verificationMethodReads += 1
      if (verificationMethodReads === 4) resolveVerificationMethodsReady()
      return {
        data: {
          success: true,
          data: { enabled: true, has_password: true },
        },
      }
    }
    if (url === '/api/user/passkey') {
      verificationMethodReads += 1
      if (verificationMethodReads === 4) resolveVerificationMethodsReady()
      return {
        data: {
          success: true,
          data: { enabled: true },
        },
      }
    }
    unexpectedGets.push(url)
    throw new Error(`Unexpected GET ${url}`)
  }) as typeof api.get
  api.post = (async (
    url: string,
    body?: unknown,
    config?: ApiRequestConfig
  ) => {
    postCalls.push({ url, body, config })
    if (url === '/api/verify') {
      return {
        data: {
          success: true,
          data: {
            proof_token: 'proof-17',
            expires_at: 4102444800,
            method: '2fa',
            scope: 'channel.key.read',
            target: '17',
          },
        },
      }
    }
    if (url === '/api/channel/17/key') {
      const headers = config?.headers as Record<string, string> | undefined
      if (!headers?.['X-Security-Proof']) {
        throw verificationRequiredError()
      }
      resolveProofRetryReady()
      return {
        data: {
          success: true,
          data: { key: 'synthetic-channel-key' },
        },
      }
    }
    throw new Error(`Unexpected POST ${url}`)
  }) as typeof api.post

  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'root-fixture',
    role: ROLE.SUPER_ADMIN,
  })
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: { retry: false, staleTime: Number.POSITIVE_INFINITY },
      mutations: { retry: false },
    },
  })
  queryClient.setQueryData(channelsQueryKeys.detail(17), {
    success: true,
    data: channel,
  })
  queryClient.setQueryData(['groups'], {
    success: true,
    data: ['default'],
  })
  queryClient.setQueryData(['channel_models'], {
    success: true,
    data: [{ id: 'gpt-test' }],
  })
  queryClient.setQueryData(['prefill_groups', 'model'], {
    success: true,
    data: [],
  })

  const container = testDocument.createElement('div')
  testDocument.body.append(container)
  const root = createRoot(container)

  try {
    await act(async () => {
      root.render(
        <QueryClientProvider client={queryClient}>
          <I18nextProvider i18n={i18n}>
            <ChannelsProvider>
              <ChannelMutateDrawer
                open
                onOpenChange={() => undefined}
                currentRow={channel}
              />
            </ChannelsProvider>
          </I18nextProvider>
        </QueryClientProvider>
      )
    })
    const reveal = findButton('Reveal key')
    assert.ok(reveal)

    await act(async () => {
      reveal.click()
      await verificationMethodsReady
    })
    const verificationDialog = [
      ...testDocument.body.querySelectorAll<HTMLElement>('[role="dialog"]'),
    ].find((dialog) =>
      dialog.textContent?.includes('Verify to view channel key')
    )
    assert.ok(verificationDialog)
    const tabs = [
      ...verificationDialog.querySelectorAll<HTMLElement>('[role="tab"]'),
    ]
    assert.deepEqual(
      tabs.map((tab) => tab.textContent?.trim()),
      ['Authenticator code', 'Passkey']
    )
    assert.equal(
      tabs.some((tab) => tab.textContent?.trim() === 'Password'),
      false
    )

    const twoFATab = tabs.find(
      (tab) => tab.textContent?.trim() === 'Authenticator code'
    )
    assert.ok(twoFATab)
    await act(async () => {
      twoFATab.click()
    })
    const codeInput = verificationDialog.querySelector<HTMLInputElement>(
      'input[placeholder="Enter verification code"]'
    )
    assert.ok(codeInput)
    await act(async () => {
      setInputValue(codeInput, '123456')
    })

    const verify = findButton('Verify')
    assert.ok(verify)
    assert.equal(verify.disabled, false)
    await act(async () => {
      verify.click()
      await proofRetryReady
    })

    assert.ok(verificationMethodReads >= 4)
    assert.deepEqual(unexpectedGets, [])
    assert.deepEqual(
      postCalls.map((call) => ({
        url: call.url,
        body: call.body,
        proof:
          (call.config?.headers as Record<string, string> | undefined)?.[
            'X-Security-Proof'
          ] ?? null,
      })),
      [
        {
          url: '/api/channel/17/key',
          body: undefined,
          proof: null,
        },
        {
          url: '/api/verify',
          body: {
            method: '2fa',
            code: '123456',
            scope: 'channel.key.read',
            target: '17',
          },
          proof: null,
        },
        {
          url: '/api/channel/17/key',
          body: undefined,
          proof: 'proof-17',
        },
      ]
    )
    const revealed = testDocument.body.querySelector<HTMLInputElement>(
      'input[placeholder="Hidden — verify to reveal"]'
    )
    assert.ok(revealed)
    assert.equal(revealed.value, 'synthetic-channel-key')
  } finally {
    await act(async () => root.unmount())
    container.remove()
    queryClient.clear()
  }
})
