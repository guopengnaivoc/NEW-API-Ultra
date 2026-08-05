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

import { startTwoFAChallenge } from '../login-result'

describe('login result MFA coordination', () => {
  test('stores the flow token and selects the OTP route for a valid challenge', () => {
    let storedToken = ''
    let navigated = false

    const result = startTwoFAChallenge(
      { require_2fa: true, flow_token: 'flow-123' },
      {
        setPendingFlowToken: (flowToken) => {
          storedToken = flowToken
        },
        navigateToTwoFA: () => {
          navigated = true
        },
      }
    )

    assert.equal(result, 'started')
    assert.equal(storedToken, 'flow-123')
    assert.equal(navigated, true)
  })

  test('leaves a completed authentication bundle to the normal login path', () => {
    let actionCount = 0

    const result = startTwoFAChallenge(
      {
        access_token: 'access-token',
        access_expires_at: 1_900_000_000,
        session: { sid: 'session-id' },
        user: { id: 1, username: 'user', role: 1 },
      },
      {
        setPendingFlowToken: () => {
          actionCount += 1
        },
        navigateToTwoFA: () => {
          actionCount += 1
        },
      }
    )

    assert.equal(result, 'not_required')
    assert.equal(actionCount, 0)
  })

  test('rejects malformed challenges without storing or navigating', () => {
    const malformedChallenges: unknown[] = [
      { require_2fa: true },
      { require_2fa: true, flow_token: '' },
      { require_2fa: true, flow_token: '   ' },
      { require_2fa: true, flow_token: 123 },
    ]

    for (const challenge of malformedChallenges) {
      let actionCount = 0
      const result = startTwoFAChallenge(challenge, {
        setPendingFlowToken: () => {
          actionCount += 1
        },
        navigateToTwoFA: () => {
          actionCount += 1
        },
      })

      assert.equal(result, 'invalid')
      assert.equal(actionCount, 0)
    }
  })
})
