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

import { resolveChatUrl } from './chat-links'

describe('chat URL credential handling', () => {
  test('refuses templates that require a stored API key', () => {
    for (const template of [
      'https://chat.example/?key={key}',
      'https://chat.example/?config={cherryConfig}',
      'https://chat.example/?config={aionuiConfig}',
      'https://chat.example/?config={deepchatConfig}',
    ]) {
      assert.equal(
        resolveChatUrl({
          template,
          serverAddress: 'https://gateway.example',
        }),
        ''
      )
    }
  })

  test('resolves address-only templates without adding credentials', () => {
    assert.equal(
      resolveChatUrl({
        template: 'https://chat.example/?base={address}',
        serverAddress: 'https://gateway.example/v1',
      }),
      'https://chat.example/?base=https%3A%2F%2Fgateway.example%2Fv1'
    )
  })
})
