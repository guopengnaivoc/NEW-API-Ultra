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

const domWindow = new Window({
  url: 'https://dashboard.example.com/channels',
})
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
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

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { SecureVerificationDialog } =
  await import('../secure-verification-dialog')

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

afterEach(() => {
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

test('zero allowed methods show unavailable state and keep Verify disabled', async () => {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let verifyCalls = 0

  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <SecureVerificationDialog
          open
          onOpenChange={() => undefined}
          methods={{
            hasPassword: true,
            has2FA: true,
            hasPasskey: true,
            passkeySupported: true,
          }}
          state={{
            method: null,
            allowedMethods: [],
            loading: false,
            code: '',
          }}
          onVerify={() => {
            verifyCalls += 1
          }}
          onCancel={() => undefined}
          onCodeChange={() => undefined}
          onMethodChange={() => undefined}
        />
      </I18nextProvider>
    )
    await Promise.resolve()
  })

  const dialog = document.body.querySelector<HTMLElement>('[role="dialog"]')
  assert.ok(dialog)
  assert.match(dialog.textContent ?? '', /Verification unavailable/)
  assert.match(
    dialog.textContent ?? '',
    /Add a password or enable Two-factor Authentication or Passkey before proceeding/
  )
  assert.equal(dialog.querySelectorAll('[role="tab"]').length, 0)
  assert.equal(dialog.querySelector('input'), null)
  const verifyButton = [
    ...dialog.querySelectorAll<HTMLButtonElement>('button'),
  ].find((button) => button.textContent?.trim() === 'Verify')
  assert.ok(verifyButton)
  assert.equal(verifyButton.disabled, true)

  await act(async () => {
    verifyButton.click()
  })
  assert.equal(verifyCalls, 0)

  await act(async () => root.unmount())
})
