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
  createEntityDialogSessionCoordinator,
  isEntityDialogOwnerCurrent,
} from '../entity-dialog-session'

describe('entity dialog session coordinator', () => {
  test('requires the live dialog owner to stay open and match the captured entity', () => {
    assert.equal(
      isEntityDialogOwnerCurrent({ open: true, entity: 'A' }, 'A'),
      true
    )
    assert.equal(
      isEntityDialogOwnerCurrent({ open: false, entity: 'A' }, 'A'),
      false
    )
    assert.equal(
      isEntityDialogOwnerCurrent({ open: true, entity: 'B' }, 'A'),
      false
    )
  })

  test('requires an owning session to close', () => {
    const coordinator = createEntityDialogSessionCoordinator<number>()

    // oxlint-disable-next-line no-constant-condition
    if (false) {
      // @ts-expect-error close requires an owning session
      coordinator.close()
    }
  })

  test('invalidates and aborts the prior entity request', () => {
    const coordinator = createEntityDialogSessionCoordinator<number>()
    const sessionA = coordinator.open(101)
    const requestA = coordinator.startRequest(sessionA)
    assert.ok(requestA)

    const sessionB = coordinator.open(202)
    const requestB = coordinator.startRequest(sessionB)
    assert.ok(requestB)

    assert.equal(sessionA.generation, 1)
    assert.equal(sessionB.generation, 2)
    assert.equal(requestA.generation, 1)
    assert.equal(requestB.generation, 2)
    assert.equal(requestA.signal.aborted, true)
    assert.equal(coordinator.isCurrentSession(sessionA, 101), false)
    assert.equal(coordinator.isCurrentRequest(requestA), false)
    assert.equal(coordinator.isCurrentSession(sessionB, 202), true)
    assert.equal(coordinator.isCurrentRequest(requestB), true)
  })

  test('distinguishes a same-entity reopen from the prior session', () => {
    const coordinator = createEntityDialogSessionCoordinator<number>()
    const firstSession = coordinator.open(101)
    const firstRequest = coordinator.startRequest(firstSession)
    assert.ok(firstRequest)
    coordinator.close(firstSession)

    const reopenedSession = coordinator.open(101)
    const reopenedRequest = coordinator.startRequest(reopenedSession)
    assert.ok(reopenedRequest)

    assert.equal(firstSession.generation, 1)
    assert.equal(reopenedSession.generation, 3)
    assert.equal(firstRequest.generation, 1)
    assert.equal(reopenedRequest.generation, 2)
    assert.equal(firstRequest.signal.aborted, true)
    assert.equal(coordinator.isCurrentSession(firstSession, 101), false)
    assert.equal(coordinator.isCurrentSession(reopenedSession, 101), true)
  })

  test('rejects A-old after an A to B to A sequence', () => {
    const coordinator = createEntityDialogSessionCoordinator<string>()
    const oldA = coordinator.open('A')
    const oldARequest = coordinator.startRequest(oldA)
    assert.ok(oldARequest)
    coordinator.open('B')
    const currentA = coordinator.open('A')

    assert.equal(coordinator.isCurrentSession(oldA, 'A'), false)
    assert.equal(coordinator.isCurrentRequest(oldARequest), false)
    assert.equal(coordinator.isCurrentSession(currentA, 'A'), true)
  })

  test('allows only the latest read within a current session', () => {
    const coordinator = createEntityDialogSessionCoordinator<number>()
    const session = coordinator.open(101)
    const pageOne = coordinator.startRequest(session)
    const pageTwo = coordinator.startRequest(session)
    assert.ok(pageOne)
    assert.ok(pageTwo)

    assert.equal(pageOne.signal.aborted, true)
    assert.equal(coordinator.isCurrentRequest(pageOne), false)
    assert.equal(coordinator.isCurrentRequest(pageTwo), true)
  })

  test('ignores stale cleanup after a newer session opens', () => {
    const coordinator = createEntityDialogSessionCoordinator<number>()
    const sessionA = coordinator.open(101)
    const sessionB = coordinator.open(202)
    const requestB = coordinator.startRequest(sessionB)
    assert.ok(requestB)

    coordinator.close(sessionA)

    assert.equal(requestB.signal.aborted, false)
    assert.equal(coordinator.isCurrentSession(sessionB, 202), true)
    assert.equal(coordinator.isCurrentRequest(requestB), true)
  })
})
