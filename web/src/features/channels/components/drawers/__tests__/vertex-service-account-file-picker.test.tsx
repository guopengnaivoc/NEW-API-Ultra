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
import { useState } from 'react'

import { transformChannelToFormDefaults } from '../../../lib/channel-form'
import { channelSchema } from '../../../types'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
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
const { VertexServiceAccountFilePicker } =
  await import('../vertex-service-account-file-picker')

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

after(() => {
  domWindow.close()
})

function Harness(props: { batch: boolean }) {
  const [key, setKey] = useState('existing-key')
  return (
    <I18nextProvider i18n={i18n}>
      <VertexServiceAccountFilePicker
        batch={props.batch}
        onValueChange={setKey}
      />
      <output data-testid='key'>{key}</output>
    </I18nextProvider>
  )
}

describe('Vertex service account file picker', () => {
  test('does not replace the existing form value when selection fails', async () => {
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () => {
      root.render(<Harness batch={false} />)
    })

    const input = container.querySelector('input[type="file"]')
    assert.ok(input instanceof HTMLInputElement)
    const invalidFile = {
      name: 'invalid.json',
      size: 2,
      async text() {
        return '{}'
      },
    }
    Object.defineProperty(input, 'files', {
      configurable: true,
      value: [invalidFile],
    })

    await act(async () => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.equal(
      container.querySelector('[data-testid="key"]')?.textContent,
      'existing-key'
    )

    await act(async () => root.unmount())
    container.remove()
  })

  test('accepts multiple files for existing multi-key edit defaults', async () => {
    const channel = channelSchema.parse({
      id: 1,
      type: 41,
      key: '',
      status: 1,
      name: 'Vertex multi-key',
      created_time: 0,
      test_time: 0,
      response_time: 0,
      balance_updated_time: 0,
      models: 'gemini-2.5-pro',
      group: 'default',
      other: '{"default":"us-central1"}',
      settings: '{"vertex_key_type":"json"}',
      channel_info: {
        is_multi_key: true,
        multi_key_size: 2,
        multi_key_polling_index: 0,
        multi_key_mode: 'random',
      },
    })
    const defaults = transformChannelToFormDefaults(channel)
    const batch =
      defaults.multi_key_mode === 'batch' ||
      defaults.multi_key_mode === 'multi_to_single'
    const container = document.createElement('div')
    document.body.append(container)
    const root = createRoot(container)

    await act(async () => {
      root.render(<Harness batch={batch} />)
    })

    const input = container.querySelector('input[type="file"]')
    assert.ok(input instanceof HTMLInputElement)
    assert.equal(input.multiple, true)
    const credentials = ['first', 'second'].map((suffix) => ({
      type: 'service_account',
      project_id: `project-${suffix}`,
      private_key: `private-${suffix}`,
      client_email: `account-${suffix}@example.com`,
    }))
    const files = credentials.map((credential, index) => {
      const text = JSON.stringify(credential)
      return {
        name: `${index}.json`,
        size: new TextEncoder().encode(text).byteLength,
        async text() {
          return text
        },
      }
    })
    Object.defineProperty(input, 'files', {
      configurable: true,
      value: files,
    })

    await act(async () => {
      input.dispatchEvent(new Event('change', { bubbles: true }))
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.deepEqual(
      JSON.parse(
        container.querySelector('[data-testid="key"]')?.textContent || ''
      ),
      credentials
    )

    await act(async () => root.unmount())
    container.remove()
  })
})
