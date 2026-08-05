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
export type EntityDialogSession<TEntity> = Readonly<{
  entity: TEntity
  generation: number
}>

export type EntityDialogRequest<TEntity> = Readonly<{
  session: EntityDialogSession<TEntity>
  generation: number
  signal: AbortSignal
}>

export type EntityDialogLiveOwner<TEntity> = Readonly<{
  open: boolean
  entity: TEntity | null
}>

export function isEntityDialogOwnerCurrent<TEntity>(
  liveOwner: EntityDialogLiveOwner<TEntity>,
  capturedEntity: TEntity
): boolean {
  return liveOwner.open && Object.is(liveOwner.entity, capturedEntity)
}

export function createEntityDialogSessionCoordinator<TEntity>() {
  let sessionGeneration = 0
  let requestGeneration = 0
  let currentSession: EntityDialogSession<TEntity> | null = null
  let currentRequest: EntityDialogRequest<TEntity> | null = null
  let currentController: AbortController | null = null

  const isCurrentSession = (
    session: EntityDialogSession<TEntity>,
    entity?: TEntity
  ): boolean =>
    currentSession === session &&
    session.generation === sessionGeneration &&
    (entity === undefined || Object.is(session.entity, entity))

  const open = (entity: TEntity): EntityDialogSession<TEntity> => {
    currentController?.abort()
    currentController = null
    currentRequest = null
    const session = Object.freeze({
      entity,
      generation: ++sessionGeneration,
    })
    currentSession = session
    return session
  }

  const startRequest = (
    session: EntityDialogSession<TEntity>
  ): EntityDialogRequest<TEntity> | null => {
    if (!isCurrentSession(session)) return null
    currentController?.abort()
    const controller = new AbortController()
    const request = Object.freeze({
      session,
      generation: ++requestGeneration,
      signal: controller.signal,
    })
    currentController = controller
    currentRequest = request
    return request
  }

  const isCurrentRequest = (request: EntityDialogRequest<TEntity>): boolean =>
    currentRequest === request &&
    request.generation === requestGeneration &&
    isCurrentSession(request.session) &&
    !request.signal.aborted

  const close = (session: EntityDialogSession<TEntity>): void => {
    if (!isCurrentSession(session)) return
    currentController?.abort()
    currentController = null
    currentRequest = null
    currentSession = null
    sessionGeneration += 1
  }

  return {
    open,
    startRequest,
    isCurrentSession,
    isCurrentRequest,
    close,
  }
}
