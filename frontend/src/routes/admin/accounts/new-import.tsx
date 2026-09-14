import { useQueryClient } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { FileJson, TextCursorInput } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { Canvas, Field, PanelCard, Stripe } from '@/components/neo'
import { ErrorBanner } from '@/components/shared/ErrorBanner'
import { Button } from '@/components/ui/button'
import { accountsImportAuthJson } from '@/generated/openapi'
import { i18n } from '@/i18n'
import { callAdmin } from '@/lib/router-api'

// Namespace-fixed translator; the language resolves at call time so
// module-scope helpers stay reactive to language switches.
const t = i18n.getFixedT(null, 'accounts')

import { invalidateAdminAccountQueries } from './query-keys'

interface ImportedAccount {
  id: number
  name: string
  provider: string
  auth_method: 'oauth_import'
  status: string
  email?: string | null
  plan_type?: string | null
  plan_type_label?: string | null
  chatgpt_account_id?: string | null
}

type ImportSource = 'file' | 'paste'

export function AdminAccountsNewImport() {
  // Subscribes this subtree to languageChanged so module-t() helpers re-render.
  useTranslation('accounts')
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [source, setSource] = useState<ImportSource>('file')
  const [file, setFile] = useState<File | null>(null)
  const [pastedJSON, setPastedJSON] = useState('')
  const [screenError, setScreenError] = useState<unknown>(null)
  const [isSubmitting, setIsSubmitting] = useState(false)

  async function handleSubmit() {
    const trimmedPastedJSON = pastedJSON.trim()
    if (source === 'file' && !file) {
      setScreenError(new Error(t('newImport.validation.fileRequired')))
      return
    }
    if (source === 'paste' && trimmedPastedJSON === '') {
      setScreenError(new Error(t('newImport.validation.pasteRequired')))
      return
    }

    setIsSubmitting(true)
    setScreenError(null)
    try {
      const authJSONText = (
        source === 'file' && file ? await readFileText(file) : trimmedPastedJSON
      ).trim()
      if (authJSONText === '') {
        setScreenError(
          new Error(
            source === 'file'
              ? t('newImport.validation.fileRequired')
              : t('newImport.validation.pasteRequired'),
          ),
        )
        return
      }
      const request = accountsImportAuthJson({
        body: { auth_json: authJSONText },
        headers: { 'Content-Type': 'application/json' },
      })
      const imported = (await callAdmin(request)) as unknown as { account: ImportedAccount }
      await invalidateAdminAccountQueries(queryClient, imported.account.id)
      toast.success(t('newImport.toasts.importSuccess'))
      await navigate({
        to: '/admin/accounts/$accountId',
        params: { accountId: String(imported.account.id) },
      })
    } catch (error) {
      setScreenError(error)
      toast.error(t('newImport.toasts.importFailed'))
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <Canvas variant="narrow">
      <Stripe eyebrow={t('newImport.eyebrow')}>{t('newImport.stripe')}</Stripe>
      <div className="mb-8 max-w-[70ch]">
        <h1 className="mb-3 max-w-[18ch] text-balance">
          {t('newImport.titleLead')} <strong>{t('newImport.titleStrong')}</strong>
        </h1>
        <p className="max-w-[64ch] text-[14px] leading-[1.7] text-[var(--text-dim)]">
          {t('newImport.intro')}
        </p>
      </div>

      {screenError ? <ErrorBanner error={screenError} className="mb-4" /> : null}

      <PanelCard title={t('newImport.panelTitle')} meta={t('newImport.panelMeta')}>
        <div className="space-y-4">
          <Field label={t('newImport.labels.source')}>
            <div className="inline-flex border border-[var(--line-2)] bg-[var(--bg-2)] p-[3px]">
              <Button
                type="button"
                variant={source === 'file' ? 'default' : 'ghost'}
                size="sm"
                onClick={() => setSource('file')}
                data-testid="import-source-file"
              >
                <FileJson aria-hidden="true" />
                {t('newImport.actions.fileMode')}
              </Button>
              <Button
                type="button"
                variant={source === 'paste' ? 'default' : 'ghost'}
                size="sm"
                onClick={() => setSource('paste')}
                data-testid="import-source-paste"
              >
                <TextCursorInput aria-hidden="true" />
                {t('newImport.actions.pasteMode')}
              </Button>
            </div>
          </Field>

          {source === 'file' ? (
            <Field label={t('newImport.labels.file')}>
              <div className="space-y-2">
                <input
                  type="file"
                  accept=".json,application/json"
                  data-testid="import-auth-json-input"
                  onChange={(event) => {
                    const nextFile = event.target.files?.[0] ?? null
                    setFile(nextFile)
                  }}
                  className="block w-full text-[13px] text-[var(--text)] file:mr-4 file:border file:border-[var(--line)] file:bg-[var(--panel-hi)] file:px-3 file:py-2 file:text-[12px] file:font-medium file:text-[var(--text)]"
                />
                <p className="text-[12.5px] leading-[1.6] text-[var(--text-dim)]">
                  {t('newImport.hints.file')}
                </p>
              </div>
            </Field>
          ) : (
            <Field label={t('newImport.labels.paste')}>
              <div className="space-y-2">
                <textarea
                  data-testid="import-auth-json-textarea"
                  value={pastedJSON}
                  onChange={(event) => setPastedJSON(event.target.value)}
                  spellCheck={false}
                  className="min-h-[260px] w-full resize-y border bg-[var(--bg-2)] px-[10px] py-[8px] font-mono text-[12.5px] leading-[1.65] text-[var(--text)] outline-none transition-colors placeholder:text-[var(--text-muted)] hover:border-[var(--line-3)] focus:border-[var(--accent)] focus-visible:[box-shadow:var(--focus)] disabled:cursor-not-allowed disabled:bg-[var(--panel-2)] disabled:text-[var(--text-muted)]"
                  style={{ borderColor: 'var(--line-2)', borderRadius: 2 }}
                />
                <p className="text-[12.5px] leading-[1.6] text-[var(--text-dim)]">
                  {t('newImport.hints.paste')}
                </p>
              </div>
            </Field>
          )}

          <Button
            onClick={() => {
              void handleSubmit()
            }}
            disabled={isSubmitting}
            data-testid="import-auth-json-submit"
          >
            {isSubmitting ? t('newImport.loading') : t('newImport.actions.upload')}
          </Button>
        </div>
      </PanelCard>
    </Canvas>
  )
}

async function readFileText(file: File): Promise<string> {
  if (typeof file.text === 'function') {
    return file.text()
  }
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(reader.error ?? new Error('failed to read auth.json file'))
    reader.onload = () => resolve(typeof reader.result === 'string' ? reader.result : '')
    reader.readAsText(file)
  })
}
