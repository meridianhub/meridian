import { describe, it, expect, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuCheckboxItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuSub,
  DropdownMenuSubTrigger,
  DropdownMenuSubContent,
  DropdownMenuGroup,
} from './dropdown-menu'

describe('DropdownMenu - closed state', () => {
  it('renders trigger but not content when closed', () => {
    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Item</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    expect(screen.getByText('Open')).toBeInTheDocument()
    expect(screen.queryByText('Item')).not.toBeInTheDocument()
  })

  it('trigger has data-slot attribute', () => {
    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Item</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    expect(screen.getByText('Open')).toHaveAttribute('data-slot', 'dropdown-menu-trigger')
  })
})

describe('DropdownMenu - open state', () => {
  it('shows content when open', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Item One</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      expect(screen.getByText('Item One')).toBeInTheDocument()
    })
  })

  it('content has correct data-slot attribute when open', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Item</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const content = document.querySelector('[data-slot="dropdown-menu-content"]')
      expect(content).toBeInTheDocument()
    })
  })

  it('renders menu items with correct data-slot', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Item A</DropdownMenuItem>
          <DropdownMenuItem>Item B</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const items = document.querySelectorAll('[data-slot="dropdown-menu-item"]')
      expect(items).toHaveLength(2)
    })
  })
})

describe('DropdownMenu - item selection', () => {
  it('calls onSelect when item is clicked', async () => {
    const user = userEvent.setup()
    const onSelect = vi.fn()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem onSelect={onSelect}>Click Me</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      expect(screen.getByText('Click Me')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Click Me'))

    expect(onSelect).toHaveBeenCalledOnce()
  })

  it('closes menu after item selection', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Select Me</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      expect(screen.getByText('Select Me')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Select Me'))

    await waitFor(() => {
      expect(screen.queryByText('Select Me')).not.toBeInTheDocument()
    })
  })
})

describe('DropdownMenu - keyboard navigation', () => {
  it('closes on Escape key', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Item</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      expect(screen.getByText('Item')).toBeInTheDocument()
    })

    await user.keyboard('{Escape}')

    await waitFor(() => {
      expect(screen.queryByText('Item')).not.toBeInTheDocument()
    })
  })

  it('opens menu via Enter key on trigger', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Item</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    screen.getByText('Open').focus()
    await user.keyboard('{Enter}')

    await waitFor(() => {
      expect(screen.getByText('Item')).toBeInTheDocument()
    })
  })
})

describe('DropdownMenu - checkbox item', () => {
  it('renders checkbox item with correct data-slot', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuCheckboxItem checked={false} onCheckedChange={vi.fn()}>
            Checkbox
          </DropdownMenuCheckboxItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const item = document.querySelector('[data-slot="dropdown-menu-checkbox-item"]')
      expect(item).toBeInTheDocument()
    })
  })

  it('calls onCheckedChange when toggled', async () => {
    const user = userEvent.setup()
    const onCheckedChange = vi.fn()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuCheckboxItem checked={false} onCheckedChange={onCheckedChange}>
            Toggle Me
          </DropdownMenuCheckboxItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      expect(screen.getByText('Toggle Me')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Toggle Me'))

    expect(onCheckedChange).toHaveBeenCalledWith(true)
  })

  it('shows check icon when checked', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuCheckboxItem checked={true} onCheckedChange={vi.fn()}>
            Checked Item
          </DropdownMenuCheckboxItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const item = document.querySelector('[data-slot="dropdown-menu-checkbox-item"]')
      expect(item).toHaveAttribute('data-state', 'checked')
    })
  })
})

describe('DropdownMenu - radio group', () => {
  it('renders radio items with correct data-slot', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuRadioGroup value="a" onValueChange={vi.fn()}>
            <DropdownMenuRadioItem value="a">Option A</DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="b">Option B</DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const items = document.querySelectorAll('[data-slot="dropdown-menu-radio-item"]')
      expect(items).toHaveLength(2)
    })
  })

  it('calls onValueChange when radio item is selected', async () => {
    const user = userEvent.setup()
    const onValueChange = vi.fn()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuRadioGroup value="a" onValueChange={onValueChange}>
            <DropdownMenuRadioItem value="a">Option A</DropdownMenuRadioItem>
            <DropdownMenuRadioItem value="b">Option B</DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      expect(screen.getByText('Option B')).toBeInTheDocument()
    })

    await user.click(screen.getByText('Option B'))

    expect(onValueChange).toHaveBeenCalledWith('b')
  })

  it('radio group has correct data-slot', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuRadioGroup value="" onValueChange={vi.fn()}>
            <DropdownMenuRadioItem value="x">X</DropdownMenuRadioItem>
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const group = document.querySelector('[data-slot="dropdown-menu-radio-group"]')
      expect(group).toBeInTheDocument()
    })
  })
})

