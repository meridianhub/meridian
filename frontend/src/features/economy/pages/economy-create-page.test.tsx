import { describe, it, expect, vi, beforeEach } from 'vitest'
import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { renderWithProviders } from '@/test/test-utils'
import { createTenantUserToken } from '@/test/jwt-helpers'
import { EconomyCreatePage } from './economy-create-page'

vi.mock('@/api/context', () => ({
  useApiClients: vi.fn(),
  ApiClientProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}))

// Mock react-router-dom navigate
const mockNavigate = vi.fn()
vi.mock('react-router-dom', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>()
  return { ...actual, useNavigate: () => mockNavigate }
})

function renderPage() {
  return renderWithProviders(
    <MemoryRouter>
      <EconomyCreatePage />
    </MemoryRouter>,
    { initialToken: createTenantUserToken() },
  )
}

describe('EconomyCreatePage', () => {
  beforeEach(() => {
    mockNavigate.mockReset()
  })

  it('renders page title and subtitle', () => {
    renderPage()
    expect(screen.getByText('Create Your Economy')).toBeInTheDocument()
    expect(screen.getByText(/Describe your business or start with a blank template/i)).toBeInTheDocument()
  })

  it('renders textarea for business description', () => {
    renderPage()
    expect(screen.getByRole('textbox')).toBeInTheDocument()
  })

  it('renders three option cards', () => {
    renderPage()
    expect(screen.getAllByText('Build My Economy').length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText("I'm Feeling Lucky").length).toBeGreaterThanOrEqual(1)
    expect(screen.getAllByText('Start from Scratch').length).toBeGreaterThanOrEqual(1)
  })

  it('navigates to /economy/edit when Start from Scratch is clicked', async () => {
    renderPage()
    const scratchButton = screen.getByRole('button', { name: /start from scratch/i })
    await userEvent.click(scratchButton)
    expect(mockNavigate).toHaveBeenCalledWith('/economy/edit')
  })

  it('Start from Scratch is always enabled regardless of prompt', () => {
    renderPage()
    const scratchButton = screen.getByRole('button', { name: /start from scratch/i })
    expect(scratchButton).not.toBeDisabled()
  })

  it('Build My Economy and I\'m Feeling Lucky are disabled when prompt is empty', () => {
    renderPage()
    const buildButton = screen.getByRole('button', { name: /build my economy/i })
    const luckyButton = screen.getByRole('button', { name: /i'm feeling lucky/i })
    expect(buildButton).toBeDisabled()
    expect(luckyButton).toBeDisabled()
  })

  it('Build My Economy and I\'m Feeling Lucky remain disabled (generator unavailable) even with prompt', async () => {
    renderPage()
    const textarea = screen.getByRole('textbox')
    await userEvent.type(textarea, 'An energy trading platform')

    // Generator not wired up in clients — should remain disabled
    const buildButton = screen.getByRole('button', { name: /build my economy/i })
    const luckyButton = screen.getByRole('button', { name: /i'm feeling lucky/i })
    expect(buildButton).toBeDisabled()
    expect(luckyButton).toBeDisabled()
  })
})
