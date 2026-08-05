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
import type { ComponentProps } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Input } from '@/components/ui/input'

import {
  VERTEX_SERVICE_ACCOUNT_MAX_BYTES,
  VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES,
  VertexServiceAccountError,
  processVertexServiceAccountFiles,
} from '../../lib/vertex-service-account'

type VertexServiceAccountFilePickerProps = Omit<
  ComponentProps<typeof Input>,
  'accept' | 'multiple' | 'onChange' | 'type'
> & {
  batch: boolean
  onValueChange: (value: string) => void
}

export function VertexServiceAccountFilePicker({
  batch,
  onValueChange,
  ...inputProps
}: VertexServiceAccountFilePickerProps) {
  const { t } = useTranslation()

  return (
    <Input
      {...inputProps}
      type='file'
      accept='.json,application/json'
      multiple={batch}
      onChange={async (event) => {
        const fileList = event.target.files
        const files = fileList ? [...fileList] : []
        event.target.value = ''

        try {
          const result = await processVertexServiceAccountFiles(files, {
            batch,
          })
          onValueChange(result.value)
          toast.success(
            t('Parsed {{count}} service account file(s)', {
              count: result.count,
            })
          )
        } catch (error) {
          if (!(error instanceof VertexServiceAccountError)) {
            toast.error(t('Failed to process service account file(s)'))
            return
          }

          switch (error.code) {
            case 'empty_selection':
              toast.info(t('Please upload key file(s)'))
              return
            case 'too_many_keys':
              toast.error(
                t('You can upload at most {{count}} service account file(s)', {
                  count: error.limit,
                })
              )
              return
            case 'key_too_large':
              toast.error(
                t(
                  'Service account file {{name}} exceeds the {{size}} KiB limit',
                  {
                    name: error.fileName,
                    size: VERTEX_SERVICE_ACCOUNT_MAX_BYTES / 1024,
                  }
                )
              )
              return
            case 'total_too_large':
              toast.error(
                t('Service account files exceed the {{size}} MiB total limit', {
                  size: VERTEX_SERVICE_ACCOUNT_MAX_TOTAL_BYTES / 1024 / 1024,
                })
              )
              return
            case 'read_failed':
              toast.error(
                t('Failed to read service account file: {{name}}', {
                  name: error.fileName,
                })
              )
              return
            case 'invalid_json':
              toast.error(
                t('Failed to parse JSON file: {{name}}', {
                  name: error.fileName,
                })
              )
              return
            case 'invalid_schema':
              toast.error(
                t(
                  'Service account file {{name}} is not a valid Google service account key',
                  {
                    name: error.fileName,
                  }
                )
              )
          }
        }
      }}
    />
  )
}
