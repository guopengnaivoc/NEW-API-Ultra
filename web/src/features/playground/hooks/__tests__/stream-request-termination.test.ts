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
import { describe, test } from 'node:test'

import { ERROR_MESSAGES } from '../../constants'
import type { ChatCompletionRequest } from '../../types'
import { createStreamRequestController } from '../use-stream-request'

class TerminationStreamSource {
  readyState = 0
  status = 200
  closed = false
  private listeners = new Map<
    string,
    Array<(event: Event & { data?: string; readyState?: number }) => void>
  >()

  addEventListener(
    type: string,
    listener: (event: Event & { data?: string; readyState?: number }) => void
  ) {
    const listeners = this.listeners.get(type) ?? []
    listeners.push(listener)
    this.listeners.set(type, listeners)
  }

  close() {
    this.closed = true
  }

  stream() {}

  emit(type: string, data?: string) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener({ data, readyState: this.readyState } as Event & {
        data?: string
        readyState?: number
      })
    }
  }
}

const payload: ChatCompletionRequest = {
  model: 'test-model',
  messages: [{ role: 'user', content: 'hello' }],
  stream: true,
}

function createTerminationHarness() {
  const source = new TerminationStreamSource()
  const streamingStates: boolean[] = []
  const errors: string[] = []
  let completions = 0
  const controller = createStreamRequestController({
    getHeaders: () => Promise.resolve({ Authorization: 'Bearer test' }),
    createSource: () => source,
    setStreaming: (streaming) => streamingStates.push(streaming),
  })

  return {
    source,
    streamingStates,
    errors,
    get completions() {
      return completions
    },
    start: () =>
      controller.send(payload, {
        onUpdate: () => undefined,
        onComplete: () => {
          completions += 1
        },
        onError: (error) => errors.push(error),
      }),
  }
}

describe('stream request terminal state', () => {
  for (const terminalEvent of ['readystatechange', 'error']) {
    test(`fails an HTTP 200 stream closed by ${terminalEvent} before [DONE]`, async () => {
      const harness = createTerminationHarness()
      await harness.start()

      harness.source.readyState = 2
      harness.source.emit(terminalEvent)
      harness.source.emit(terminalEvent)

      assert.deepEqual(harness.errors, [ERROR_MESSAGES.CONNECTION_CLOSED])
      assert.equal(harness.completions, 0)
      assert.equal(harness.source.closed, true)
      assert.deepEqual(harness.streamingStates, [false, true, false])
    })
  }

  test('does not report an error when closure follows [DONE]', async () => {
    const harness = createTerminationHarness()
    await harness.start()

    harness.source.emit('message', '[DONE]')
    harness.source.readyState = 2
    harness.source.emit('readystatechange')
    harness.source.emit('error')

    assert.deepEqual(harness.errors, [])
    assert.equal(harness.completions, 1)
    assert.equal(harness.source.closed, true)
    assert.deepEqual(harness.streamingStates, [false, true, false])
  })

  test('preserves a non-200 status when the stream closes', async () => {
    const harness = createTerminationHarness()
    await harness.start()

    harness.source.status = 502
    harness.source.readyState = 2
    harness.source.emit('readystatechange')

    assert.deepEqual(harness.errors, [
      `HTTP 502: ${ERROR_MESSAGES.CONNECTION_CLOSED}`,
    ])
    assert.equal(harness.source.closed, true)
    assert.deepEqual(harness.streamingStates, [false, true, false])
  })
})
