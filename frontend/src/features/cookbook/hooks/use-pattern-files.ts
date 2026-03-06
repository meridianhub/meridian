import { useEffect, useState } from 'react'

interface PatternFilesResult {
  starlarkContent: string | null
  manifestContent: string | null
  isLoading: boolean
}

export function usePatternFiles(patternName: string | undefined): PatternFilesResult {
  const [starlarkContent, setStarlarkContent] = useState<string | null>(null)
  const [manifestContent, setManifestContent] = useState<string | null>(null)
  const [isLoading, setIsLoading] = useState(false)

  useEffect(() => {
    if (!patternName) return

    let cancelled = false
    setIsLoading(true)
    setStarlarkContent(null)
    setManifestContent(null)

    const fetchFile = async (path: string): Promise<string | null> => {
      try {
        const res = await fetch(path)
        if (!res.ok) return null
        return await res.text()
      } catch {
        return null
      }
    }

    Promise.all([
      fetchFile(`/cookbook/patterns/${patternName}/saga.star`),
      fetchFile(`/cookbook/patterns/${patternName}/manifest.yaml`),
    ]).then(([star, yaml]) => {
      if (cancelled) return
      setStarlarkContent(star)
      setManifestContent(yaml)
      setIsLoading(false)
    })

    return () => {
      cancelled = true
    }
  }, [patternName])

  return { starlarkContent, manifestContent, isLoading }
}
