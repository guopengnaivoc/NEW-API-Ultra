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
import { after, beforeEach, test } from 'node:test'

import { Window } from 'happy-dom'
import type { FormEvent } from 'react'

import type { PromptInputMessage, PromptInputProps } from '../prompt-input'

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
  'fetch',
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
const revokeObjectUrlDescriptor = Object.getOwnPropertyDescriptor(
  URL,
  'revokeObjectURL'
)
let nextObjectUrl = 1
let objectUrlOverrides: string[] = []
let createdObjectUrls: string[] = []
let revokedObjectUrls: string[] = []
let fetchedUrls: string[] = []
let pendingFetches: {
  resolve: (response: Response) => void
  reject: (reason?: unknown) => void
}[] = []
let pendingFileReads: ControlledFileReader[] = []

class ControlledFileReader {
  result: string | ArrayBuffer | null = null
  onloadend: (() => void) | null = null
  onerror: ((reason?: unknown) => void) | null = null

  readAsDataURL(_blob: Blob) {
    pendingFileReads.push(this)
  }
}

Object.defineProperty(URL, 'createObjectURL', {
  configurable: true,
  value: () => {
    const url = objectUrlOverrides.shift() ?? `blob:file-${nextObjectUrl++}`
    createdObjectUrls.push(url)
    return url
  },
})
Object.defineProperty(URL, 'revokeObjectURL', {
  configurable: true,
  value: (url: string) => {
    revokedObjectUrls.push(url)
  },
})
Object.defineProperty(globalThis, 'fetch', {
  configurable: true,
  value: (input: RequestInfo | URL) => {
    fetchedUrls.push(String(input))
    return new Promise<Response>((resolve, reject) => {
      pendingFetches.push({ resolve, reject })
    })
  },
})
Object.defineProperty(globalThis, 'FileReader', {
  configurable: true,
  value: ControlledFileReader,
})

const { act, StrictMode, useState } = await import('react')
const { createRoot } = await import('react-dom/client')
const i18next = (await import('i18next')).default
const { initReactI18next } = await import('react-i18next')
await i18next.use(initReactI18next).init({
  lng: 'en',
  resources: {
    en: {
      translation: {
        'Failed to process attachments.': 'Failed to process attachments.',
        'Upload files': 'Upload files',
      },
    },
  },
})
const {
  PromptInput,
  PromptInputProvider,
  PromptInputTextarea,
  usePromptInputAttachments,
  usePromptInputController,
  useProviderAttachments,
} = await import('../prompt-input')

const reactTestGlobals = globalThis as typeof globalThis & {
  IS_REACT_ACT_ENVIRONMENT?: boolean
}
reactTestGlobals.IS_REACT_ACT_ENVIRONMENT = true

function AttachmentProbe() {
  const attachments = usePromptInputAttachments()

  return (
    <>
      <button
        data-action='add-a'
        onClick={() =>
          attachments.add([new File(['A'], 'a.txt', { type: 'text/plain' })])
        }
        type='button'
      >
        Add A
      </button>
      <button
        data-action='add-b'
        onClick={() =>
          attachments.add([new File(['B'], 'b.txt', { type: 'text/plain' })])
        }
        type='button'
      >
        Add B
      </button>
      <button
        data-action='add-a-and-submit'
        onClick={(event) => {
          attachments.add([new File(['A'], 'a.txt', { type: 'text/plain' })])
          event.currentTarget.form?.requestSubmit()
        }}
        type='button'
      >
        Add A and submit
      </button>
      <button
        data-action='remove-first'
        onClick={() => {
          const first = attachments.files[0]
          if (first) {
            attachments.remove(first.id)
          }
        }}
        type='button'
      >
        Remove first
      </button>
      <button data-action='clear' onClick={attachments.clear} type='button'>
        Clear
      </button>
      <output data-attachments>
        {attachments.files
          .map((file) => `${file.filename}:${file.url}`)
          .join('|')}
      </output>
    </>
  )
}

function ProviderOutsideAttachmentProbe() {
  const attachments = usePromptInputAttachments()

  return (
    <>
      <button
        data-action='add-provider-outside'
        onClick={() =>
          attachments.add([
            new File(['outside'], 'outside.txt', { type: 'text/plain' }),
          ])
        }
        type='button'
      >
        Add provider attachment outside PromptInput
      </button>
      <output data-provider-attachments>
        {attachments.files
          .map((file) => `${file.filename}:${file.url}`)
          .join('|')}
      </output>
    </>
  )
}

function ProviderSubmissionProbe(props: { onComplete?: () => void }) {
  const controller = usePromptInputController()

  return (
    <>
      <button
        data-action='set-provider-text-and-submit'
        onClick={(event) => {
          controller.textInput.setInput('same-event provider text')
          event.currentTarget.form?.requestSubmit()
        }}
        type='button'
      >
        Set provider text and submit
      </button>
      <button
        data-action='set-provider-text-and-complete'
        onClick={() => {
          props.onComplete?.()
          controller.textInput.setInput('same-batch provider draft')
        }}
        type='button'
      >
        Set provider text and complete
      </button>
    </>
  )
}

