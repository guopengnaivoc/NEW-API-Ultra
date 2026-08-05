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
import { after, describe, test } from 'node:test'

import { Window } from 'happy-dom'
import type React from 'react'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'SVGElement',
  'Node',
  'Element',
  'Event',
  'CustomEvent',
  'MutationObserver',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
] as const

for (const key of domGlobals) {
  Object.defineProperty(globalThis, key, {
    configurable: true,
    value: domWindow[key],
  })
}

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Open in new tab': 'Open in new tab',
      },
    },
  },
})

const { ChatPresetContent } = await import('../chat-preset-content')
const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type RenderedContent = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

async function renderContent(
  props: React.ComponentProps<typeof ChatPresetContent>
): Promise<RenderedContent> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      <I18nextProvider i18n={i18n}>
        <ChatPresetContent {...props} />
      </I18nextProvider>
    )
  })

  return { container, root }
}

async function unmountContent(rendered: RenderedContent) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

describe('chat preset external navigation', () => {
  after(() => {
    domWindow.close()
  })

  test('opens an address-only web preset outside the application without an iframe', async () => {
    const rendered = await renderContent({
      preset: {
        id: '0',
        name: 'Trusted chat',
        type: 'web',
        url: 'https://chat.example/?base={address}',
      },
      serverAddress: 'https://gateway.example',
    })

    assert.equal(rendered.container.querySelector('iframe'), null)

    const link = rendered.container.querySelector('a')
    assert.ok(link)
    assert.equal(
      link.href,
      'https://chat.example/?base=https%3A%2F%2Fgateway.example'
    )
    assert.equal(link.target, '_blank')
    assert.equal(link.rel, 'noopener noreferrer')
    assert.equal(link.textContent?.includes('Open in new tab'), true)

    await unmountContent(rendered)
  })
})
