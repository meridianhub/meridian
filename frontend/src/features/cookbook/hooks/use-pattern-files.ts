import { useEffect, useReducer } from 'react'

interface PatternFilesState {
  starlarkContent: string | null
  manifestContent: string | null
  isLoading: boolean
}

type PatternFilesAction =
  | { type: 'reset' }
  | { type: 'fetch_start' }
  | { type: 'fetch_done'; starlark: string | null; manifest: string | null }

const initialState: PatternFilesState = {
  starlarkContent: null,
  manifestContent: null,
  isLoading: false,
}

function reducer(_state: PatternFilesState, action: PatternFilesAction): PatternFilesState {
  switch (action.type) {
    case 'reset':
      return initialState
    case 'fetch_start':
      return { starlarkContent: null, manifestContent: null, isLoading: true }
    case 'fetch_done':
      return { starlarkContent: action.starlark, manifestContent: action.manifest, isLoading: false }
  }
}

export function usePatternFiles(patternName: string | undefined): PatternFilesState {
  const [state, dispatch] = useReducer(reducer, initialState)

  useEffect(() => {
    if (!patternName) {
      dispatch({ type: 'reset' })
      return
    }

    let cancelled = false
    dispatch({ type: 'fetch_start' })

    const fetchFile = async (path: string): Promise<string | null> => {
      try {
        const res = await fetch(path)
        if (!res.ok) return null
        return await res.text()
      } catch {
        return null
      }
    }

    const encoded = encodeURIComponent(patternName)
    Promise.all([
      fetchFile(`/cookbook/patterns/${encoded}/saga.star`),
      fetchFile(`/cookbook/patterns/${encoded}/manifest.yaml`),
    ]).then(([star, yaml]) => {
      if (cancelled) return
      dispatch({ type: 'fetch_done', starlark: star, manifest: yaml })
    })

    return () => {
      cancelled = true
    }
  }, [patternName])

  return state
}