function ProviderApiBypassProbe() {
  const controller = usePromptInputController()
  const providerAttachments = useProviderAttachments()

  return (
    <>
      <button
        data-action='add-via-controller'
        onClick={() =>
          controller.attachments.add([
            new File(['controller'], 'controller.txt', {
              type: 'text/plain',
            }),
          ])
        }
        type='button'
      >
        Add through controller
      </button>
      <button
        data-action='add-via-provider-hook'
        onClick={() =>
          providerAttachments.add([
            new File(['provider hook'], 'provider-hook.txt', {
              type: 'text/plain',
            }),
          ])
        }
        type='button'
      >
        Add through provider hook
      </button>
      <output data-controller-attachments>
        {controller.attachments.files
          .map((file) => `${file.filename}:${file.url}`)
          .join('|')}
      </output>
      <output data-provider-hook-attachments>
        {providerAttachments.files
          .map((file) => `${file.filename}:${file.url}`)
          .join('|')}
      </output>
    </>
  )
}

function EnabledProviderAttachmentProbe() {
  const attachments = usePromptInputAttachments()

  return (
    <output data-enabled-provider-attachments>
      {attachments.files
        .map((file) => `${file.filename}:${file.url}`)
        .join('|')}
    </output>
  )
}

function LocalControlledSubmissionProbe(props: { initialText?: string }) {
  const [text, setText] = useState(props.initialText ?? '')

  return (
    <>
      <PromptInputTextarea
        data-message
        onChange={(event) => setText(event.currentTarget.value)}
        value={text}
      />
      <output data-parent-message>{text}</output>
    </>
  )
}

type RenderedPrompt = {
  container: HTMLDivElement
  root: ReturnType<typeof createRoot>
}

type PromptError = {
  code: string
  message: string
}

type SubmissionPromptOptions = {
  attachmentsEnabled?: boolean
  controlled?: boolean
  globalDrop?: boolean
  includeSibling?: boolean
  includeProviderOutside?: boolean
  initialText?: string
  onError?: (error: PromptError) => void
  onProviderCompletion?: () => void
  onSubmit: (
    message: PromptInputMessage,
    event: FormEvent<HTMLFormElement>
  ) => void | Promise<void>
  provider?: boolean
}

function createDeferred<T>() {
  let resolvePromise: (value: T | PromiseLike<T>) => void = () => undefined
  let rejectPromise: (reason?: unknown) => void = () => undefined
  const promise = new Promise<T>((resolve, reject) => {
    resolvePromise = resolve
    rejectPromise = reject
  })

  return {
    promise,
    reject: rejectPromise,
    resolve: resolvePromise,
  }
}

async function renderLocalPrompt(
  options: { strictMode?: boolean } = {}
): Promise<RenderedPrompt> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const prompt = (
    <PromptInput onSubmit={() => undefined}>
      <AttachmentProbe />
    </PromptInput>
  )

  await act(async () => {
    root.render(options.strictMode ? <StrictMode>{prompt}</StrictMode> : prompt)
  })

  return { container, root }
}

async function renderProviderPrompt(
  options: { strictMode?: boolean } = {}
): Promise<RenderedPrompt> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  const prompt = (
    <PromptInputProvider>
      <AttachmentProbe />
    </PromptInputProvider>
  )

  await act(async () => {
    root.render(options.strictMode ? <StrictMode>{prompt}</StrictMode> : prompt)
  })

  return { container, root }
}

async function renderSubmissionPrompt(
  options: SubmissionPromptOptions
): Promise<RenderedPrompt> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)
  let messageControl = (
    <PromptInputTextarea data-message defaultValue={options.initialText} />
  )
  if (options.controlled) {
    messageControl = (
      <LocalControlledSubmissionProbe initialText={options.initialText} />
    )
  }
  if (options.provider) {
    messageControl = <PromptInputTextarea data-message />
  }
  const prompt = (
    <PromptInput
      attachmentsEnabled={options.attachmentsEnabled}
      globalDrop={options.globalDrop}
      onError={options.onError as PromptInputProps['onError']}
      onSubmit={options.onSubmit}
    >
      {messageControl}
      {options.includeSibling ? (
        <input data-sibling defaultValue='original sibling' name='unrelated' />
      ) : null}
      {options.provider ? (
        <ProviderSubmissionProbe onComplete={options.onProviderCompletion} />
      ) : null}
      <AttachmentProbe />
    </PromptInput>
  )
  const tree = options.provider ? (
    <PromptInputProvider initialInput={options.initialText}>
      {options.includeProviderOutside ? (
        <ProviderOutsideAttachmentProbe />
      ) : null}
      {prompt}
    </PromptInputProvider>
  ) : (
    prompt
  )

  await act(async () => {
    root.render(tree)
  })

  return { container, root }
}

