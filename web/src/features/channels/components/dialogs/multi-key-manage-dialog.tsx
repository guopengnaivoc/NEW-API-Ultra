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
import { useQueryClient } from '@tanstack/react-query'
import { Loader2, RefreshCw, Trash2, Power, PowerOff } from 'lucide-react'
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Separator } from '@/components/ui/separator'
import {
  ADMIN_PERMISSION_ACTIONS,
  ADMIN_PERMISSION_RESOURCES,
  hasPermission,
} from '@/lib/admin-permissions'
import {
  createEntityDialogSessionCoordinator,
  isEntityDialogOwnerCurrent,
  type EntityDialogLiveOwner,
  type EntityDialogRequest,
  type EntityDialogSession,
} from '@/lib/entity-dialog-session'
import { useAuthStore } from '@/stores/auth-store'

import {
  getMultiKeyStatus,
  enableMultiKey,
  disableMultiKey,
  deleteMultiKey,
  enableAllMultiKeys,
  disableAllMultiKeys,
  deleteDisabledMultiKeys,
} from '../../api'
import { MULTI_KEY_FILTER_OPTIONS } from '../../constants'
import {
  channelsQueryKeys,
  formatTimestamp,
  getMultiKeyStatusConfig,
  getMultiKeyConfirmMessage,
  isDestructiveAction,
} from '../../lib'
import type { MultiKeyConfirmAction, MultiKeyStatusResponse } from '../../types'
import { useChannels } from '../channels-provider'
import { StatisticsCard } from './multi-key-statistics-card'
import { MultiKeyTableRowActions } from './multi-key-table-row-actions'

type MultiKeyData = NonNullable<MultiKeyStatusResponse['data']>

type MultiKeyViewState = {
  status: 'idle' | 'loading' | 'ready' | 'error'
  request: EntityDialogRequest<number> | null
  data: MultiKeyData
}

type OwnedMultiKeyConfirmAction = MultiKeyConfirmAction & {
  session: EntityDialogSession<number>
}

const EMPTY_MULTI_KEY_DATA: MultiKeyData = {
  keys: [],
  total: 0,
  page: 1,
  page_size: 10,
  total_pages: 0,
  enabled_count: 0,
  manual_disabled_count: 0,
  auto_disabled_count: 0,
}

type MultiKeyManageDialogProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function MultiKeyManageDialog({
  open,
  onOpenChange,
}: MultiKeyManageDialogProps) {
  const { t } = useTranslation()
  const { currentRow } = useChannels()
  const currentChannelId = currentRow?.id ?? null
  const queryClient = useQueryClient()
  const currentUser = useAuthStore((s) => s.auth.user)
  const canEditSensitive = hasPermission(
    currentUser,
    ADMIN_PERMISSION_RESOURCES.CHANNEL,
    ADMIN_PERMISSION_ACTIONS.SENSITIVE_WRITE
  )

  const [viewState, setViewState] = useState<MultiKeyViewState>({
    status: 'idle',
    request: null,
    data: EMPTY_MULTI_KEY_DATA,
  })
  const [statusFilter, setStatusFilter] = useState<number | null>(null)
  const [confirmAction, setConfirmAction] =
    useState<OwnedMultiKeyConfirmAction | null>(null)
  const [performingSession, setPerformingSession] =
    useState<EntityDialogSession<number> | null>(null)
  const coordinatorRef = useRef<ReturnType<
    typeof createEntityDialogSessionCoordinator<number>
  > | null>(null)
  if (coordinatorRef.current === null) {
    coordinatorRef.current = createEntityDialogSessionCoordinator<number>()
  }
  const coordinator = coordinatorRef.current
  const liveOwnerRef = useRef<EntityDialogLiveOwner<number>>({
    open,
    entity: currentChannelId,
  })
  liveOwnerRef.current = { open, entity: currentChannelId }
  const translationRef = useRef(t)
  translationRef.current = t

  const loadKeyStatus = useCallback(
    async (
      session: EntityDialogSession<number>,
      page: number,
      size: number,
      status: number | null
    ) => {
      const owner = session.entity
      if (
        !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
        !coordinator.isCurrentSession(session, owner)
      ) {
        return
      }

      const request = coordinator.startRequest(session)
      if (request === null) return

      setViewState({
        status: 'loading',
        request,
        data: {
          ...EMPTY_MULTI_KEY_DATA,
          page,
          page_size: size,
        },
      })

      try {
        const response = await getMultiKeyStatus(
          owner,
          page,
          size,
          status === null ? undefined : status,
          { signal: request.signal }
        )
        if (
          !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
          !coordinator.isCurrentRequest(request)
        ) {
          return
        }

        const data = response.data
        const hasValidKeys = data?.keys == null || Array.isArray(data.keys)
        const keys = Array.isArray(data?.keys) ? data.keys : []
        const hasValidPagination =
          data != null &&
          [data.page, data.page_size, data.total_pages].every(
            (value) => Number.isSafeInteger(value) && value > 0
          ) &&
          data.page <= data.total_pages
        const hasValidCounts =
          data != null &&
          [
            data.total,
            data.enabled_count,
            data.manual_disabled_count,
            data.auto_disabled_count,
          ].every((value) => Number.isSafeInteger(value) && value >= 0) &&
          Number.isSafeInteger(
            data.enabled_count +
              data.manual_disabled_count +
              data.auto_disabled_count
          )
        const aggregateKeyCount =
          data == null
            ? 0
            : data.enabled_count +
              data.manual_disabled_count +
              data.auto_disabled_count
        const seenKeyIndexes = new Set<number>()
        const hasValidRows =
          hasValidKeys &&
          data != null &&
          hasValidPagination &&
          hasValidCounts &&
          keys.length <= data.page_size &&
          keys.every((key) => {
            if (typeof key !== 'object' || key === null || Array.isArray(key)) {
              return false
            }

            const prototype = Object.getPrototypeOf(key)
            if (prototype !== Object.prototype && prototype !== null) {
              return false
            }

            if (
              !Number.isSafeInteger(key.index) ||
              key.index < 0 ||
              key.index >= aggregateKeyCount ||
              seenKeyIndexes.has(key.index) ||
              (key.status !== 1 && key.status !== 2 && key.status !== 3) ||
              (key.reason !== undefined && typeof key.reason !== 'string') ||
              (key.disabled_time !== undefined &&
                (!Number.isSafeInteger(key.disabled_time) ||
                  key.disabled_time < 0))
            ) {
              return false
            }

            seenKeyIndexes.add(key.index)
            return true
          })

        if (
          response.success &&
          data &&
          hasValidKeys &&
          hasValidPagination &&
          hasValidCounts &&
          hasValidRows
        ) {
          setViewState({
            status: 'ready',
            request,
            data: {
              ...data,
              keys,
            },
          })
          return
        }

        toast.error(
          response.message ||
            translationRef.current('Failed to load key status')
        )
        setViewState({
          status: 'error',
          request,
          data: {
            ...EMPTY_MULTI_KEY_DATA,
            page,
            page_size: size,
          },
        })
      } catch (error: unknown) {
        if (
          !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
          !coordinator.isCurrentRequest(request)
        ) {
          return
        }

        toast.error(
          error instanceof Error
            ? error.message
            : translationRef.current('Failed to load key status')
        )
        setViewState({
          status: 'error',
          request,
          data: {
            ...EMPTY_MULTI_KEY_DATA,
            page,
            page_size: size,
          },
        })
      }
    },
    [coordinator]
  )

  useEffect(() => {
    if (!open || currentChannelId === null) {
      setViewState({
        status: 'idle',
        request: null,
        data: EMPTY_MULTI_KEY_DATA,
      })
      setStatusFilter(null)
      setConfirmAction(null)
      setPerformingSession(null)
      return
    }

    const session = coordinator.open(currentChannelId)
    setStatusFilter(null)
    setConfirmAction(null)
    setPerformingSession(null)
    void loadKeyStatus(session, 1, EMPTY_MULTI_KEY_DATA.page_size, null)

    return () => coordinator.close(session)
  }, [coordinator, currentChannelId, loadKeyStatus, open])

  const requestOwnsActiveDialog =
    open &&
    currentChannelId !== null &&
    viewState.request !== null &&
    isEntityDialogOwnerCurrent(
      liveOwnerRef.current,
      viewState.request.session.entity
    ) &&
    coordinator.isCurrentRequest(viewState.request) &&
    viewState.request.session.entity === currentChannelId
  const activeRequest = requestOwnsActiveDialog ? viewState.request : null
  const readyRequest =
    activeRequest !== null && viewState.status === 'ready'
      ? activeRequest
      : null
  const visibleData =
    readyRequest === null ? EMPTY_MULTI_KEY_DATA : viewState.data
  const visibleStatusFilter = requestOwnsActiveDialog ? statusFilter : null
  const isLoading =
    open &&
    currentChannelId !== null &&
    (!requestOwnsActiveDialog || viewState.status === 'loading')

  const visibleConfirmAction =
    confirmAction !== null &&
    readyRequest !== null &&
    confirmAction.session === readyRequest.session &&
    isEntityDialogOwnerCurrent(
      liveOwnerRef.current,
      confirmAction.session.entity
    ) &&
    coordinator.isCurrentSession(
      confirmAction.session,
      confirmAction.session.entity
    )
      ? confirmAction
      : null
  const isPerformingAction =
    visibleConfirmAction !== null &&
    performingSession === visibleConfirmAction.session &&
    isEntityDialogOwnerCurrent(
      liveOwnerRef.current,
      visibleConfirmAction.session.entity
    ) &&
    coordinator.isCurrentSession(
      visibleConfirmAction.session,
      visibleConfirmAction.session.entity
    )

  const handleRefresh = () => {
    if (viewState.request === null || !requestOwnsActiveDialog || isLoading) {
      return
    }

    const request = viewState.request
    const session = request.session
    if (
      !isEntityDialogOwnerCurrent(liveOwnerRef.current, session.entity) ||
      !coordinator.isCurrentRequest(request)
    ) {
      return
    }

    void loadKeyStatus(
      session,
      viewState.data.page,
      viewState.data.page_size,
      visibleStatusFilter
    )
  }

  const handleStatusFilterChange = (value: string) => {
    if (activeRequest === null) return
    const session = activeRequest.session
    if (
      !isEntityDialogOwnerCurrent(liveOwnerRef.current, session.entity) ||
      !coordinator.isCurrentRequest(activeRequest) ||
      !coordinator.isCurrentSession(session, session.entity)
    ) {
      return
    }

    const newFilter = value === 'all' ? null : Number.parseInt(value, 10)
    setStatusFilter(newFilter)
    void loadKeyStatus(session, 1, viewState.data.page_size, newFilter)
  }

  const handlePageChange = (newPage: number) => {
    if (readyRequest === null) return
    const session = readyRequest.session
    if (
      !isEntityDialogOwnerCurrent(liveOwnerRef.current, session.entity) ||
      !coordinator.isCurrentRequest(readyRequest) ||
      !coordinator.isCurrentSession(session, session.entity)
    ) {
      return
    }

    void loadKeyStatus(
      session,
      newPage,
      viewState.data.page_size,
      visibleStatusFilter
    )
  }

  const handleConfirmAction = (action: MultiKeyConfirmAction) => {
    if (readyRequest === null) return
    const session = readyRequest.session
    if (
      !isEntityDialogOwnerCurrent(liveOwnerRef.current, session.entity) ||
      !coordinator.isCurrentRequest(readyRequest) ||
      !coordinator.isCurrentSession(session, session.entity)
    ) {
      return
    }

    setConfirmAction({ ...action, session })
  }

  const handleConfirmOpenChange = (nextOpen: boolean) => {
    if (
      nextOpen ||
      visibleConfirmAction === null ||
      readyRequest === null ||
      visibleConfirmAction.session !== readyRequest.session
    ) {
      return
    }

    const session = visibleConfirmAction.session
    if (
      !isEntityDialogOwnerCurrent(liveOwnerRef.current, session.entity) ||
      !coordinator.isCurrentSession(session, session.entity) ||
      !coordinator.isCurrentRequest(readyRequest)
    ) {
      return
    }

    setConfirmAction((current) =>
      current?.session === session ? null : current
    )
  }

  const performAction = async () => {
    const action = visibleConfirmAction
    const readyRequestAtDispatch = readyRequest
    if (
      action === null ||
      readyRequestAtDispatch === null ||
      action.session !== readyRequestAtDispatch.session
    ) {
      return
    }

    const session = action.session
    const owner = session.entity
    if (
      !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
      !coordinator.isCurrentSession(session, owner) ||
      !coordinator.isCurrentRequest(readyRequestAtDispatch)
    ) {
      return
    }

    if (
      !canEditSensitive &&
      (action.type === 'delete' || action.type === 'delete-disabled')
    ) {
      setConfirmAction((current) =>
        current?.session === session ? null : current
      )
      return
    }

    const type = action.type
    const keyIndex = action.keyIndex
    const pageAtDispatch = viewState.data.page
    const pageSizeAtDispatch = viewState.data.page_size
    const statusAtDispatch = visibleStatusFilter
    const isRowAction =
      type === 'enable' || type === 'disable' || type === 'delete'
    if (isRowAction && keyIndex === undefined) return

    setPerformingSession(session)
    let reloadPage: number | null = null
    try {
      let response: { success: boolean; message?: string } | undefined
      if (type === 'enable' && keyIndex !== undefined) {
        response = await enableMultiKey(owner, keyIndex)
      } else if (type === 'disable' && keyIndex !== undefined) {
        response = await disableMultiKey(owner, keyIndex)
      } else if (type === 'delete' && keyIndex !== undefined) {
        response = await deleteMultiKey(owner, keyIndex)
      } else if (type === 'enable-all') {
        response = await enableAllMultiKeys(owner)
      } else if (type === 'disable-all') {
        response = await disableAllMultiKeys(owner)
      } else if (type === 'delete-disabled') {
        response = await deleteDisabledMultiKeys(owner)
      }

      if (
        !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
        !coordinator.isCurrentSession(session, owner) ||
        !coordinator.isCurrentRequest(readyRequestAtDispatch)
      ) {
        return
      }

      if (response?.success) {
        toast.success(
          response.message || translationRef.current('Operation successful')
        )
        void queryClient.invalidateQueries({
          queryKey: channelsQueryKeys.lists(),
        })
        const isBulkAction = type.includes('all') || type === 'delete-disabled'
        reloadPage = isBulkAction ? 1 : pageAtDispatch
      } else {
        toast.error(
          response?.message || translationRef.current('Operation failed')
        )
      }
    } catch (error: unknown) {
      if (
        !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
        !coordinator.isCurrentSession(session, owner) ||
        !coordinator.isCurrentRequest(readyRequestAtDispatch)
      ) {
        return
      }

      toast.error(
        error instanceof Error
          ? error.message
          : translationRef.current('Operation failed')
      )
    } finally {
      if (
        isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) &&
        coordinator.isCurrentSession(session, owner) &&
        coordinator.isCurrentRequest(readyRequestAtDispatch)
      ) {
        setPerformingSession((current) =>
          current === session ? null : current
        )
        setConfirmAction((current) =>
          current?.session === session ? null : current
        )
      }
    }

    if (
      reloadPage !== null &&
      isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) &&
      coordinator.isCurrentSession(session, owner) &&
      coordinator.isCurrentRequest(readyRequestAtDispatch)
    ) {
      void loadKeyStatus(
        session,
        reloadPage,
        pageSizeAtDispatch,
        statusAtDispatch
      )
    }
  }

  const handleClose = (nextOpen: boolean) => {
    if (nextOpen) {
      onOpenChange(true)
      return
    }

    const session = viewState.request?.session
    if (session) coordinator.close(session)
    setViewState({
      status: 'idle',
      request: null,
      data: EMPTY_MULTI_KEY_DATA,
    })
    setStatusFilter(null)
    setConfirmAction(null)
    setPerformingSession(null)
    onOpenChange(false)
  }

  const renderStatusBadge = (status: number) => {
    const config = getMultiKeyStatusConfig(status)
    return (
      <StatusBadge
        label={t(config.label)}
        variant={config.variant}
        showDot
        copyable={false}
      />
    )
  }

  const formatKeyTimestamp = (timestamp?: number) => {
    if (!timestamp) return '-'
    return formatTimestamp(timestamp)
  }

  let tableContent: ReactNode
  if (isLoading) {
    tableContent = (
      <div className='flex items-center justify-center py-12'>
        <Loader2 className='text-muted-foreground h-8 w-8 animate-spin' />
      </div>
    )
  } else if (visibleData.keys.length === 0) {
    tableContent = (
      <div className='text-muted-foreground py-12 text-center'>
        {t('No keys found')}
      </div>
    )
  } else {
    tableContent = (
      <StaticDataTable
        className='rounded-none border-0'
        tableClassName='min-w-[800px]'
        data={visibleData.keys}
        getRowKey={(key) => key.index}
        columns={[
          {
            id: 'index',
            header: t('Index'),
            className: 'w-20',
            cellClassName: 'font-mono text-sm',
            cell: (key) => `#${key.index + 1}`,
          },
          {
            id: 'status',
            header: t('Status'),
            className: 'w-32',
            cell: (key) => renderStatusBadge(key.status),
          },
          {
            id: 'reason',
            header: t('Disabled Reason'),
            className: 'min-w-[200px]',
            cellClassName: 'max-w-xs truncate text-sm',
            cell: (key) => key.reason || '-',
          },
          {
            id: 'disabled-time',
            header: t('Disabled Time'),
            className: 'w-44',
            cellClassName: 'text-muted-foreground text-sm',
            cell: (key) => formatKeyTimestamp(key.disabled_time),
          },
          {
            id: 'actions',
            header: t('Actions'),
            className: 'text-right',
            cell: (key) => (
              <MultiKeyTableRowActions
                keyIndex={key.index}
                status={key.status}
                canDelete={canEditSensitive}
                onAction={handleConfirmAction}
              />
            ),
          },
        ]}
      />
    )
  }

  if (!currentRow) return null

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={handleClose}
        title={
          <>
            {t('Multi-Key Management')}
            <StatusBadge
              label={currentRow.name}
              variant='neutral'
              copyable={false}
            />
            {currentRow.channel_info?.multi_key_mode && (
              <StatusBadge
                label={
                  currentRow.channel_info.multi_key_mode === 'random'
                    ? t('Random')
                    : t('Polling')
                }
                variant='neutral'
                copyable={false}
              />
            )}
          </>
        }
        description={t(
          'Manage multi-key status and configuration for this channel'
        )}
        contentClassName='flex max-h-[90vh] max-w-5xl flex-col'
        titleClassName='flex items-center gap-2'
        contentHeight='min(72vh, 720px)'
        bodyClassName='space-y-4'
      >
        <div className='flex min-h-0 flex-1 flex-col space-y-4 overflow-hidden'>
          {/* Statistics */}
          <div className='grid shrink-0 grid-cols-3 gap-3'>
            <StatisticsCard
              label={t('Enabled')}
              count={visibleData.enabled_count}
              total={visibleData.total}
            />
            <StatisticsCard
              label={t('Manual Disabled')}
              count={visibleData.manual_disabled_count}
              total={visibleData.total}
            />
            <StatisticsCard
              label={t('Auto Disabled')}
              count={visibleData.auto_disabled_count}
              total={visibleData.total}
            />
          </div>

          <Separator className='shrink-0' />

          {/* Toolbar */}
          <div className='flex shrink-0 items-center justify-between'>
            <Select
              items={MULTI_KEY_FILTER_OPTIONS.map((option) => ({
                value: option.value,
                label: t(option.label),
              }))}
              value={
                visibleStatusFilter === null
                  ? 'all'
                  : visibleStatusFilter.toString()
              }
              onValueChange={(v) => v !== null && handleStatusFilterChange(v)}
              disabled={activeRequest === null}
            >
              <SelectTrigger className='w-40'>
                <SelectValue placeholder={t('All Status')} />
              </SelectTrigger>
              <SelectContent alignItemWithTrigger={false}>
                <SelectGroup>
                  {MULTI_KEY_FILTER_OPTIONS.map((option) => (
                    <SelectItem key={option.value} value={option.value}>
                      {t(option.label)}
                    </SelectItem>
                  ))}
                </SelectGroup>
              </SelectContent>
            </Select>

            <div className='flex items-center gap-2'>
              <Button
                variant='outline'
                size='sm'
                onClick={handleRefresh}
                disabled={
                  isLoading ||
                  viewState.request === null ||
                  !requestOwnsActiveDialog
                }
              >
                <RefreshCw className='h-4 w-4' />
              </Button>

              {visibleData.manual_disabled_count +
                visibleData.auto_disabled_count >
                0 && (
                <Button
                  variant='default'
                  size='sm'
                  onClick={() => handleConfirmAction({ type: 'enable-all' })}
                >
                  <Power className='mr-2 h-4 w-4' />
                  {t('Enable All')}
                </Button>
              )}

              {visibleData.enabled_count > 0 && (
                <Button
                  variant='destructive'
                  size='sm'
                  onClick={() => handleConfirmAction({ type: 'disable-all' })}
                >
                  <PowerOff className='mr-2 h-4 w-4' />
                  {t('Disable All')}
                </Button>
              )}

              {visibleData.auto_disabled_count > 0 && (
                <Button
                  variant='destructive'
                  size='sm'
                  onClick={() => {
                    if (!canEditSensitive) return
                    handleConfirmAction({ type: 'delete-disabled' })
                  }}
                  disabled={!canEditSensitive}
                  title={
                    canEditSensitive
                      ? undefined
                      : t('No permission to perform this action')
                  }
                >
                  <Trash2 className='mr-2 h-4 w-4' />
                  {t('Delete Auto-Disabled')}
                </Button>
              )}
            </div>
          </div>
          {!canEditSensitive && (
            <p className='text-muted-foreground text-xs'>
              {t('No permission to perform this action')}
            </p>
          )}

          {/* Table */}
          <div className='min-h-0 flex-1 overflow-auto rounded-md border'>
            {tableContent}
          </div>

          {/* Pagination */}
          {visibleData.total_pages > 1 && (
            <div className='flex shrink-0 items-center justify-between'>
              <div className='text-muted-foreground text-sm'>
                {t('Page {{current}} of {{total}}', {
                  current: visibleData.page,
                  total: visibleData.total_pages,
                })}
              </div>
              <div className='flex gap-2'>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => handlePageChange(visibleData.page - 1)}
                  disabled={visibleData.page === 1 || isLoading}
                >
                  {t('Previous')}
                </Button>
                <Button
                  variant='outline'
                  size='sm'
                  onClick={() => handlePageChange(visibleData.page + 1)}
                  disabled={
                    visibleData.page >= visibleData.total_pages || isLoading
                  }
                >
                  {t('Next')}
                </Button>
              </div>
            </div>
          )}
        </div>
      </Dialog>

      {/* Confirmation Dialog */}
      <ConfirmDialog
        open={visibleConfirmAction !== null}
        onOpenChange={handleConfirmOpenChange}
        title={t('Confirm Action')}
        desc={t(getMultiKeyConfirmMessage(visibleConfirmAction))}
        destructive={isDestructiveAction(visibleConfirmAction)}
        isLoading={isPerformingAction}
        handleConfirm={performAction}
      />
    </>
  )
}
