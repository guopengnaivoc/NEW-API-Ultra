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
import { Loader2 } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  SecureVerificationDialog,
  useSecureVerification,
  type VerificationMethod,
} from '@/features/auth/secure-verification'
import { setup2FA, enable2FA } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { TwoFASetupData } from '../../types'

// ============================================================================
// Two-FA Setup Dialog Component
// ============================================================================

interface TwoFASetupDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}

export function TwoFASetupDialog({
  open,
  onOpenChange,
  onSuccess,
}: TwoFASetupDialogProps) {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(false)
  const [initializing, setInitializing] = useState(false)
  const [step, setStep] = useState(0)
  const [setupData, setSetupData] = useState<TwoFASetupData | null>(null)
  const [code, setCode] = useState('')
  const currentUserId = useAuthStore((state) => state.auth.user?.id)
  const stepLabels = [
    t('Scan QR Code'),
    t('Save Backup Codes'),
    t('Verify Setup'),
  ]
  const {
    open: verificationOpen,
    setOpen: setVerificationOpen,
    methods: verificationMethods,
    state: verificationState,
    startVerification,
    executeVerification,
    cancel: cancelVerification,
    setCode: setVerificationCode,
    switchMethod,
  } = useSecureVerification({
    onSuccess: (result) => {
      const response = result as {
        success: boolean
        data?: TwoFASetupData
      }
      if (response.data) {
        setSetupData(response.data)
        setStep(0)
      }
      setInitializing(false)
    },
  })

  const handleSetup = useCallback(async () => {
    if (!currentUserId) {
      toast.error(t('Verification is not configured properly'))
      onOpenChange(false)
      return
    }

    try {
      setInitializing(true)
      const started = await startVerification(
        async (proofToken?: string) => {
          const response = await setup2FA(proofToken)
          if (!response.success || !response.data) {
            throw new Error(response.message || t('Failed to setup 2FA'))
          }
          return response
        },
        {
          scope: 'twofa.setup',
          target: String(currentUserId),
          title: t('Security verification'),
        }
      )
      if (!started) {
        setInitializing(false)
        onOpenChange(false)
      }
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Setup 2FA error:', error)
      toast.error(t('Failed to setup 2FA'))
      onOpenChange(false)
      setInitializing(false)
    }
  }, [currentUserId, onOpenChange, startVerification, t])

  const handleVerification = async (
    method: VerificationMethod,
    credential?: string
  ) => {
    try {
      await executeVerification(method, credential)
    } catch {
      // The verification hook displays the actionable error.
    }
  }

  const handleVerificationCancel = () => {
    cancelVerification()
    setInitializing(false)
    onOpenChange(false)
  }

  const handleEnable = async () => {
    if (!code) {
      toast.error(t('Please enter the verification code'))
      return
    }

    try {
      setLoading(true)
      const response = await enable2FA(code)

      if (response.success) {
        toast.success(t('Two-factor authentication enabled successfully!'))
        onOpenChange(false)
        onSuccess()
        // Reset
        setStep(0)
        setCode('')
        setSetupData(null)
      } else {
        toast.error(response.message || t('Failed to enable 2FA'))
      }
    } catch {
      toast.error(t('Failed to enable 2FA'))
    } finally {
      setLoading(false)
    }
  }

  const handleOpenChange = (open: boolean) => {
    if (!loading && !initializing) {
      if (!open) {
        setStep(0)
        setCode('')
        setSetupData(null)
      }
      onOpenChange(open)
    }
  }

  // Initialize when dialog opens
  useEffect(() => {
    if (open && !setupData && !initializing) {
      handleSetup()
    }
  }, [open, setupData, initializing, handleSetup])

  return (
    <>
      <Dialog
        open={open}
        onOpenChange={handleOpenChange}
        title={t('Setup Two-Factor Authentication')}
        description={
          <>
            {t('Step')}
            {step + 1}
            {t('of 3:')}
            {stepLabels[step]}
          </>
        }
        contentClassName='sm:max-w-lg'
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <>
            {step > 0 && (
              <Button
                variant='outline'
                onClick={() => setStep(step - 1)}
                disabled={initializing || loading}
              >
                {t('Back')}
              </Button>
            )}
            {step < 2 ? (
              <Button
                onClick={() => setStep(step + 1)}
                disabled={initializing || !setupData}
              >
                {t('Next')}
              </Button>
            ) : (
              <Button
                onClick={handleEnable}
                disabled={initializing || loading || !code}
              >
                {loading && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
                {loading ? t('Enabling...') : t('Enable 2FA')}
              </Button>
            )}
          </>
        }
      >
        <div className='space-y-4 py-4'>
          {initializing && (
            <div className='flex flex-col items-center justify-center gap-3 py-8'>
              <div className='border-primary h-8 w-8 animate-spin rounded-full border-4 border-t-transparent' />
              <div className='text-muted-foreground text-sm'>
                {t('Setting up 2FA...')}
              </div>
            </div>
          )}
          {!initializing && !setupData && (
            <div className='flex justify-center py-8'>
              <div className='text-muted-foreground'>
                {t('Failed to load setup data')}
              </div>
            </div>
          )}
          {!initializing && setupData && (
            <>
              {/* Step 0: QR Code */}
              {step === 0 && (
                <div className='space-y-4'>
                  <p className='text-muted-foreground text-sm'>
                    {t(
                      'Scan this QR code with your authenticator app (Google Authenticator, Microsoft Authenticator, etc.)'
                    )}
                  </p>
                  <div className='flex justify-center rounded-lg bg-white p-4'>
                    <QRCodeSVG value={setupData.qr_code_data} size={200} />
                  </div>
                  <div className='bg-muted rounded-lg p-3'>
                    <div className='flex items-center justify-between'>
                      <div>
                        <p className='text-muted-foreground text-xs'>
                          {t('Or enter this key manually:')}
                        </p>
                        <code className='font-mono text-sm'>
                          {setupData.secret}
                        </code>
                      </div>
                      <CopyButton
                        value={setupData.secret}
                        variant='ghost'
                        tooltip={t('Copy secret key')}
                        aria-label={t('Copy secret key')}
                      />
                    </div>
                  </div>
                </div>
              )}

              {/* Step 1: Backup Codes */}
              {step === 1 && (
                <div className='space-y-4'>
                  <Alert>
                    <AlertDescription>
                      {t(
                        'Save these backup codes in a safe place. Each code can only be used once.'
                      )}
                    </AlertDescription>
                  </Alert>
                  <div className='rounded-lg border p-4'>
                    <div className='grid grid-cols-2 gap-2'>
                      {setupData.backup_codes.map((code) => (
                        <div
                          key={code}
                          className='bg-muted rounded-md p-2 text-center font-mono text-sm'
                        >
                          {code}
                        </div>
                      ))}
                    </div>
                  </div>
                  <CopyButton
                    value={setupData.backup_codes.join('\n')}
                    variant='outline'
                    size='default'
                    className='w-full'
                    iconClassName='mr-2 size-4'
                    tooltip={t('Copy all backup codes')}
                    aria-label={t('Copy all backup codes')}
                  >
                    {t('Copy All Codes')}
                  </CopyButton>
                </div>
              )}

              {/* Step 2: Verify */}
              {step === 2 && (
                <div className='space-y-4'>
                  <div className='space-y-2'>
                    <Label htmlFor='code'>{t('Verification Code')}</Label>
                    <Input
                      id='code'
                      value={code}
                      onChange={(e) => setCode(e.target.value)}
                      placeholder={t('Enter 6-digit code')}
                      maxLength={6}
                      disabled={loading}
                    />
                    <p className='text-muted-foreground text-xs'>
                      {t('Enter the 6-digit code from your authenticator app')}
                    </p>
                  </div>
                </div>
              )}
            </>
          )}
        </div>
      </Dialog>
      <SecureVerificationDialog
        open={verificationOpen}
        onOpenChange={(nextOpen) => {
          if (nextOpen) {
            setVerificationOpen(true)
          } else {
            handleVerificationCancel()
          }
        }}
        methods={verificationMethods}
        state={verificationState}
        onVerify={handleVerification}
        onCancel={handleVerificationCancel}
        onCodeChange={setVerificationCode}
        onMethodChange={switchMethod}
      />
    </>
  )
}
