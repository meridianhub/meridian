import { useMemo } from 'react'
import yaml from 'js-yaml'
import type { CookbookItem } from './use-cookbook'

export interface StarlarkFile {
  name: string
  content: string
}

export interface PatternFilesState {
  starlarkFiles: StarlarkFile[]
  manifestContent: string | null
  hasSagas: boolean
  sagaTrigger: string | null
  isLoading: false
}

function isValidContent(content: string | undefined): content is string {
  if (!content) return false
  const trimmed = content.trimStart()
  return !trimmed.startsWith('<!DOCTYPE') && !trimmed.startsWith('<html')
}

interface ManifestSaga {
  name?: string
  trigger?: string
  filter?: string
}

function parseManifestSagas(manifestContent: string | null): ManifestSaga[] {
  if (!manifestContent) return []
  try {
    const doc = yaml.load(manifestContent) as Record<string, unknown> | null
    if (!doc || typeof doc !== 'object') return []
    const sagas = doc.sagas
    return Array.isArray(sagas) ? sagas : []
  } catch {
    return []
  }
}


export function usePatternFiles(item: CookbookItem | undefined): PatternFilesState {
  return useMemo(() => {
    const empty: PatternFilesState = { starlarkFiles: [], manifestContent: null, hasSagas: false, sagaTrigger: null, isLoading: false }
    if (!item || item.type !== 'registry:pattern') return empty

    const files = item.files ?? []

    const manifestFile = files.find((f) => f.path.endsWith('.yaml'))
    const manifestContent = isValidContent(manifestFile?.content) ? manifestFile!.content : null

    const starlarkFiles: StarlarkFile[] = files
      .filter((f) => f.path.endsWith('.star'))
      .map((f) => ({
        name: f.path.split('/').pop() ?? f.path,
        content: isValidContent(f.content) ? f.content : '',
      }))
      .filter((f) => f.content.length > 0)

    const manifestSagas = parseManifestSagas(manifestContent)
    const hasSagas = starlarkFiles.length > 0 || manifestSagas.length > 0
    const sagaTrigger = manifestSagas[0]?.trigger ?? null

    return { starlarkFiles, manifestContent, hasSagas, sagaTrigger, isLoading: false }
  }, [item])
}
