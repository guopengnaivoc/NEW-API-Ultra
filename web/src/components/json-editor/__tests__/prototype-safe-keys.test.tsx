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
import type { ReactNode } from 'react'

const domWindow = new Window()
const domGlobals = [
  'window',
  'document',
  'navigator',
  'HTMLElement',
  'HTMLInputElement',
  'HTMLTextAreaElement',
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

const { act, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const { createInstance } = await import('i18next')
const { I18nextProvider, initReactI18next } = await import('react-i18next')
const { JsonEditor } = await import('../../json-editor')

const i18n = createInstance()
await i18n.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Add Row': 'Add Row',
        Copy: 'Copy',
        'Failed to copy': 'Failed to copy',
        'Fill Template': 'Fill Template',
        'Format JSON': 'Format JSON',
        'Invalid JSON': 'Invalid JSON',
        'Invalid JSON format': 'Invalid JSON format',
        JSON: 'JSON',
        'JSON Mode': 'JSON Mode',
        Key: 'Key',
        'No mappings configured. Click "Add Row" to get started.':
          'No mappings configured. Click "Add Row" to get started.',
        Value: 'Value',
        'Visual Mode': 'Visual Mode',
      },
    },
  },
})

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

type RenderedEditor = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

type HarnessProps = {
  changes: string[]
  initialValue: string
}

function ControlledHarness(props: HarnessProps) {
  const [value, setValue] = useState(props.initialValue)

  return (
    <JsonEditor
      value={value}
      onChange={(nextValue) => {
        props.changes.push(nextValue)
        setValue(nextValue)
      }}
    />
  )
}

function withI18n(node: ReactNode) {
  return <I18nextProvider i18n={i18n}>{node}</I18nextProvider>
}

async function renderEditor(node: ReactNode): Promise<RenderedEditor> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(withI18n(node))
  })

  return { container, root }
}

async function unmountEditor(rendered: RenderedEditor) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

function findButton(container: HTMLElement, label: string): HTMLButtonElement {
  const button = [
    ...container.querySelectorAll<HTMLButtonElement>('button'),
  ].find((candidate) => candidate.textContent?.trim().includes(label))
  assert.ok(button)
  return button
}

function getJsonTextArea(container: HTMLElement): HTMLTextAreaElement {
  const textarea = container.querySelector<HTMLTextAreaElement>(
    '.json-code-editor-textarea'
  )
  assert.ok(textarea)
  return textarea
}

