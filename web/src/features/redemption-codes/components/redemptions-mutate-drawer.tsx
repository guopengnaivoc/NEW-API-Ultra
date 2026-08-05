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
import { zodResolver } from '@hookform/resolvers/zod'
import {
  type FormEvent,
  useEffect,
  useEffectEvent,
  useRef,
  useState,
} from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { DateTimePicker } from '@/components/datetime-picker'
import {
  SideDrawerSection,
  sideDrawerContentClassName,
  sideDrawerFooterClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetClose,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { getCurrencyDisplay, getCurrencyLabel } from '@/lib/currency'
import {
  createEntityDialogSessionCoordinator,
  isEntityDialogOwnerCurrent,
  type EntityDialogLiveOwner,
  type EntityDialogSession,
} from '@/lib/entity-dialog-session'
import { formatQuota, parseQuotaFromDollars } from '@/lib/format'
import { addTimeToDate } from '@/lib/time'

import { createRedemption, updateRedemption, getRedemption } from '../api'
import { SUCCESS_MESSAGES } from '../constants'
import {
  getRedemptionFormSchema,
  type RedemptionFormValues,
  REDEMPTION_FORM_DEFAULT_VALUES,
  transformFormDataToPayload,
  transformRedemptionToFormDefaults,
} from '../lib'
import type { Redemption } from '../types'
import { useRedemptions } from './redemptions-provider'

type RedemptionsMutateDrawerProps = {
  open: boolean
  onOpenChange: (open: boolean) => void
  currentRow?: Redemption
}

type RedemptionTarget = number | 'create'

type RedemptionLoadState =
  | { status: 'idle'; session: null }
  | {
      status: 'loading' | 'ready' | 'error'
      session: EntityDialogSession<RedemptionTarget>
    }

export function RedemptionsMutateDrawer({
  open,
  onOpenChange,
  currentRow,
}: RedemptionsMutateDrawerProps) {
  const { t } = useTranslation()
  const isUpdate = !!currentRow
  const { triggerRefresh } = useRedemptions()
  const [isSubmitting, setIsSubmitting] = useState(false)
  const [loadState, setLoadState] = useState<RedemptionLoadState>({
    status: 'idle',
    session: null,
  })
  const coordinatorRef = useRef<ReturnType<
    typeof createEntityDialogSessionCoordinator<RedemptionTarget>
  > | null>(null)
  if (coordinatorRef.current === null) {
    coordinatorRef.current =
      createEntityDialogSessionCoordinator<RedemptionTarget>()
  }
  const coordinator = coordinatorRef.current

  const form = useForm<RedemptionFormValues>({
    resolver: zodResolver(getRedemptionFormSchema(t)),
    defaultValues: REDEMPTION_FORM_DEFAULT_VALUES,
  })

  const target: RedemptionTarget = currentRow?.id ?? 'create'
  const liveOwnerRef = useRef<EntityDialogLiveOwner<RedemptionTarget>>({
    open,
    entity: target,
  })
  liveOwnerRef.current = { open, entity: target }
  const isReady =
    open &&
    loadState.status === 'ready' &&
    loadState.session !== null &&
    isEntityDialogOwnerCurrent(
      liveOwnerRef.current,
      loadState.session.entity
    ) &&
    coordinator.isCurrentSession(loadState.session, target)
  const showLoadFailure = useEffectEvent(() => {
    toast.error(t('Failed to load redemption codes'))
  })

  useEffect(() => {
    if (!open) {
      setLoadState({ status: 'idle', session: null })
      setIsSubmitting(false)
      form.reset(REDEMPTION_FORM_DEFAULT_VALUES)
      return
    }

    const session = coordinator.open(target)
    form.reset(REDEMPTION_FORM_DEFAULT_VALUES)
    setIsSubmitting(false)

    if (target === 'create') {
      setLoadState({ status: 'ready', session })
      return () => coordinator.close(session)
    }

    setLoadState({ status: 'loading', session })
    const requestedId = target
    const request = coordinator.startRequest(session)
    if (request === null) {
      setLoadState({ status: 'error', session })
      return () => coordinator.close(session)
    }

    void getRedemption(requestedId, {
      signal: request.signal,
      disableDuplicate: true,
      skipBusinessError: true,
      skipErrorHandler: true,
    })
      .then((result) => {
        if (
          !isEntityDialogOwnerCurrent(
            liveOwnerRef.current,
            request.session.entity
          ) ||
          !coordinator.isCurrentRequest(request)
        ) {
          return
        }

        if (result.success && result.data && result.data.id === requestedId) {
          form.reset(transformRedemptionToFormDefaults(result.data))
          setLoadState({ status: 'ready', session })
          return
        }

        showLoadFailure()
        setLoadState({ status: 'error', session })
      })
      .catch(() => {
        if (
          !isEntityDialogOwnerCurrent(
            liveOwnerRef.current,
            request.session.entity
          ) ||
          !coordinator.isCurrentRequest(request)
        ) {
          return
        }

        showLoadFailure()
        setLoadState({ status: 'error', session })
      })

    return () => coordinator.close(session)
  }, [coordinator, form, open, target])

  const onSubmit = async (data: RedemptionFormValues) => {
    if (!isReady || loadState.session === null) return

    const submissionSession = loadState.session
    const owner = submissionSession.entity
    setIsSubmitting(true)
    try {
      const basePayload = transformFormDataToPayload(data)

      if (owner !== 'create') {
        const result = await updateRedemption({
          ...basePayload,
          id: owner,
        })
        if (
          !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
          !coordinator.isCurrentSession(submissionSession, owner)
        ) {
          return
        }
        if (!result.success) return
      } else {
        const result = await createRedemption(basePayload)
        if (
          !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
          !coordinator.isCurrentSession(submissionSession, owner)
        ) {
          return
        }
        if (!result.success) return

        const count = result.data?.length || 0

        toast.success(
          count > 1
            ? t('Successfully created {{count}} redemption codes', {
                count,
              })
            : t(SUCCESS_MESSAGES.REDEMPTION_CREATED)
        )
      }
    } finally {
      if (
        isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) &&
        coordinator.isCurrentSession(submissionSession, owner)
      ) {
        setIsSubmitting(false)
      }
    }

    if (
      !isEntityDialogOwnerCurrent(liveOwnerRef.current, owner) ||
      !coordinator.isCurrentSession(submissionSession, owner)
    ) {
      return
    }

    if (owner !== 'create') {
      toast.success(t(SUCCESS_MESSAGES.REDEMPTION_UPDATED))
    }
    triggerRefresh()
    form.reset(REDEMPTION_FORM_DEFAULT_VALUES)

    coordinator.close(submissionSession)
    onOpenChange(false)
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    if (!isUpdate) {
      const name = form.getValues('name')
      if (!name?.trim()) {
        const quota = parseQuotaFromDollars(form.getValues('quota_dollars'))
        form.setValue('name', formatQuota(quota), { shouldValidate: true })
      }
    }

    void form.handleSubmit(onSubmit)(event)
  }

  const handleSetExpiry = (months: number, days: number, hours: number) => {
    const newDate = addTimeToDate(months, days, hours)
    form.setValue('expired_time', newDate)
  }

  const handleOpenChange = (nextOpen: boolean) => {
    if (nextOpen) {
      onOpenChange(true)
      return
    }

    const session = loadState.session
    if (session !== null) coordinator.close(session)
    form.reset(REDEMPTION_FORM_DEFAULT_VALUES)
    onOpenChange(false)
  }

  const { meta: currencyMeta } = getCurrencyDisplay()
  const currencyLabel = getCurrencyLabel()
  const tokensOnly = currencyMeta.kind === 'tokens'
  const quotaLabel = t('Quota ({{currency}})', { currency: currencyLabel })
  const quotaPlaceholder = tokensOnly
    ? t('Enter quota in tokens')
    : t('Enter quota in {{currency}}', { currency: currencyLabel })

  return (
    <Sheet open={open} onOpenChange={handleOpenChange}>
      <SheetContent className={sideDrawerContentClassName('sm:max-w-[600px]')}>
        <SheetHeader className={sideDrawerHeaderClassName()}>
          <SheetTitle>
            {isUpdate
              ? t('Update Redemption Code')
              : t('Create Redemption Code')}
          </SheetTitle>
          <SheetDescription>
            {isUpdate
              ? t('Update the redemption code by providing necessary info.')
              : t(
                  'Add new redemption code(s) by providing necessary info.'
                )}{' '}
            {t("Click save when you're done.")}
          </SheetDescription>
        </SheetHeader>
        <Form {...form}>
          <form
            id='redemption-form'
            onSubmit={handleSubmit}
            className={sideDrawerFormClassName()}
          >
            <SideDrawerSection>
              <FormField
                control={form.control}
                name='name'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Name')}</FormLabel>
                    <FormControl>
                      <Input {...field} placeholder={t('Enter a name')} />
                    </FormControl>
                    <FormDescription>
                      {t('Name for this redemption code (1-20 characters)')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='quota_dollars'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{quotaLabel}</FormLabel>
                    <FormControl>
                      <Input
                        {...field}
                        type='number'
                        step={tokensOnly ? 1 : 0.01}
                        placeholder={quotaPlaceholder}
                        onChange={(e) =>
                          field.onChange(Number.parseFloat(e.target.value) || 0)
                        }
                      />
                    </FormControl>
                    <FormDescription>
                      {tokensOnly
                        ? t('Enter the quota amount in tokens')
                        : t('Enter the quota amount in {{currency}}', {
                            currency: currencyLabel,
                          })}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='expired_time'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Expiration Time')}</FormLabel>
                    <div className='flex flex-col gap-2'>
                      <FormControl>
                        <DateTimePicker
                          value={field.value}
                          onChange={field.onChange}
                          placeholder={t('Never expires')}
                        />
                      </FormControl>
                      <div className='grid grid-cols-4 gap-1.5 sm:flex sm:gap-2'>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(0, 0, 0)}
                        >
                          {t('Never')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(1, 0, 0)}
                        >
                          {t('1M')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(0, 7, 0)}
                        >
                          {t('1W')}
                        </Button>
                        <Button
                          type='button'
                          variant='outline'
                          size='sm'
                          onClick={() => handleSetExpiry(0, 1, 0)}
                        >
                          {t('1 Day')}
                        </Button>
                      </div>
                    </div>
                    <FormDescription>
                      {t('Leave empty for never expires')}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {!isUpdate && (
                <FormField
                  control={form.control}
                  name='count'
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Quantity')}</FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          type='number'
                          min='1'
                          max='100'
                          placeholder={t('Number of codes to create')}
                          onChange={(e) =>
                            field.onChange(
                              Number.parseInt(e.target.value, 10) || 1
                            )
                          }
                        />
                      </FormControl>
                      <FormDescription>
                        {t('Create multiple redemption codes at once (1-100)')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              )}
            </SideDrawerSection>
          </form>
        </Form>
        <SheetFooter className={sideDrawerFooterClassName()}>
          <SheetClose render={<Button variant='outline' />}>
            {t('Close')}
          </SheetClose>
          <Button
            form='redemption-form'
            type='submit'
            disabled={!isReady || isSubmitting}
          >
            {isSubmitting ? t('Saving...') : t('Save changes')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