async function renderProviderCapabilityBoundary(
  onSubmit: SubmissionPromptOptions['onSubmit']
): Promise<RenderedPrompt> {
  const container = document.createElement('div')
  document.body.append(container)
  const root = createRoot(container)

  await act(async () => {
    root.render(
      <PromptInputProvider initialInput='shared provider draft'>
        <PromptInput attachmentsEnabled={false} onSubmit={() => undefined}>
          <ProviderApiBypassProbe />
        </PromptInput>
        <PromptInput data-enabled-prompt onSubmit={onSubmit}>
          <PromptInputTextarea data-enabled-message />
          <EnabledProviderAttachmentProbe />
        </PromptInput>
      </PromptInputProvider>
    )
  })

  return { container, root }
}

function getAction(
  rendered: RenderedPrompt,
  action: string
): HTMLButtonElement {
  const button = rendered.container.querySelector<HTMLButtonElement>(
    `[data-action="${action}"]`
  )
  assert.ok(button)
  return button
}

function getAttachmentState(rendered: RenderedPrompt): string {
  const output =
    rendered.container.querySelector<HTMLOutputElement>('[data-attachments]')
  assert.ok(output)
  return output.textContent ?? ''
}

function getProviderAttachmentState(rendered: RenderedPrompt): string {
  const output = rendered.container.querySelector<HTMLOutputElement>(
    '[data-provider-attachments]'
  )
  assert.ok(output)
  return output.textContent ?? ''
}

function getOutputText(rendered: RenderedPrompt, selector: string): string {
  const output = rendered.container.querySelector<HTMLOutputElement>(selector)
  assert.ok(output)
  return output.textContent ?? ''
}

function getMessageInput(rendered: RenderedPrompt): HTMLTextAreaElement {
  const input =
    rendered.container.querySelector<HTMLTextAreaElement>('[data-message]')
  assert.ok(input)
  return input
}

function getSiblingInput(rendered: RenderedPrompt): HTMLInputElement {
  const input =
    rendered.container.querySelector<HTMLInputElement>('[data-sibling]')
  assert.ok(input)
  return input
}

function getParentMessage(rendered: RenderedPrompt): string {
  const output = rendered.container.querySelector<HTMLOutputElement>(
    '[data-parent-message]'
  )
  assert.ok(output)
  return output.textContent ?? ''
}

async function clickAction(rendered: RenderedPrompt, action: string) {
  await act(async () => {
    getAction(rendered, action).click()
  })
}