function getVisualInputs(container: HTMLElement): HTMLInputElement[] {
  return [...container.querySelectorAll<HTMLInputElement>('input')]
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

// Enter a single key/value pair through the visual editor and return the JSON
// the editor emitted to its onChange callback.
async function serializeSingleRow(
  key: string,
  value: string
): Promise<{ emitted: string; changes: string[] }> {
  const changes: string[] = []
  const rendered = await renderEditor(
    <ControlledHarness initialValue='' changes={changes} />
  )

  try {
    await act(async () => {
      findButton(rendered.container, 'Add Row').click()
    })

    const inputs = getVisualInputs(rendered.container)
    assert.equal(inputs.length, 2)

    await act(async () => {
      setInputValue(inputs[0], key)
    })
    await act(async () => {
      setInputValue(getVisualInputs(rendered.container)[1], value)
    })

    const emitted = changes.at(-1) ?? ''
    return { emitted, changes }
  } finally {
    await unmountEditor(rendered)
  }
}

describe('JsonEditor prototype-safe key serialization', () => {
  after(() => {
    domWindow.close()
  })

  // The core regression: a "__proto__" key entered in the visual editor must
  // survive as an own data property in the serialized JSON. A plain {} target
  // routes obj["__proto__"] = v through Object.prototype's __proto__ setter,
  // dropping the pair entirely.
  test('retains a __proto__ key as an own data property', async () => {
    const { emitted } = await serializeSingleRow('__proto__', 'danger')

    const parsed = JSON.parse(emitted) as Record<string, unknown>
    assert.equal(
      Object.prototype.hasOwnProperty.call(parsed, '__proto__'),
      true
    )
    assert.equal(
      Object.getOwnPropertyDescriptor(parsed, '__proto__')?.value,
      'danger'
    )
    assert.match(emitted, /"__proto__"/)
  })

  test('retains constructor and prototype keys', async () => {
    for (const key of ['constructor', 'prototype']) {
      const { emitted } = await serializeSingleRow(key, 'v')
      const parsed = JSON.parse(emitted) as Record<string, unknown>
      assert.equal(Object.prototype.hasOwnProperty.call(parsed, key), true)
      assert.equal(parsed[key], 'v')
    }
  })

  test('serializes ordinary keys unchanged alongside special keys', async () => {
    const changes: string[] = []
    const rendered = await renderEditor(
      <ControlledHarness initialValue='' changes={changes} />
    )

    try {
      await act(async () => {
        findButton(rendered.container, 'Add Row').click()
      })
      await act(async () => {
        findButton(rendered.container, 'Add Row').click()
      })

      const inputs = getVisualInputs(rendered.container)
      assert.equal(inputs.length, 4)

      await act(async () => {
        setInputValue(inputs[0], 'normal')
      })
      await act(async () => {
        setInputValue(getVisualInputs(rendered.container)[1], 'value')
      })
      await act(async () => {
        setInputValue(getVisualInputs(rendered.container)[2], '__proto__')
      })
      await act(async () => {
        setInputValue(getVisualInputs(rendered.container)[3], 'polluted')
      })

      const parsed = JSON.parse(changes.at(-1) ?? '') as Record<string, unknown>
      assert.equal(parsed.normal, 'value')
      assert.equal(
        Object.getOwnPropertyDescriptor(parsed, '__proto__')?.value,
        'polluted'
      )
      assert.deepEqual(Object.keys(parsed).sort(), ['__proto__', 'normal'])
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('round-trips a __proto__ mapping from JSON mode into visual mode', async () => {
    const changes: string[] = []
    const rendered = await renderEditor(
      <ControlledHarness initialValue='' changes={changes} />
    )

    try {
      await act(async () => {
        findButton(rendered.container, 'JSON Mode').click()
      })

      const raw = '{"__proto__":"danger","keep":"safe"}'
      const textarea = getJsonTextArea(rendered.container)
      await act(async () => {
        textarea.value = raw
        textarea.dispatchEvent(new Event('input', { bubbles: true }))
      })

      await act(async () => {
        findButton(rendered.container, 'Visual Mode').click()
      })

      // Both rows must be represented; the __proto__ pair must not vanish.
      const inputs = getVisualInputs(rendered.container)
      const keys = inputs
        .filter((_, index) => index % 2 === 0)
        .map((input) => input.value)
      assert.deepEqual(keys.sort(), ['__proto__', 'keep'])

      await act(async () => {
        findButton(rendered.container, 'JSON Mode').click()
      })

      const parsed = JSON.parse(
        getJsonTextArea(rendered.container).value
      ) as Record<string, unknown>
      assert.equal(
        Object.getOwnPropertyDescriptor(parsed, '__proto__')?.value,
        'danger'
      )
      assert.equal(parsed.keep, 'safe')
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('does not pollute Object.prototype when serializing a __proto__ key', async () => {
    const before = Object.getOwnPropertyDescriptor(
      Object.prototype,
      '__proto__'
    )
    const canary = {}

    await serializeSingleRow('__proto__', '{"polluted":true}')

    // A leaked prototype write would surface on an unrelated plain object.
    assert.equal((canary as Record<string, unknown>).polluted, undefined)
    assert.deepEqual(
      Object.getOwnPropertyDescriptor(Object.prototype, '__proto__'),
      before
    )
  })
})
