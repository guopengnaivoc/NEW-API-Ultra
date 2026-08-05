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
export type VerificationMethod = 'password' | '2fa' | 'passkey'

export type SecurityProofScope =
  | 'channel.key.read'
  | 'passkey.register'
  | 'passkey.delete'
  | 'email.change'
  | 'twofa.setup'
  | 'external.bind'
  | 'external.unbind'
  | 'pat.rotate'
  | 'account.delete'
  | 'token.key.rotate'
  | 'admin.binding.clear'
  | 'admin.twofa.disable'
  | 'admin.passkey.reset'

export interface SecurityProof {
  proof_token: string
  expires_at: number
  method: VerificationMethod
  scope: SecurityProofScope
  target: string
}

export interface VerificationMethods {
  hasPassword: boolean
  has2FA: boolean
  hasPasskey: boolean
  passkeySupported: boolean
}

export interface SecureVerificationState {
  method: VerificationMethod | null
  scope?: SecurityProofScope
  target?: string
  allowedMethods?: readonly VerificationMethod[]
  loading: boolean
  code: string
  title?: string
  description?: string
}

export interface UseSecureVerificationOptions {
  onSuccess?: (result: unknown, method: VerificationMethod) => void
  onError?: (error: unknown) => void
  successMessage?: string
  autoReset?: boolean
}

export interface StartVerificationOptions {
  scope: SecurityProofScope
  target: string
  allowedMethods?: readonly VerificationMethod[]
  preferredMethod?: VerificationMethod
  title?: string
  description?: string
}
