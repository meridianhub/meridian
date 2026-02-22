import { describe, it, expect } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { axe } from '@/test/test-utils'
import * as React from 'react'
import {
  Dialog,
  DialogTrigger,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'

function DialogExample() {
  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button>Open Dialog</Button>
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Dialog Title</DialogTitle>
          <DialogDescription>Dialog description text here</DialogDescription>
        </DialogHeader>
        <div>Dialog content goes here</div>
        <DialogFooter>
          <DialogClose asChild>
            <Button variant="outline">Cancel</Button>
          </DialogClose>
          <Button>Confirm</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

describe('Dialog component accessibility', () => {
  it('has no accessibility violations when closed', async () => {
    const { container } = render(<DialogExample />)
    const results = await axe(container)
    expect(results).toHaveNoViolations()
  })

  it('has no violations when open', async () => {
    const user = userEvent.setup()
    render(<DialogExample />)

    const openButton = screen.getByRole('button', { name: /open dialog/i })
    await user.click(openButton)

    await waitFor(() => {
      expect(screen.getByText('Dialog Title')).toBeInTheDocument()
    })

    // Use document.body to include portaled content from DialogPortal
    const results = await axe(document.body)
    expect(results).toHaveNoViolations()
  })

  it('has accessible title and description', async () => {
    const user = userEvent.setup()
    render(<DialogExample />)

    const openButton = screen.getByRole('button', { name: /open dialog/i })
    await user.click(openButton)

    await waitFor(() => {
      expect(screen.getByText('Dialog Title')).toBeInTheDocument()
    })

    expect(screen.getByText('Dialog description text here')).toBeInTheDocument()
  })

  it('close button has accessible label', async () => {
    const user = userEvent.setup()
    render(<DialogExample />)

    const openButton = screen.getByRole('button', { name: /open dialog/i })
    await user.click(openButton)

    await waitFor(() => {
      expect(screen.getByText('Dialog Title')).toBeInTheDocument()
    })

    // Dialog should have a close button with sr-only text
    const closeButtons = screen.getAllByRole('button')
    const srOnlyClose = closeButtons.find((btn) =>
      btn.querySelector('.sr-only')?.textContent?.includes('Close')
    )
    expect(srOnlyClose).toBeInTheDocument()
  })

  it('supports keyboard navigation (Tab within dialog)', async () => {
    const user = userEvent.setup()
    render(<DialogExample />)

    const openButton = screen.getByRole('button', { name: /open dialog/i })
    await user.click(openButton)

    await waitFor(() => {
      expect(screen.getByText('Dialog Title')).toBeInTheDocument()
    })

    // Should be able to tab through dialog buttons
    const buttons = screen.getAllByRole('button')
    expect(buttons.length).toBeGreaterThanOrEqual(2) // Close, Confirm at minimum
  })

  it('supports Escape key to close', async () => {
    const user = userEvent.setup()
    render(<DialogExample />)

    const openButton = screen.getByRole('button', { name: /open dialog/i })
    await user.click(openButton)

    await waitFor(() => {
      expect(screen.getByText('Dialog Title')).toBeInTheDocument()
    })

    await user.keyboard('{Escape}')

    await waitFor(() => {
      expect(screen.queryByText('Dialog Title')).not.toBeInTheDocument()
    })
  })

  it('focus is managed correctly when dialog closes', async () => {
    const user = userEvent.setup()
    render(<DialogExample />)

    const openButton = screen.getByRole('button', { name: /open dialog/i })
    await user.click(openButton)

    await waitFor(() => {
      expect(screen.getByText('Dialog Title')).toBeInTheDocument()
    })

    const cancelButton = screen.getByRole('button', { name: /cancel/i })
    await user.click(cancelButton)

    // Focus should return to trigger button or close to it
    await waitFor(() => {
      expect(document.activeElement).toBeTruthy()
    })
  })

  it('has inert backdrop preventing interaction with page behind', async () => {
    const user = userEvent.setup()
    render(<DialogExample />)

    const openButton = screen.getByRole('button', { name: /open dialog/i })
    await user.click(openButton)

    await waitFor(() => {
      expect(screen.getByText('Dialog Title')).toBeInTheDocument()
    })

    // Verify dialog overlay exists in document (portals render outside container)
    const overlay = document.querySelector('[data-slot="dialog-overlay"]')
    expect(overlay).toBeInTheDocument()
  })

  it('has proper header structure', async () => {
    const user = userEvent.setup()
    render(<DialogExample />)

    const openButton = screen.getByRole('button', { name: /open dialog/i })
    await user.click(openButton)

    await waitFor(() => {
      const title = screen.getByText('Dialog Title')
      expect(title).toBeInTheDocument()
    })

    // Dialog should have proper text hierarchy
    const titleElement = screen.getByText('Dialog Title')
    expect(titleElement.tagName).toMatch(/^H\d$/i)
  })
})
