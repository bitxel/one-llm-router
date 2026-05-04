import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'

import { buildJsonPreviewFromText, JsonPreview } from './JsonPreview'

describe('JsonPreview', () => {
  it('highlights JSON tokens and renders escaped newlines as visual line breaks', () => {
    const preview = buildJsonPreviewFromText(
      '{"text":"hello\\n\\nworld","count":2,"ok":true,"empty":null}',
      'empty',
    )

    const { container } = render(<JsonPreview preview={preview} />)

    expect(container.querySelectorAll('[data-json-token="key"]').length).toBeGreaterThan(0)
    expect(container.querySelectorAll('[data-json-token="string"]').length).toBeGreaterThan(0)
    expect(container.querySelectorAll('[data-json-token="number"]').length).toBeGreaterThan(0)
    expect(container.querySelectorAll('[data-json-token="boolean"]').length).toBeGreaterThan(0)
    expect(container.querySelectorAll('[data-json-token="null"]').length).toBeGreaterThan(0)
    expect(screen.getAllByTestId('json-string-newline')).toHaveLength(2)
    expect(container.querySelector('[data-json-token="string"]')).toHaveTextContent('helloworld')
    expect(preview.formatted).toContain('"text": "hello\\n\\nworld"')
    expect(screen.getByRole('tab', { name: 'Beautify' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByRole('tab', { name: 'Raw' })).toHaveAttribute('aria-selected', 'false')
  })

  it('switches between beautified and raw views and copies the active view', async () => {
    const user = userEvent.setup()
    const onCopy = vi.fn()
    const preview = buildJsonPreviewFromText('{"text":"hello\\n\\nworld"}', 'empty')

    render(<JsonPreview preview={preview} copyLabel="Copy response" onCopy={onCopy} />)

    await user.click(screen.getByRole('button', { name: 'Copy response' }))
    expect(onCopy).toHaveBeenLastCalledWith('{\n  "text": "hello\\n\\nworld"\n}')

    await user.click(screen.getByRole('tab', { name: 'Raw' }))
    expect(screen.getByRole('tab', { name: 'Raw' })).toHaveAttribute('aria-selected', 'true')
    expect(screen.queryByTestId('json-string-newline')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Copy response' }))
    expect(onCopy).toHaveBeenLastCalledWith('{"text":"hello\\n\\nworld"}')
  })

  it('keeps literal backslash-n text on one line', () => {
    const preview = buildJsonPreviewFromText('{"text":"literal \\\\n marker"}', 'empty')

    const { container } = render(<JsonPreview preview={preview} />)

    expect(screen.queryByTestId('json-string-newline')).not.toBeInTheDocument()
    expect(container.querySelector('[data-json-token="string"]')).toHaveTextContent(
      'literal \\\\n marker',
    )
  })

  it('renders non-JSON bodies as plain text', () => {
    const preview = buildJsonPreviewFromText('not json', 'empty')

    const { container } = render(<JsonPreview preview={preview} />)

    expect(screen.getByText('not json')).toBeInTheDocument()
    expect(container.querySelector('[data-json-token]')).not.toBeInTheDocument()
  })
})
