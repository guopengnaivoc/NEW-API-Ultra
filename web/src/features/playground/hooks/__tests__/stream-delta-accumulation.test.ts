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

import { MESSAGE_ROLES, MESSAGE_STATUS } from '../../constants'
import { appendStreamDelta, applyStreamingChunk } from '../../lib'
import type { Message } from '../../types'

function assistantMessage(): Message {
  return {
    key: 'assistant',
    from: MESSAGE_ROLES.ASSISTANT,
    versions: [{ id: 'version', content: '' }],
    status: MESSAGE_STATUS.LOADING,
  }
}

describe('OpenAI-compatible stream delta accumulation', () => {
  test('keeps a later delta that starts with an earlier pending delta', () => {
    assert.equal(appendStreamDelta('a', 'ab'), 'aab')
  })

  test('appends content deltas that start with already-rendered content', () => {
    const first = applyStreamingChunk(assistantMessage(), 'content', 'a')
    const second = applyStreamingChunk(first, 'content', 'ab')

    assert.equal(second.versions[0]?.content, 'aab')
  })

  test('appends reasoning deltas that start with rendered reasoning', () => {
    const first = applyStreamingChunk(assistantMessage(), 'reasoning', 'a')
    const second = applyStreamingChunk(first, 'reasoning', 'ab')

    assert.equal(second.reasoning?.content, 'aab')
  })
})
