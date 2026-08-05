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
// @ts-expect-error Bun provides this runtime module; the app tsconfig loads Node types only.
import { afterAll, beforeEach, test } from 'bun:test'
import assert from 'node:assert/strict'

import { Window } from 'happy-dom'

import type { ParameterEnabled, PlaygroundConfig } from '../../../types'

const domWindow = new Window()
const domGlobalKeys = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLButtonElement',
  'HTMLFormElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
  'Node',
  'Element',
  'Event',
  'MouseEvent',
  'CustomEvent',
  'File',
  'FileList',
  'Blob',
  'FormData',
  'FileReader',
  'MutationObserver',
  'ResizeObserver',
  'DOMRect',
  'Range',
  'requestAnimationFrame',
  'cancelAnimationFrame',
  'getComputedStyle',
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

const createObjectUrlDescriptor = Object.getOwnPropertyDescriptor(
  URL,
  'createObjectURL'
)
let createdObjectUrls: string[] = []
Object.defineProperty(URL, 'createObjectURL', {
  configurable: true,
  value: () => {
    const url = `blob:playground-${createdObjectUrls.length + 1}`
    createdObjectUrls.push(url)
    return url
  },
})

const { act } = await import('react')
const { createRoot } = await import('react-dom/client')
const i18next = (await import('i18next')).default
const { initReactI18next } = await import('react-i18next')
await i18next.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Ask anything': 'Ask anything',
        Attach: 'Attach',
        Parameters: 'Parameters',
        Search: 'Search',
        Send: 'Send',
      },
    },
  },
})
const { PlaygroundInput } = await import('../playground-input')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

const config: PlaygroundConfig = {
  frequency_penalty: 0,
  group: 'default',
  max_tokens: 1024,
  model: 'test-model',
  presence_penalty: 0,
  seed: null,
  stream: true,
  temperature: 1,
  top_p: 1,
}
const parameterEnabled: ParameterEnabled = {
  frequency_penalty: false,
  max_tokens: false,
  presence_penalty: false,
  seed: false,
  temperature: false,
  top_p: false,
}

type RenderedPlaygroundInput = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

async function renderPlaygroundInput(
  onSubmit: (text: string) => void
): Promise<RenderedPlaygroundInput> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      <PlaygroundInput
        config={config}
        groupValue='default'
        groups={[{ label: 'Default', ratio: 1, value: 'default' }]}
        modelValue='test-model'
        models={[{ label: 'Test model', value: 'test-model' }]}
        onConfigChange={() => undefined}
        onGroupChange={() => undefined}
        onModelChange={() => undefined}
        onParameterEnabledChange={() => undefined}
        onSubmit={onSubmit}
        parameterEnabled={parameterEnabled}
      />
    )
  })

  return { container, root }
}

function getMessageInput(
  rendered: RenderedPlaygroundInput
): HTMLTextAreaElement {
  const input = rendered.container.querySelector<HTMLTextAreaElement>(
    'textarea[name="message"]'
  )
  assert.ok(input)
  return input
}

async function unmountPlaygroundInput(rendered: RenderedPlaygroundInput) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

beforeEach(() => {
  createdObjectUrls = []
  document.body.replaceChildren()
})

afterAll(() => {
  domWindow.close()
  if (createObjectUrlDescriptor) {
    Object.defineProperty(URL, 'createObjectURL', createObjectUrlDescriptor)
  } else {
    Reflect.deleteProperty(URL, 'createObjectURL')
  }
  for (const [key, descriptor] of globalDescriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('Playground leaves file paste to the browser', async () => {
  const rendered = await renderPlaygroundInput(() => undefined)

  try {
    const pasteEvent = new Event('paste', {
      bubbles: true,
      cancelable: true,
    })
    Object.defineProperty(pasteEvent, 'clipboardData', {
      value: {
        items: [
          {
            getAsFile: () =>
              new File(['paste'], 'paste.txt', { type: 'text/plain' }),
            kind: 'file',
          },
        ],
      },
    })

    await act(async () => getMessageInput(rendered).dispatchEvent(pasteEvent))

    assert.equal(pasteEvent.defaultPrevented, false)
    assert.deepEqual(createdObjectUrls, [])
  } finally {
    await unmountPlaygroundInput(rendered)
  }
})

test('Playground still submits text after attachment capture is disabled', async () => {
  const submissions: string[] = []
  const rendered = await renderPlaygroundInput((text) => submissions.push(text))

  try {
    const input = getMessageInput(rendered)
    const valueSetter = Object.getOwnPropertyDescriptor(
      HTMLTextAreaElement.prototype,
      'value'
    )?.set
    assert.ok(valueSetter)

    await act(async () => {
      valueSetter.call(input, 'text-only message')
      input.dispatchEvent(new Event('input', { bubbles: true }))
      input.dispatchEvent(new Event('change', { bubbles: true }))
    })

    const form = rendered.container.querySelector('form')
    assert.ok(form)
    const submitEvent = new Event('submit', {
      bubbles: true,
      cancelable: true,
    })
    await act(async () => {
      form.dispatchEvent(submitEvent)
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.equal(submitEvent.defaultPrevented, true)
    assert.deepEqual(submissions, ['text-only message'])
    assert.equal(getMessageInput(rendered).value, '')
  } finally {
    await unmountPlaygroundInput(rendered)
  }
})
