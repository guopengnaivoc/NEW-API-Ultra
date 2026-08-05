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
import { useNavigate } from '@tanstack/react-router'
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import type { z } from 'zod'

import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Spinner } from '@/components/ui/spinner'

import { resetPassword } from '../api'
import { AuthLayout } from '../auth-layout'
import { resetPasswordFormSchema } from '../constants'

export function ResetPasswordConfirm() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const [isComplete, setIsComplete] = useState(false)
  const form = useForm<z.infer<typeof resetPasswordFormSchema>>({
    resolver: zodResolver(resetPasswordFormSchema),
    defaultValues: {
      token: '',
      password: '',
      confirmPassword: '',
    },
  })

  async function onSubmit(
    data: z.infer<typeof resetPasswordFormSchema>
  ): Promise<void> {
    try {
      const response = await resetPassword({
        token: data.token,
        password: data.password,
      })
      if (!response.success) return

      form.reset()
      setIsComplete(true)
    } catch {
      // Errors handled by global interceptor
    }
  }

  return (
    <AuthLayout>
      <div className='flex w-full flex-col gap-8'>
        <div className='flex flex-col gap-2'>
          <h2 className='text-center text-2xl font-semibold tracking-tight sm:text-left'>
            {t('Reset password')}
          </h2>
          <p className='text-muted-foreground text-left text-sm sm:text-base'>
            {isComplete
              ? t('auth.resetPasswordConfirm.success')
              : t('auth.resetPasswordConfirm.description')}
          </p>
        </div>

        {isComplete ? (
          <Button
            className='w-full'
            onClick={() => navigate({ to: '/sign-in', replace: true })}
          >
            {t('auth.resetPasswordConfirm.backToLogin')}
          </Button>
        ) : (
          <Form {...form}>
            <form onSubmit={form.handleSubmit(onSubmit)} className='grid gap-4'>
              <FormField
                control={form.control}
                name='token'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Verification code')}</FormLabel>
                    <FormControl>
                      <Input
                        autoComplete='one-time-code'
                        autoCapitalize='none'
                        autoCorrect='off'
                        spellCheck={false}
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='password'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('New password')}</FormLabel>
                    <FormControl>
                      <Input
                        type='password'
                        autoComplete='new-password'
                        placeholder={t('Enter password (8-20 characters)')}
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <FormField
                control={form.control}
                name='confirmPassword'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Confirm password')}</FormLabel>
                    <FormControl>
                      <Input
                        type='password'
                        autoComplete='new-password'
                        placeholder={t('Please confirm your password')}
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <Button
                type='submit'
                className='w-full'
                disabled={form.formState.isSubmitting}
              >
                {form.formState.isSubmitting ? (
                  <Spinner data-icon='inline-start' />
                ) : null}
                {t('auth.resetPasswordConfirm.confirm')}
              </Button>

              <Button
                type='button'
                variant='link'
                className='w-full'
                onClick={() => navigate({ to: '/sign-in', replace: true })}
              >
                {t('Back to login')}
              </Button>
            </form>
          </Form>
        )}
      </div>
    </AuthLayout>
  )
}
