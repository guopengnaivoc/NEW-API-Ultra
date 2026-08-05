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
import type {
  SecurityProofScope,
  VerificationMethod,
  VerificationMethods,
} from './types'

export interface SecurityVerificationRequest {
  method: VerificationMethod
  scope: SecurityProofScope
  target: string
  code?: string
  password?: string
}

export function buildSecurityVerificationRequest(
  method: VerificationMethod,
  scope: SecurityProofScope,
  target: string,
  credential?: string
): SecurityVerificationRequest {
  const request: SecurityVerificationRequest = {
    method,
    scope,
    target,
  }
  if (method === '2fa') {
    request.code = credential?.trim() ?? ''
  } else if (method === 'password') {
    request.password = credential ?? ''
  }
  return request
}

export function availableSecurityVerificationMethods(
  methods: VerificationMethods,
  allowedMethods?: readonly VerificationMethod[]
): VerificationMethod[] {
  const allowed = new Set<VerificationMethod>(
    allowedMethods ?? ['password', '2fa', 'passkey']
  )
  const available: VerificationMethod[] = []
  if (allowed.has('password') && methods.hasPassword) {
    available.push('password')
  }
  if (allowed.has('2fa') && methods.has2FA) {
    available.push('2fa')
  }
  if (
    allowed.has('passkey') &&
    methods.hasPasskey &&
    methods.passkeySupported
  ) {
    available.push('passkey')
  }
  return available
}

export function selectSecurityVerificationMethod(
  methods: VerificationMethods,
  preferredMethod?: VerificationMethod,
  allowedMethods?: readonly VerificationMethod[]
): VerificationMethod | null {
  const availableMethods = availableSecurityVerificationMethods(
    methods,
    allowedMethods
  )
  if (preferredMethod && availableMethods.includes(preferredMethod)) {
    return preferredMethod
  }
  if (availableMethods.includes('passkey')) return 'passkey'
  if (availableMethods.includes('2fa')) return '2fa'
  if (availableMethods.includes('password')) return 'password'
  return null
}
