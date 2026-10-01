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

  it('collapses and expands all containers and toggles individual containers', async () => {
    const user = userEvent.setup()
    const preview = buildJsonPreviewFromText('{"outer":{"inner":1},"items":[2]}', 'empty')

    render(<JsonPreview preview={preview} />)

    await user.click(screen.getByRole('tab', { name: 'Collapse all' }))
    expect(screen.getByText('2 keys')).toBeInTheDocument()
    expect(screen.queryByText('"inner"')).not.toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: 'Expand all' }))
    expect(screen.getByText('"inner"')).toBeInTheDocument()

    await user.click(screen.getAllByRole('button', { name: 'Collapse JSON node' })[1])
    expect(screen.queryByText('"inner"')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Expand 1 keys' }))
    expect(screen.getByText('"inner"')).toBeInTheDocument()
  })

  it('keeps similarly spelled object paths independent', async () => {
    const user = userEvent.setup()
    render(
      <JsonPreview
        preview={buildJsonPreviewFromText('{"a.b":{"x":1},"a":{"b":{"y":2}}}', 'empty')}
      />,
    )

    const collapseButtons = screen.getAllByRole('button', { name: 'Collapse JSON node' })
    await user.click(collapseButtons[1])

    expect(screen.getByText('1 keys')).toBeInTheDocument()
    expect(screen.getByText('"y"')).toBeInTheDocument()
  })

  it('opens a larger overlay for the current payload', async () => {
    const user = userEvent.setup()
    const { container } = render(
      <JsonPreview preview={buildJsonPreviewFromText('{"hello":{"value":1}}', 'empty')} />,
    )

    await user.click(screen.getAllByRole('button', { name: 'Collapse JSON node' })[0])
    await user.click(screen.getByRole('button', { name: 'Expand JSON viewer' }))
    expect(screen.getByRole('dialog', { name: 'Expanded JSON viewer' })).toBeInTheDocument()
    expect(screen.getByRole('dialog').querySelector('[aria-label="Expand 1 keys"]')).toBeTruthy()

    await user.click(
      screen
        .getByRole('dialog')
        .querySelector('[role="tab"][aria-selected="false"]') as HTMLElement,
    )
    expect(screen.getByRole('dialog').querySelector('[aria-label="Expand 1 keys"]')).toBeNull()
    await user.click(screen.getAllByRole('button', { name: 'Close expanded JSON viewer' })[1])
    expect(screen.getByRole('tab', { name: 'Raw' })).toHaveAttribute('aria-selected', 'true')
    expect(container.querySelector('[aria-label="Expand 1 keys"]')).toBeNull()
  })

  it('traps focus in the expanded viewer and restores it when closed', async () => {
    const user = userEvent.setup()
    render(<JsonPreview preview={buildJsonPreviewFromText('{"hello":1}', 'empty')} />)

    const openButton = screen.getByRole('button', { name: 'Expand JSON viewer' })
    await user.click(openButton)
    expect(screen.getAllByRole('button', { name: 'Close expanded JSON viewer' })[1]).toHaveFocus()

    await user.keyboard('{Tab}')
    expect(screen.getByRole('dialog')).toContainElement(document.activeElement as HTMLElement)

    await user.keyboard('{Escape}')
    expect(openButton).toHaveFocus()
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
