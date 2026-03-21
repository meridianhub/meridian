import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { renderHook, act } from '@testing-library/react'
import { useManifestValidate } from './use-manifest-validate'

// Mock the protobuf create function
vi.mock('@bufbuild/protobuf', () => ({
  create: (_schema: unknown, fields: Record<string, unknown>) => fields,
}))

const mockApplyManifest = vi.fn()

vi.mock('@/api/context', () => ({
  useApiClients: () => ({
    manifestApplier: {
      applyManifest: mockApplyManifest,
    },
  }),
}))

// Stub the generated proto imports
vi.mock('@/api/gen/meridian/control_plane/v1/apply_manifest_service_pb', () => ({
  ApplyManifestStatus: { VALIDATION_FAILED: 3, DRY_RUN: 1 },
  StepResultStatus: { SUCCESS: 1 },
  ValidationErrorSchema: { typeName: 'ValidationError' },
}))

describe('useManifestValidate', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('returns initial state', () => {
    const { result } = renderHook(() => useManifestValidate())

    expect(result.current.isValidating).toBe(false)
    expect(result.current.result).toBeNull()
    expect(typeof result.current.validate).toBe('function')
  })

  it('debounces validation calls by 500ms', async () => {
    const manifest = { yaml: 'test' } as never
    mockApplyManifest.mockResolvedValue({
      validationErrors: [],
      stepResults: [],
      status: 1,
    })

    const { result } = renderHook(() => useManifestValidate())

    act(() => {
      result.current.validate(manifest)
    })

    // Not yet called - still within debounce period
    expect(mockApplyManifest).not.toHaveBeenCalled()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(500)
    })

    // Now it should be called
    expect(mockApplyManifest).toHaveBeenCalledTimes(1)
    expect(mockApplyManifest).toHaveBeenCalledWith(
      { manifest, dryRun: true, force: false, appliedBy: '' },
      expect.objectContaining({ signal: expect.any(AbortSignal) }),
    )
  })

  it('cancels previous debounce when validate is called again', async () => {
    const manifest1 = { yaml: 'first' } as never
    const manifest2 = { yaml: 'second' } as never
    mockApplyManifest.mockResolvedValue({
      validationErrors: [],
      stepResults: [],
      status: 1,
    })

    const { result } = renderHook(() => useManifestValidate())

    act(() => {
      result.current.validate(manifest1)
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(300)
    })

    act(() => {
      result.current.validate(manifest2)
    })

    // Advance past original debounce but not new one
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300)
    })

    // First call should have been cancelled
    expect(mockApplyManifest).not.toHaveBeenCalled()

    await act(async () => {
      await vi.advanceTimersByTimeAsync(200)
    })

    expect(mockApplyManifest).toHaveBeenCalledTimes(1)
    expect(mockApplyManifest).toHaveBeenCalledWith(
      expect.objectContaining({ manifest: manifest2 }),
      expect.any(Object),
    )
  })

  it('separates errors and warnings from response', async () => {
    mockApplyManifest.mockResolvedValue({
      validationErrors: [
        { severity: 'ERROR', code: 'E1', message: 'err1' },
        { severity: 'WARNING', code: 'W1', message: 'warn1' },
        { severity: 'ERROR', code: 'E2', message: 'err2' },
      ],
      stepResults: [],
      status: 1,
    })

    const { result } = renderHook(() => useManifestValidate())

    act(() => {
      result.current.validate({} as never)
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(500)
    })

    expect(result.current.result).not.toBeNull()
    expect(result.current.result!.errors).toHaveLength(2)
    expect(result.current.result!.warnings).toHaveLength(1)
  })

  it('extracts step-level errors when VALIDATION_FAILED but no structured errors', async () => {
    mockApplyManifest.mockResolvedValue({
      validationErrors: [],
      stepResults: [
        { stepName: 'step1', status: 0, message: 'Step failed' },
        { stepName: 'step2', status: 1, message: 'OK' },
      ],
      status: 3, // VALIDATION_FAILED
    })

    const { result } = renderHook(() => useManifestValidate())

    act(() => {
      result.current.validate({} as never)
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(500)
    })

    expect(result.current.result).not.toBeNull()
    expect(result.current.result!.errors).toHaveLength(1)
    expect(result.current.result!.errors[0]).toEqual(
      expect.objectContaining({ code: 'step1', message: 'Step failed' }),
    )
  })

  it('handles network errors', async () => {
    mockApplyManifest.mockRejectedValue(new Error('Network down'))

    const { result } = renderHook(() => useManifestValidate())

    act(() => {
      result.current.validate({} as never)
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(500)
    })

    expect(result.current.result).not.toBeNull()
    expect(result.current.result!.errors).toHaveLength(1)
    expect(result.current.result!.errors[0]).toEqual(
      expect.objectContaining({ code: 'NETWORK_ERROR', message: 'Network down' }),
    )
    expect(result.current.result!.warnings).toHaveLength(0)
  })

  it('handles non-Error rejection', async () => {
    mockApplyManifest.mockRejectedValue('string error')

    const { result } = renderHook(() => useManifestValidate())

    act(() => {
      result.current.validate({} as never)
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(500)
    })

    expect(result.current.result).not.toBeNull()
    expect(result.current.result!.errors[0]).toEqual(
      expect.objectContaining({ message: 'Validation request failed' }),
    )
  })

  it('ignores AbortError', async () => {
    const abortError = new DOMException('Aborted', 'AbortError')
    mockApplyManifest.mockRejectedValue(abortError)

    const { result } = renderHook(() => useManifestValidate())

    act(() => {
      result.current.validate({} as never)
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(600)
    })

    // result should remain null since abort errors are ignored
    expect(result.current.result).toBeNull()
  })

  it('sets isValidating during request', async () => {
    let resolvePromise: (value: unknown) => void
    mockApplyManifest.mockReturnValue(
      new Promise((resolve) => {
        resolvePromise = resolve
      }),
    )

    const { result } = renderHook(() => useManifestValidate())

    act(() => {
      result.current.validate({} as never)
    })

    await act(async () => {
      await vi.advanceTimersByTimeAsync(500)
    })

    expect(result.current.isValidating).toBe(true)

    await act(async () => {
      resolvePromise!({
        validationErrors: [],
        stepResults: [],
        status: 1,
      })
    })

    expect(result.current.isValidating).toBe(false)
  })
})
