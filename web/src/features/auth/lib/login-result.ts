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
export type TwoFAChallengeActions = {
  setPendingFlowToken: (flowToken: string) => void
  navigateToTwoFA: () => void
}

export type TwoFAChallengeStartResult = 'not_required' | 'started' | 'invalid'

export function startTwoFAChallenge(
  data: unknown,
  actions: TwoFAChallengeActions
): TwoFAChallengeStartResult {
  if (
    typeof data !== 'object' ||
    data === null ||
    !('require_2fa' in data) ||
    data.require_2fa !== true
  ) {
    return 'not_required'
  }
  if (
    !('flow_token' in data) ||
    typeof data.flow_token !== 'string' ||
    data.flow_token.trim() === ''
  ) {
    return 'invalid'
  }

  actions.setPendingFlowToken(data.flow_token)
  actions.navigateToTwoFA()
  return 'started'
}