async function setMessageText(rendered: RenderedPrompt, value: string) {
  const input = getMessageInput(rendered)
  const valueSetter = Object.getOwnPropertyDescriptor(
    HTMLTextAreaElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)

  await act(async () => {
    valueSetter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

async function setSiblingText(rendered: RenderedPrompt, value: string) {
  const input = getSiblingInput(rendered)
  const valueSetter = Object.getOwnPropertyDescriptor(
    HTMLInputElement.prototype,
    'value'
  )?.set
  assert.ok(valueSetter)

  await act(async () => {
    valueSetter.call(input, value)
    input.dispatchEvent(new Event('input', { bubbles: true }))
    input.dispatchEvent(new Event('change', { bubbles: true }))
  })
}

async function submitPrompt(rendered: RenderedPrompt) {
  const form = rendered.container.querySelector('form')
  assert.ok(form)
  const submitEvent = new Event('submit', {
    bubbles: true,
    cancelable: true,
  })

  await act(async () => {
    form.dispatchEvent(submitEvent)
    await Promise.resolve()
  })

  assert.equal(submitEvent.defaultPrevented, true)
}

async function submitEnabledProviderPrompt(rendered: RenderedPrompt) {
  const form = rendered.container.querySelector<HTMLFormElement>(
    'form[data-enabled-prompt]'
  )
  assert.ok(form)
  const submitEvent = new Event('submit', {
    bubbles: true,
    cancelable: true,
  })

  await act(async () => {
    form.dispatchEvent(submitEvent)
    await Promise.resolve()
  })

  assert.equal(submitEvent.defaultPrevented, true)
}

async function flushAsyncWork() {
  await act(async () => {
    await Promise.resolve()
    await Promise.resolve()
  })
}

async function resolveNextFetch(options: { ok?: boolean } = {}) {
  const pending = pendingFetches.shift()
  assert.ok(pending)

  await act(async () => {
    pending.resolve({
      blob: async () => new Blob(['converted'], { type: 'text/plain' }),
      ok: options.ok ?? true,
    } as Response)
    await Promise.resolve()
    await Promise.resolve()
  })
}

async function resolveNextFileRead(dataUrl: string) {
  const reader = pendingFileReads.shift()
  assert.ok(reader)

  await act(async () => {
    reader.result = dataUrl
    reader.onloadend?.()
    await Promise.resolve()
    await Promise.resolve()
  })
}

async function resolveSuccessfulConversion(dataUrl: string) {
  await resolveNextFetch()
  await resolveNextFileRead(dataUrl)
}

async function unmountPrompt(rendered: RenderedPrompt) {
  await act(async () => rendered.root.unmount())
  rendered.container.remove()
}

beforeEach(() => {
  nextObjectUrl = 1
  objectUrlOverrides = []
  createdObjectUrls = []
  revokedObjectUrls = []
  fetchedUrls = []
  pendingFetches = []
  pendingFileReads = []
  document.body.replaceChildren()
})

after(() => {
  domWindow.close()
  if (createObjectUrlDescriptor) {
    Object.defineProperty(URL, 'createObjectURL', createObjectUrlDescriptor)
  } else {
    Reflect.deleteProperty(URL, 'createObjectURL')
  }
  if (revokeObjectUrlDescriptor) {
    Object.defineProperty(URL, 'revokeObjectURL', revokeObjectUrlDescriptor)
  } else {
    Reflect.deleteProperty(URL, 'revokeObjectURL')
  }
  for (const [key, descriptor] of globalDescriptors) {
    if (descriptor) {
      Object.defineProperty(globalThis, key, descriptor)
    } else {
      Reflect.deleteProperty(globalThis, key)
    }
  }
})

test('dropping a file on the rendered local form adds the attachment', async () => {
  const rendered = await renderLocalPrompt()

  try {
    const form = rendered.container.querySelector('form')
    assert.ok(form)
    const dropEvent = new Event('drop', {
      bubbles: true,
      cancelable: true,
    })
    Object.defineProperty(dropEvent, 'dataTransfer', {
      value: {
        files: [new File(['drop'], 'drop.txt', { type: 'text/plain' })],
        types: ['Files'],
      },
    })

    await act(async () => form.dispatchEvent(dropEvent))

    assert.equal(dropEvent.defaultPrevented, true)
    assert.equal(getAttachmentState(rendered), 'drop.txt:blob:file-1')
  } finally {
    await unmountPrompt(rendered)
  }
})

test('a bubbling form drop with global capture adds one attachment', async () => {
  const rendered = await renderSubmissionPrompt({
    globalDrop: true,
    onSubmit: () => undefined,
  })

  try {
    const form = rendered.container.querySelector('form')
    assert.ok(form)
    const dropEvent = new Event('drop', {
      bubbles: true,
      cancelable: true,
    })
    Object.defineProperty(dropEvent, 'dataTransfer', {
      value: {
        files: [new File(['drop'], 'drop.txt', { type: 'text/plain' })],
        types: ['Files'],
      },
    })

    await act(async () => form.dispatchEvent(dropEvent))

    assert.equal(dropEvent.defaultPrevented, true)
    assert.equal(getAttachmentState(rendered), 'drop.txt:blob:file-1')
    assert.deepEqual(createdObjectUrls, ['blob:file-1'])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('disabled attachment capture leaves file paste to the browser', async () => {
  const rendered = await renderSubmissionPrompt({
    attachmentsEnabled: false,
    onSubmit: () => undefined,
  })

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
    assert.equal(getAttachmentState(rendered), '')
    assert.deepEqual(createdObjectUrls, [])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('disabled attachment capture does not intercept form file drops', async () => {
  const rendered = await renderSubmissionPrompt({
    attachmentsEnabled: false,
    onSubmit: () => undefined,
  })

  try {
    const form = rendered.container.querySelector('form')
    assert.ok(form)
    const dropEvent = new Event('drop', {
      bubbles: true,
      cancelable: true,
    })
    Object.defineProperty(dropEvent, 'dataTransfer', {
      value: {
        files: [new File(['drop'], 'drop.txt', { type: 'text/plain' })],
        types: ['Files'],
      },
    })

    await act(async () => form.dispatchEvent(dropEvent))

    assert.equal(dropEvent.defaultPrevented, false)
    assert.equal(getAttachmentState(rendered), '')
    assert.deepEqual(createdObjectUrls, [])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('disabled attachment capture does not install global file-drop handling', async () => {
  const rendered = await renderSubmissionPrompt({
    attachmentsEnabled: false,
    globalDrop: true,
    onSubmit: () => undefined,
  })

  try {
    const dropEvent = new Event('drop', {
      bubbles: true,
      cancelable: true,
    })
    Object.defineProperty(dropEvent, 'dataTransfer', {
      value: {
        files: [new File(['global'], 'global.txt', { type: 'text/plain' })],
        types: ['Files'],
      },
    })

    await act(async () => document.dispatchEvent(dropEvent))

    assert.equal(dropEvent.defaultPrevented, false)
    assert.equal(getAttachmentState(rendered), '')
    assert.deepEqual(createdObjectUrls, [])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('disabled attachment capture leaves text-only paste unchanged', async () => {
  const rendered = await renderSubmissionPrompt({
    attachmentsEnabled: false,
    initialText: 'existing draft',
    onSubmit: () => undefined,
  })

  try {
    const pasteEvent = new Event('paste', {
      bubbles: true,
      cancelable: true,
    })
    Object.defineProperty(pasteEvent, 'clipboardData', {
      value: {
        items: [
          {
            getAsFile: () => null,
            kind: 'string',
          },
        ],
      },
    })

    await act(async () => getMessageInput(rendered).dispatchEvent(pasteEvent))

    assert.equal(pasteEvent.defaultPrevented, false)
    assert.equal(getMessageInput(rendered).value, 'existing draft')
    assert.equal(getAttachmentState(rendered), '')
  } finally {
    await unmountPrompt(rendered)
  }
})

test('disabled attachment capture ignores hidden file-input changes', async () => {
  const rendered = await renderSubmissionPrompt({
    attachmentsEnabled: false,
    onSubmit: () => undefined,
  })

  try {
    const fileInput =
      rendered.container.querySelector<HTMLInputElement>('input[type="file"]')
    assert.ok(fileInput)
    Object.defineProperty(fileInput, 'files', {
      configurable: true,
      value: [new File(['change'], 'change.txt', { type: 'text/plain' })],
    })

    await act(async () =>
      fileInput.dispatchEvent(new Event('change', { bubbles: true }))
    )

    assert.equal(getAttachmentState(rendered), '')
    assert.deepEqual(createdObjectUrls, [])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('disabled attachment add is a no-op and submission passes no files', async () => {
  const submissions: PromptInputMessage[] = []
  const rendered = await renderSubmissionPrompt({
    attachmentsEnabled: false,
    initialText: 'text only',
    onSubmit: (message) => {
      submissions.push(message)
    },
  })

  try {
    await clickAction(rendered, 'add-a')
    await submitPrompt(rendered)
    await flushAsyncWork()

    assert.equal(getAttachmentState(rendered), '')
    assert.deepEqual(createdObjectUrls, [])
    assert.deepEqual(fetchedUrls, [])
    assert.deepEqual(submissions, [{ files: [], text: 'text only' }])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('disabled provider PromptInput ignores attachments owned outside it', async () => {
  const submissions: PromptInputMessage[] = []
  const rendered = await renderSubmissionPrompt({
    attachmentsEnabled: false,
    includeProviderOutside: true,
    initialText: 'provider text only',
    onSubmit: (message) => {
      submissions.push(message)
    },
    provider: true,
  })

  try {
    await clickAction(rendered, 'add-provider-outside')
    revokedObjectUrls = []

    assert.equal(
      getProviderAttachmentState(rendered),
      'outside.txt:blob:file-1'
    )
    assert.equal(getAttachmentState(rendered), '')

    await submitPrompt(rendered)
    await flushAsyncWork()

    assert.deepEqual(fetchedUrls, [])
    assert.deepEqual(submissions, [{ files: [], text: 'provider text only' }])
    assert.equal(
      getProviderAttachmentState(rendered),
      'outside.txt:blob:file-1'
    )
    assert.deepEqual(revokedObjectUrls, [])
  } finally {
    await unmountPrompt(rendered)
  }

  assert.deepEqual(revokedObjectUrls, ['blob:file-1'])
})

for (const providerApi of [
  {
    action: 'add-via-controller',
    name: 'controller attachments',
  },
  {
    action: 'add-via-provider-hook',
    name: 'provider attachments hook',
  },
] as const) {
  test(`disabled PromptInput masks ${providerApi.name} from enabled siblings`, async () => {
    const submissions: PromptInputMessage[] = []
    const rendered = await renderProviderCapabilityBoundary((message) => {
      submissions.push(message)
    })

    try {
      await clickAction(rendered, providerApi.action)

      assert.equal(getOutputText(rendered, '[data-controller-attachments]'), '')
      assert.equal(
        getOutputText(rendered, '[data-provider-hook-attachments]'),
        ''
      )
      assert.equal(
        getOutputText(rendered, '[data-enabled-provider-attachments]'),
        ''
      )
      assert.deepEqual(createdObjectUrls, [])

      await submitEnabledProviderPrompt(rendered)
      await flushAsyncWork()

      assert.deepEqual(fetchedUrls, [])
      assert.deepEqual(submissions, [
        { files: [], text: 'shared provider draft' },
      ])
    } finally {
      await unmountPrompt(rendered)
    }
  })
}

test('adding a second local attachment keeps the first Blob URL live', async () => {
  const rendered = await renderLocalPrompt()

  try {
    await clickAction(rendered, 'add-a')
    await clickAction(rendered, 'add-b')

    assert.equal(
      getAttachmentState(rendered),
      'a.txt:blob:file-1|b.txt:blob:file-2'
    )
    assert.deepEqual(revokedObjectUrls, [])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('removing a local attachment revokes only its Blob URL once', async () => {
  const rendered = await renderLocalPrompt()

  try {
    await clickAction(rendered, 'add-a')
    await clickAction(rendered, 'add-b')
    revokedObjectUrls = []

    await clickAction(rendered, 'remove-first')

    assert.equal(getAttachmentState(rendered), 'b.txt:blob:file-2')
    assert.deepEqual(revokedObjectUrls, ['blob:file-1'])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('clearing local attachments revokes each Blob URL exactly once', async () => {
  const rendered = await renderLocalPrompt()

  try {
    await clickAction(rendered, 'add-a')
    await clickAction(rendered, 'add-b')
    revokedObjectUrls = []

    await clickAction(rendered, 'clear')

    assert.equal(getAttachmentState(rendered), '')
    assert.deepEqual(revokedObjectUrls, ['blob:file-1', 'blob:file-2'])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('unmounting a local prompt revokes its remaining Blob URL once', async () => {
  const rendered = await renderLocalPrompt()
  await clickAction(rendered, 'add-a')
  revokedObjectUrls = []

  await unmountPrompt(rendered)

  assert.deepEqual(revokedObjectUrls, ['blob:file-1'])
})

test('local remove and unmount never revoke data or HTTP URLs', async () => {
  objectUrlOverrides = [
    'data:text/plain;base64,QQ==',
    'https://example.test/b.txt',
  ]
  const rendered = await renderLocalPrompt()

  await clickAction(rendered, 'add-a')
  await clickAction(rendered, 'add-b')
  revokedObjectUrls = []
  await clickAction(rendered, 'remove-first')
  await unmountPrompt(rendered)

  assert.deepEqual(revokedObjectUrls, [])
})

test('the provider revokes removed and remaining Blob URLs exactly once', async () => {
  const rendered = await renderProviderPrompt()

  await clickAction(rendered, 'add-a')
  await clickAction(rendered, 'add-b')
  await clickAction(rendered, 'remove-first')
  await unmountPrompt(rendered)

  assert.deepEqual(revokedObjectUrls, ['blob:file-1', 'blob:file-2'])
})

test('Strict Mode local ownership creates and revokes each Blob URL exactly once', async () => {
  const rendered = await renderLocalPrompt({ strictMode: true })

  try {
    await clickAction(rendered, 'add-a')
    await clickAction(rendered, 'add-b')
    await clickAction(rendered, 'remove-first')
  } finally {
    await unmountPrompt(rendered)
  }

  assert.deepEqual(
    {
      created: createdObjectUrls,
      revoked: revokedObjectUrls,
    },
    {
      created: ['blob:file-1', 'blob:file-2'],
      revoked: ['blob:file-1', 'blob:file-2'],
    }
  )
})

test('Strict Mode provider ownership creates and revokes each Blob URL exactly once', async () => {
  const rendered = await renderProviderPrompt({ strictMode: true })

  try {
    await clickAction(rendered, 'add-a')
    await clickAction(rendered, 'add-b')
    await clickAction(rendered, 'remove-first')
  } finally {
    await unmountPrompt(rendered)
  }

  assert.deepEqual(
    {
      created: createdObjectUrls,
      revoked: revokedObjectUrls,
    },
    {
      created: ['blob:file-1', 'blob:file-2'],
      revoked: ['blob:file-1', 'blob:file-2'],
    }
  )
})

test('a failed Blob conversion reports one error and preserves the local draft', async () => {
  const errors: PromptError[] = []
  const submissions: PromptInputMessage[] = []
  const rendered = await renderSubmissionPrompt({
    onError: (error) => errors.push(error),
    onSubmit: (message) => {
      submissions.push(message)
    },
  })

  try {
    await setMessageText(rendered, 'retry this draft')
    await clickAction(rendered, 'add-a')
    revokedObjectUrls = []

    await submitPrompt(rendered)
    await resolveNextFetch({ ok: false })
    if (pendingFileReads.length > 0) {
      await resolveNextFileRead('data:text/plain;base64,QQ==')
    }
    await flushAsyncWork()

    assert.deepEqual(errors, [
      {
        code: 'submit_error',
        message: 'Failed to process attachments.',
      },
    ])
    assert.deepEqual(submissions, [])
    assert.equal(getMessageInput(rendered).value, 'retry this draft')
    assert.equal(getAttachmentState(rendered), 'a.txt:blob:file-1')
    assert.deepEqual(revokedObjectUrls, [])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('a rejected async submission reports one error and preserves provider state', async () => {
  const errors: PromptError[] = []
  const submissions: PromptInputMessage[] = []
  const submission = createDeferred<void>()
  const rendered = await renderSubmissionPrompt({
    initialText: 'provider draft',
    onError: (error) => errors.push(error),
    onSubmit: (message) => {
      submissions.push(message)
      return submission.promise
    },
    provider: true,
  })

  try {
    await clickAction(rendered, 'add-a')
    revokedObjectUrls = []
    await submitPrompt(rendered)
    await resolveSuccessfulConversion('data:text/plain;base64,QQ==')

    await act(async () => {
      submission.reject(new Error('submission rejected'))
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.equal(submissions.length, 1)
    assert.deepEqual(errors, [
      {
        code: 'submit_error',
        message: 'Failed to process attachments.',
      },
    ])
    assert.equal(getMessageInput(rendered).value, 'provider draft')
    assert.equal(getAttachmentState(rendered), 'a.txt:blob:file-1')
    assert.deepEqual(revokedObjectUrls, [])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('a successful submission converts Blob URLs to data URLs', async () => {
  const errors: PromptError[] = []
  const submissions: PromptInputMessage[] = []
  const rendered = await renderSubmissionPrompt({
    onError: (error) => errors.push(error),
    onSubmit: (message) => {
      submissions.push(message)
    },
  })

  try {
    await setMessageText(rendered, 'send this')
    await clickAction(rendered, 'add-a')
    revokedObjectUrls = []
    await submitPrompt(rendered)
    await resolveSuccessfulConversion('data:text/plain;base64,QQ==')
    await flushAsyncWork()

    assert.deepEqual(submissions, [
      {
        files: [
          {
            filename: 'a.txt',
            mediaType: 'text/plain',
            type: 'file',
            url: 'data:text/plain;base64,QQ==',
          },
        ],
        text: 'send this',
      },
    ])
    assert.deepEqual(errors, [])
    assert.equal(getAttachmentState(rendered), '')
    assert.equal(getMessageInput(rendered).value, '')
    assert.deepEqual(revokedObjectUrls, ['blob:file-1'])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('local success clears only the unchanged message with a nonempty default', async () => {
  const submissions: PromptInputMessage[] = []
  const submission = createDeferred<void>()
  const rendered = await renderSubmissionPrompt({
    includeSibling: true,
    initialText: 'default draft',
    onSubmit: (message) => {
      submissions.push(message)
      return submission.promise
    },
  })

  try {
    await submitPrompt(rendered)
    assert.deepEqual(submissions, [{ files: [], text: 'default draft' }])
    await setSiblingText(rendered, 'concurrent sibling')

    await act(async () => {
      submission.resolve(undefined)
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.equal(getMessageInput(rendered).value, '')
    assert.equal(getSiblingInput(rendered).value, 'concurrent sibling')
  } finally {
    await unmountPrompt(rendered)
  }
})

test('local controlled text remains parent-owned after successful submission', async () => {
  const submissions: PromptInputMessage[] = []
  const rendered = await renderSubmissionPrompt({
    controlled: true,
    initialText: 'controlled draft',
    onSubmit: (message) => {
      submissions.push(message)
    },
  })

  try {
    await submitPrompt(rendered)
    await flushAsyncWork()

    assert.deepEqual(submissions, [{ files: [], text: 'controlled draft' }])
    assert.equal(getParentMessage(rendered), 'controlled draft')
    assert.equal(getMessageInput(rendered).value, 'controlled draft')
  } finally {
    await unmountPrompt(rendered)
  }
})

test('provider text and files added during conversion survive successful completion', async () => {
  const submissions: PromptInputMessage[] = []
  const submission = createDeferred<void>()
  const rendered = await renderSubmissionPrompt({
    initialText: 'before conversion',
    onSubmit: (message) => {
      submissions.push(message)
      return submission.promise
    },
    provider: true,
  })

  try {
    await clickAction(rendered, 'add-a')
    revokedObjectUrls = []
    await submitPrompt(rendered)

    await setMessageText(rendered, 'after conversion started')
    await clickAction(rendered, 'add-b')
    await resolveSuccessfulConversion('data:text/plain;base64,QQ==')

    await act(async () => {
      submission.resolve(undefined)
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.deepEqual(submissions, [
      {
        files: [
          {
            filename: 'a.txt',
            mediaType: 'text/plain',
            type: 'file',
            url: 'data:text/plain;base64,QQ==',
          },
        ],
        text: 'before conversion',
      },
    ])
    assert.equal(getMessageInput(rendered).value, 'after conversion started')
    assert.equal(getAttachmentState(rendered), 'b.txt:blob:file-2')
    assert.deepEqual(revokedObjectUrls, ['blob:file-1'])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('provider submission snapshots text set in the same user event', async () => {
  const submissions: PromptInputMessage[] = []
  const rendered = await renderSubmissionPrompt({
    initialText: 'stale provider text',
    onSubmit: (message) => {
      submissions.push(message)
    },
    provider: true,
  })

  try {
    await clickAction(rendered, 'set-provider-text-and-submit')
    await flushAsyncWork()

    assert.deepEqual(submissions, [
      { files: [], text: 'same-event provider text' },
    ])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('provider completion preserves text set in the same React batch', async () => {
  const submission = createDeferred<void>()
  const rendered = await renderSubmissionPrompt({
    initialText: 'submitted provider text',
    onProviderCompletion: () => submission.resolve(undefined),
    onSubmit: () => submission.promise,
    provider: true,
  })

  try {
    await submitPrompt(rendered)
    await clickAction(rendered, 'set-provider-text-and-complete')
    await flushAsyncWork()

    assert.equal(getMessageInput(rendered).value, 'same-batch provider draft')
  } finally {
    await unmountPrompt(rendered)
  }
})

for (const provider of [false, true]) {
  const ownership = provider ? 'provider' : 'local'

  test(`${ownership} submission snapshots an attachment added in the same user event`, async () => {
    const submissions: PromptInputMessage[] = []
    const rendered = await renderSubmissionPrompt({
      onSubmit: (message) => {
        submissions.push(message)
      },
      provider,
    })

    try {
      await clickAction(rendered, 'add-a-and-submit')

      assert.deepEqual(fetchedUrls, ['blob:file-1'])
      await resolveSuccessfulConversion('data:text/plain;base64,QQ==')
      await flushAsyncWork()

      assert.deepEqual(submissions, [
        {
          files: [
            {
              filename: 'a.txt',
              mediaType: 'text/plain',
              type: 'file',
              url: 'data:text/plain;base64,QQ==',
            },
          ],
          text: '',
        },
      ])
      assert.equal(getAttachmentState(rendered), '')
      assert.deepEqual(revokedObjectUrls, ['blob:file-1'])
    } finally {
      await unmountPrompt(rendered)
    }
  })
}

test('success removes only snapshotted local attachments and preserves newer remote URLs', async () => {
  const submissions: PromptInputMessage[] = []
  const rendered = await renderSubmissionPrompt({
    onSubmit: (message) => {
      submissions.push(message)
    },
  })

  try {
    await setMessageText(rendered, 'snapshot')
    await clickAction(rendered, 'add-a')
    revokedObjectUrls = []
    await submitPrompt(rendered)

    objectUrlOverrides = ['https://example.test/b.txt']
    await clickAction(rendered, 'add-b')
    await resolveSuccessfulConversion('data:text/plain;base64,QQ==')
    await flushAsyncWork()

    assert.deepEqual(submissions, [
      {
        files: [
          {
            filename: 'a.txt',
            mediaType: 'text/plain',
            type: 'file',
            url: 'data:text/plain;base64,QQ==',
          },
        ],
        text: 'snapshot',
      },
    ])
    assert.equal(
      getAttachmentState(rendered),
      'b.txt:https://example.test/b.txt'
    )
    assert.deepEqual(revokedObjectUrls, ['blob:file-1'])
  } finally {
    await unmountPrompt(rendered)
  }
})

test('a second submit during conversion starts no duplicate work', async () => {
  const submissions: PromptInputMessage[] = []
  const submission = createDeferred<void>()
  const rendered = await renderSubmissionPrompt({
    onSubmit: (message) => {
      submissions.push(message)
      return submission.promise
    },
  })

  try {
    await clickAction(rendered, 'add-a')
    revokedObjectUrls = []
    await submitPrompt(rendered)
    await submitPrompt(rendered)
    const startedFetches = fetchedUrls.length

    let conversion = 0
    while (pendingFetches.length > 0) {
      conversion += 1
      await resolveSuccessfulConversion(
        `data:text/plain;base64,conversion-${conversion}`
      )
    }

    await act(async () => {
      submission.resolve(undefined)
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.equal(startedFetches, 1)
    assert.deepEqual(fetchedUrls, ['blob:file-1'])
    assert.equal(submissions.length, 1)
    assert.deepEqual(revokedObjectUrls, ['blob:file-1'])
  } finally {
    await unmountPrompt(rendered)
  }
})

for (const provider of [false, true]) {
  const ownership = provider ? 'provider' : 'local'

  test(`unmounting during ${ownership} conversion prevents submission and duplicate cleanup`, async () => {
    const errors: PromptError[] = []
    const submissions: PromptInputMessage[] = []
    const rendered = await renderSubmissionPrompt({
      onError: (error) => errors.push(error),
      onSubmit: (message) => {
        submissions.push(message)
      },
      provider,
    })

    await clickAction(rendered, 'add-a')
    revokedObjectUrls = []
    await submitPrompt(rendered)
    await unmountPrompt(rendered)

    assert.deepEqual(revokedObjectUrls, ['blob:file-1'])

    await resolveSuccessfulConversion('data:text/plain;base64,QQ==')
    await flushAsyncWork()

    assert.deepEqual(submissions, [])
    assert.deepEqual(errors, [])
    assert.deepEqual(revokedObjectUrls, ['blob:file-1'])
  })

  test(`unmounting during ${ownership} submission prevents duplicate cleanup`, async () => {
    const errors: PromptError[] = []
    const submissions: PromptInputMessage[] = []
    const submission = createDeferred<void>()
    const rendered = await renderSubmissionPrompt({
      onError: (error) => errors.push(error),
      onSubmit: (message) => {
        submissions.push(message)
        return submission.promise
      },
      provider,
    })

    await clickAction(rendered, 'add-a')
    revokedObjectUrls = []
    await submitPrompt(rendered)
    await resolveSuccessfulConversion('data:text/plain;base64,QQ==')
    assert.equal(submissions.length, 1)

    await unmountPrompt(rendered)
    assert.deepEqual(revokedObjectUrls, ['blob:file-1'])

    await act(async () => {
      submission.resolve(undefined)
      await Promise.resolve()
      await Promise.resolve()
    })

    assert.deepEqual(errors, [])
    assert.deepEqual(revokedObjectUrls, ['blob:file-1'])
  })
}