describe('DropdownMenu - label and separator', () => {
  it('renders label with correct data-slot', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuLabel>Section Label</DropdownMenuLabel>
          <DropdownMenuItem>Item</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const label = document.querySelector('[data-slot="dropdown-menu-label"]')
      expect(label).toBeInTheDocument()
      expect(label).toHaveTextContent('Section Label')
    })
  })

  it('renders label with inset prop', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuLabel inset>Inset Label</DropdownMenuLabel>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const label = document.querySelector('[data-slot="dropdown-menu-label"]')
      expect(label).toHaveAttribute('data-inset', 'true')
    })
  })

  it('renders separator with correct data-slot', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Above</DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem>Below</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const separator = document.querySelector('[data-slot="dropdown-menu-separator"]')
      expect(separator).toBeInTheDocument()
    })
  })
})

describe('DropdownMenu - shortcut', () => {
  it('renders shortcut with correct data-slot', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>
            Action
            <DropdownMenuShortcut>⌘K</DropdownMenuShortcut>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const shortcut = document.querySelector('[data-slot="dropdown-menu-shortcut"]')
      expect(shortcut).toBeInTheDocument()
      expect(shortcut).toHaveTextContent('⌘K')
    })
  })
})

describe('DropdownMenu - variants', () => {
  it('item has data-variant=destructive for destructive variant', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem variant="destructive">Delete</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const item = document.querySelector('[data-slot="dropdown-menu-item"]')
      expect(item).toHaveAttribute('data-variant', 'destructive')
    })
  })

  it('item has data-inset for inset prop', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem inset>Inset Item</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const item = document.querySelector('[data-slot="dropdown-menu-item"]')
      expect(item).toHaveAttribute('data-inset', 'true')
    })
  })

  it('merges custom className on item', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem className="custom-class">Styled</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const item = document.querySelector('[data-slot="dropdown-menu-item"]')
      expect(item?.className).toContain('custom-class')
    })
  })
})

describe('DropdownMenu - submenu', () => {
  it('renders sub trigger with correct data-slot', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>More Options</DropdownMenuSubTrigger>
            <DropdownMenuSubContent>
              <DropdownMenuItem>Sub Item</DropdownMenuItem>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const trigger = document.querySelector('[data-slot="dropdown-menu-sub-trigger"]')
      expect(trigger).toBeInTheDocument()
      expect(trigger).toHaveTextContent('More Options')
    })
  })

  it('renders chevron icon in sub trigger', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>Sub Menu</DropdownMenuSubTrigger>
            <DropdownMenuSubContent>
              <DropdownMenuItem>Child</DropdownMenuItem>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const trigger = document.querySelector('[data-slot="dropdown-menu-sub-trigger"]')
      // ChevronRight icon is rendered as an svg
      const svg = trigger?.querySelector('svg')
      expect(svg).toBeInTheDocument()
    })
  })
})

describe('DropdownMenu - group', () => {
  it('renders group with correct data-slot', async () => {
    const user = userEvent.setup()

    render(
      <DropdownMenu>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuGroup>
            <DropdownMenuItem>Grouped Item</DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    await waitFor(() => {
      const group = document.querySelector('[data-slot="dropdown-menu-group"]')
      expect(group).toBeInTheDocument()
    })
  })
})

describe('DropdownMenu - controlled open state', () => {
  it('calls onOpenChange when opened', async () => {
    const user = userEvent.setup()
    const onOpenChange = vi.fn()

    render(
      <DropdownMenu onOpenChange={onOpenChange}>
        <DropdownMenuTrigger>Open</DropdownMenuTrigger>
        <DropdownMenuContent>
          <DropdownMenuItem>Item</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>,
    )

    await user.click(screen.getByText('Open'))

    expect(onOpenChange).toHaveBeenCalledWith(true)
  })
})
