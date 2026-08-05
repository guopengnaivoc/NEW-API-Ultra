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

import {
  availableSecurityVerificationMethods,
  buildSecurityVerificationRequest,
  selectSecurityVerificationMethod,
} from '../protocol'

describe('targeted security verification requests', () => {
  test('keeps channel verification bound to its exact target', () => {
    assert.deepEqual(
      buildSecurityVerificationRequest(
        '2fa',
        'channel.key.read',
        '17',
        ' 123456 '
      ),
      {
        method: '2fa',
        code: '123456',
        scope: 'channel.key.read',
        target: '17',
      }
    )
  })

  test('binds password proof to the exact mutation target', () => {
    assert.deepEqual(
      buildSecurityVerificationRequest(
        'password',
        'email.change',
        'new@example.com',
        ' current password '
      ),
      {
        method: 'password',
        password: ' current password ',
        scope: 'email.change',
        target: 'new@example.com',
      }
    )
  })

  test('trims authenticator codes without changing the target', () => {
    assert.deepEqual(
      buildSecurityVerificationRequest(
        '2fa',
        'external.bind',
        'github',
        ' 123456 '
      ),
      {
        method: '2fa',
        code: '123456',
        scope: 'external.bind',
        target: 'github',
      }
    )
  })

  test('does not attach a credential body to Passkey verification', () => {
    assert.deepEqual(
      buildSecurityVerificationRequest('passkey', 'passkey.delete', '42'),
      {
        method: 'passkey',
        scope: 'passkey.delete',
        target: '42',
      }
    )
  })

  test('preserves the PAT rotation scope and self target', () => {
    assert.deepEqual(
      buildSecurityVerificationRequest(
        'password',
        'pat.rotate',
        'self',
        'current password'
      ),
      {
        method: 'password',
        password: 'current password',
        scope: 'pat.rotate',
        target: 'self',
      }
    )
  })
})

describe('security verification method selection', () => {
  test('limits channel reveal to Passkey and 2FA', () => {
    const methods = {
      hasPassword: true,
      has2FA: true,
      hasPasskey: true,
      passkeySupported: true,
    }
    const allowed = ['passkey', '2fa'] as const

    assert.deepEqual(availableSecurityVerificationMethods(methods, allowed), [
      '2fa',
      'passkey',
    ])
    assert.equal(
      selectSecurityVerificationMethod(methods, 'passkey', allowed),
      'passkey'
    )
    assert.equal(
      selectSecurityVerificationMethod(
        { ...methods, has2FA: false, hasPasskey: false },
        'passkey',
        allowed
      ),
      null
    )
  })

  test('keeps password-only accounts able to authorize sensitive mutations', () => {
    assert.equal(
      selectSecurityVerificationMethod({
        hasPassword: true,
        has2FA: false,
        hasPasskey: false,
        passkeySupported: false,
      }),
      'password'
    )
  })

  test('prefers a supported Passkey and falls back from an unavailable preference', () => {
    const methods = {
      hasPassword: true,
      has2FA: true,
      hasPasskey: true,
      passkeySupported: true,
    }
    assert.equal(selectSecurityVerificationMethod(methods), 'passkey')
    assert.equal(
      selectSecurityVerificationMethod(
        { ...methods, passkeySupported: false },
        'passkey'
      ),
      '2fa'
    )
    assert.equal(
      selectSecurityVerificationMethod({
        hasPassword: false,
        has2FA: false,
        hasPasskey: true,
        passkeySupported: false,
      }),
      null
    )
  })
})
