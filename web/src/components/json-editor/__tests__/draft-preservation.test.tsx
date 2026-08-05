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

async function rerenderEditor(rendered: RenderedEditor, node: ReactNode) {
  await act(async () => {
    rendered.root.render(withI18n(node))
  })
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

function setTextAreaValue(textarea: HTMLTextAreaElement, value: string): void {
  textarea.value = value
  textarea.dispatchEvent(new Event('input', { bubbles: true }))
}

function setInputValue(
  input: HTMLInputElement,
  value: string,
  caretPosition = value.length
): void {
  const setter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(setter)
  setter.call(input, value)
  input.setSelectionRange(caretPosition, caretPosition)
  input.dispatchEvent(new Event('input', { bubbles: true }))
}

describe('JsonEditor draft preservation', () => {
  after(() => {
    domWindow.close()
  })

  test('rejects visual mode without replacing an invalid JSON draft', async () => {
    const changes: string[] = []
    const rendered = await renderEditor(
      <ControlledHarness initialValue='{"source":"target"}' changes={changes} />
    )

    try {
      await act(async () => {
        findButton(rendered.container, 'JSON Mode').click()
      })

      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), '{"source":')
      })
      const callsBeforeRejectedSwitch = changes.length

      await act(async () => {
        findButton(rendered.container, 'Visual Mode').click()
      })

      const textarea = getJsonTextArea(rendered.container)
      assert.equal(textarea.value, '{"source":')
      assert.equal(textarea.getAttribute('aria-invalid'), 'true')
      assert.match(rendered.container.textContent ?? '', /Invalid JSON format/)
      assert.equal(changes.length, callsBeforeRejectedSwitch)
      assert.equal(changes.at(-1), '{"source":')
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('allows a repaired draft into visual mode and serializes later row edits', async () => {
    const changes: string[] = []
    const rendered = await renderEditor(
      <ControlledHarness initialValue='{"source":"target"}' changes={changes} />
    )

    try {
      await act(async () => {
        findButton(rendered.container, 'JSON Mode').click()
      })
      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), '{"source":')
      })
      await act(async () => {
        findButton(rendered.container, 'Visual Mode').click()
      })

      await act(async () => {
        setTextAreaValue(
          getJsonTextArea(rendered.container),
          '{"source":"repaired"}'
        )
      })
      await act(async () => {
        findButton(rendered.container, 'Visual Mode').click()
      })

      const inputs = [
        ...rendered.container.querySelectorAll<HTMLInputElement>('input'),
      ]
      assert.equal(inputs.length, 2)
      assert.equal(inputs[0]?.value, 'source')
      assert.equal(inputs[1]?.value, 'repaired')

      await act(async () => {
        setInputValue(inputs[1], 'final')
      })
      await act(async () => {
        findButton(rendered.container, 'JSON Mode').click()
      })

      assert.deepEqual(JSON.parse(getJsonTextArea(rendered.container).value), {
        source: 'final',
      })
      assert.deepEqual(JSON.parse(changes.at(-1) ?? ''), {
        source: 'final',
      })
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('shows an external invalid controlled value in JSON mode', async () => {
    const changes: string[] = []
    const handleChange = (value: string) => changes.push(value)
    const rendered = await renderEditor(
      <JsonEditor value='{"source":"target"}' onChange={handleChange} />
    )

    try {
      await rerenderEditor(
        rendered,
        <JsonEditor value='{"broken":' onChange={handleChange} />
      )

      assert.equal(getJsonTextArea(rendered.container).value, '{"broken":')
      assert.match(rendered.container.textContent ?? '', /Invalid JSON format/)
      assert.deepEqual(changes, [])
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('applies controlled values even when they match historical local edits', async () => {
    const changes: string[] = []
    const handleChange = (value: string) => changes.push(value)
    const firstDraft = '{"draft":"first"'
    const secondDraft = '{"draft":"second"'
    const externalValue = '{"external":"replacement"'
    const rendered = await renderEditor(
      <JsonEditor value='{"initial":' onChange={handleChange} />
    )

    try {
      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), firstDraft)
      })
      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), secondDraft)
      })

      await rerenderEditor(
        rendered,
        <JsonEditor value={externalValue} onChange={handleChange} />
      )
      assert.equal(getJsonTextArea(rendered.container).value, externalValue)

      await rerenderEditor(
        rendered,
        <JsonEditor value={firstDraft} onChange={handleChange} />
      )
      assert.equal(getJsonTextArea(rendered.container).value, firstDraft)
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('applies a controlled value that equals the initial prop after local edits', async () => {
    const changes: string[] = []
    const handleChange = (value: string) => changes.push(value)
    const initialValue = '{"draft":"initial"'
    const intermediateDraft = '{"draft":"intermediate"'
    const newestDraft = '{"draft":"newest"'
    const rendered = await renderEditor(
      <JsonEditor value={initialValue} onChange={handleChange} />
    )

    try {
      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), intermediateDraft)
      })
      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), initialValue)
      })
      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), newestDraft)
      })

      await rerenderEditor(
        rendered,
        <JsonEditor value={intermediateDraft} onChange={handleChange} />
      )
      assert.equal(getJsonTextArea(rendered.container).value, intermediateDraft)

      await rerenderEditor(
        rendered,
        <JsonEditor value={initialValue} onChange={handleChange} />
      )
      assert.equal(getJsonTextArea(rendered.container).value, initialValue)
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('keeps a local draft while the parent has not echoed the change', async () => {
    const changes: string[] = []
    const rendered = await renderEditor(
      <JsonEditor
        value='{"source":"target"}'
        onChange={(value) => changes.push(value)}
      />
    )

    try {
      await act(async () => {
        findButton(rendered.container, 'JSON Mode').click()
      })
      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), '{"source":')
        await Promise.resolve()
      })

      assert.equal(getJsonTextArea(rendered.container).value, '{"source":')
      assert.equal(changes.at(-1), '{"source":')
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('preserves the focused visual input and caret across synchronous parent echoes', async () => {
    const changes: string[] = []
    const rendered = await renderEditor(
      <ControlledHarness initialValue='{"source":"target"}' changes={changes} />
    )

    try {
      const originalValueInput =
        rendered.container.querySelectorAll<HTMLInputElement>('input')[1]
      assert.ok(originalValueInput)

      await act(async () => {
        originalValueInput.focus()
        setInputValue(originalValueInput, 'target-one', 6)
      })

      const valueInputAfterFirstEcho =
        rendered.container.querySelectorAll<HTMLInputElement>('input')[1]
      assert.equal(valueInputAfterFirstEcho === originalValueInput, true)
      assert.equal(document.activeElement === originalValueInput, true)
      assert.equal(valueInputAfterFirstEcho.selectionStart, 6)
      assert.equal(valueInputAfterFirstEcho.selectionEnd, 6)

      await act(async () => {
        setInputValue(valueInputAfterFirstEcho, 'target-two', 4)
      })

      const valueInputAfterSecondEcho =
        rendered.container.querySelectorAll<HTMLInputElement>('input')[1]
      assert.equal(valueInputAfterSecondEcho === originalValueInput, true)
      assert.equal(document.activeElement === originalValueInput, true)
      assert.equal(valueInputAfterSecondEcho.selectionStart, 4)
      assert.equal(valueInputAfterSecondEcho.selectionEnd, 4)
      assert.deepEqual(JSON.parse(changes.at(-1) ?? ''), {
        source: 'target-two',
      })
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('treats every changed controlled value as authoritative', async () => {
    const changes: string[] = []
    const handleChange = (value: string) => changes.push(value)
    const initialValue = '{"source":"target"}'
    const rendered = await renderEditor(
      <JsonEditor value={initialValue} onChange={handleChange} />
    )

    try {
      await act(async () => {
        findButton(rendered.container, 'JSON Mode').click()
      })
      changes.length = 0

      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), '{"first":')
      })
      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), '{"second":')
      })

      assert.deepEqual(changes, ['{"first":', '{"second":'])

      await rerenderEditor(
        rendered,
        <JsonEditor value={changes[0] ?? ''} onChange={handleChange} />
      )

      assert.equal(getJsonTextArea(rendered.container).value, '{"first":')

      const externalValue = '{"external":"replacement"}'
      const callsBeforeExternalValue = changes.length
      await rerenderEditor(
        rendered,
        <JsonEditor value={externalValue} onChange={handleChange} />
      )

      assert.equal(getJsonTextArea(rendered.container).value, externalValue)
      assert.equal(changes.length, callsBeforeExternalValue)

      await act(async () => {
        findButton(rendered.container, 'Visual Mode').click()
      })

      const inputs = [
        ...rendered.container.querySelectorAll<HTMLInputElement>('input'),
      ]
      assert.equal(inputs.length, 2)
      assert.equal(inputs[0]?.value, 'external')
      assert.equal(inputs[1]?.value, 'replacement')
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('applies changed controlled state after same-value parent echoes', async () => {
    const changes: string[] = []
    const handleChange = (value: string) => changes.push(value)
    const initialValue = '{"source":"target"}'
    const rendered = await renderEditor(
      <JsonEditor value={initialValue} onChange={handleChange} />
    )

    try {
      await act(async () => {
        findButton(rendered.container, 'JSON Mode').click()
      })
      const normalizedValue = changes.at(-1)
      assert.ok(normalizedValue)

      await act(async () => {
        findButton(rendered.container, 'Visual Mode').click()
      })
      await act(async () => {
        findButton(rendered.container, 'JSON Mode').click()
      })

      assert.deepEqual(changes, [normalizedValue, normalizedValue])

      await rerenderEditor(
        rendered,
        <JsonEditor value={normalizedValue} onChange={handleChange} />
      )

      const externalValue = '{"external":"B"}'
      await rerenderEditor(
        rendered,
        <JsonEditor value={externalValue} onChange={handleChange} />
      )
      assert.equal(getJsonTextArea(rendered.container).value, externalValue)

      await rerenderEditor(
        rendered,
        <JsonEditor value={normalizedValue} onChange={handleChange} />
      )

      assert.equal(getJsonTextArea(rendered.container).value, normalizedValue)
    } finally {
      await unmountEditor(rendered)
    }
  })

  test('rejects valid JSON values that the visual object editor cannot represent', async () => {
    for (const draft of ['[]', 'null', '"text"', '42']) {
      const changes: string[] = []
      const rendered = await renderEditor(
        <ControlledHarness
          initialValue='{"source":"target"}'
          changes={changes}
        />
      )

      try {
        await act(async () => {
          findButton(rendered.container, 'JSON Mode').click()
        })
        await act(async () => {
          setTextAreaValue(getJsonTextArea(rendered.container), draft)
        })
        const callsBeforeRejectedSwitch = changes.length

        await act(async () => {
          findButton(rendered.container, 'Visual Mode').click()
        })

        const textarea = getJsonTextArea(rendered.container)
        assert.equal(textarea.value, draft)
        assert.equal(textarea.getAttribute('aria-invalid'), 'true')
        assert.match(
          rendered.container.textContent ?? '',
          /Invalid JSON format/
        )
        assert.equal(changes.length, callsBeforeRejectedSwitch)
        assert.equal(changes.at(-1), draft)
      } finally {
        await unmountEditor(rendered)
      }
    }
  })

  test('allows an explicit empty JSON draft to enter visual mode', async () => {
    const changes: string[] = []
    const rendered = await renderEditor(
      <ControlledHarness initialValue='{"source":"target"}' changes={changes} />
    )

    try {
      await act(async () => {
        findButton(rendered.container, 'JSON Mode').click()
      })
      await act(async () => {
        setTextAreaValue(getJsonTextArea(rendered.container), '')
      })
      await act(async () => {
        findButton(rendered.container, 'Visual Mode').click()
      })

      assert.match(
        rendered.container.textContent ?? '',
        /No mappings configured/
      )
      assert.doesNotMatch(
        rendered.container.textContent ?? '',
        /Invalid JSON format/
      )
      assert.equal(changes.at(-1), '')
    } finally {
      await unmountEditor(rendered)
    }
  })
})
