import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { i18n } from '@lingui/core'
import { I18nProvider } from '@lingui/react'
import { AddNodeButton, ADD_NODE_MENU_ITEMS } from './AddNodeButton'

const renderOpenMenu = (onSelectNodeType = vi.fn()) => {
  render(
    <I18nProvider i18n={i18n}>
      <AddNodeButton
        onSelectNodeType={onSelectNodeType}
        hasListSelected
        isMenuOpen
        onMenuToggle={vi.fn()}
      />
    </I18nProvider>
  )
  return { onSelectNodeType }
}

describe('AddNodeButton menu', () => {
  it('offers the in-app / push message right after the webhook', () => {
    const keys = ADD_NODE_MENU_ITEMS.map((item) => item.key)

    expect(keys).toContain('novu_message')
    expect(keys.indexOf('novu_message')).toBe(keys.indexOf('webhook') + 1)
  })

  it('shows it by its name, not by its key', () => {
    renderOpenMenu()

    expect(screen.getByText('In-app / push message')).toBeInTheDocument()
    expect(screen.queryByText('novu_message')).not.toBeInTheDocument()
  })

  it('adds a novu_message node when it is picked', () => {
    const { onSelectNodeType } = renderOpenMenu()

    fireEvent.click(screen.getByText('In-app / push message'))

    expect(onSelectNodeType).toHaveBeenCalledWith('novu_message')
  })

  it('does not need a list, as the email node does', () => {
    render(
      <I18nProvider i18n={i18n}>
        <AddNodeButton
          onSelectNodeType={vi.fn()}
          hasListSelected={false}
          isMenuOpen
          onMenuToggle={vi.fn()}
        />
      </I18nProvider>
    )

    const item = screen.getByText('In-app / push message').closest('button')
    expect(item).not.toHaveClass('cursor-not-allowed')
  })
})
