import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import App from '@/App'

describe('App', () => {
  it('renders the operations console heading', () => {
    render(<App />)
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      'Meridian Operations Console',
    )
  })

  it('renders without crashing with QueryClientProvider', () => {
    const { container } = render(<App />)
    expect(container).toBeTruthy()
  })
})
