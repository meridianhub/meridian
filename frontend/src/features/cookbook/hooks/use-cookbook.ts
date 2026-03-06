export interface CookbookItem {
  name: string
  type: 'registry:pattern' | 'registry:ui'
  title: string
  description?: string
  categories?: string[]
  meta?: PatternMeta | ComponentMeta
  files?: CookbookFile[]
}

export interface PatternMeta {
  complexity?: number
  design_pattern?: string
  industries?: string[]
  provides?: {
    instruments?: string[]
    account_types?: string[]
    sagas?: string[]
    valuation_rules?: string[]
    triggers?: string[]
  }
  requires?: {
    instruments?: string[]
    market_data?: string[]
  }
  composes_with?: string[]
  conflicts_with?: string[]
  extends?: string[]
}

export interface ComponentMeta {
  feature_module?: string
  used_by?: string[]
  configurable?: boolean
}

export interface CookbookFile {
  path: string
  type?: string
  content?: string
}

export interface CookbookRegistry {
  name: string
  items: CookbookItem[]
}

// Hook stub - returns empty data initially. Task 2 (Vite plugin) will provide real data.
export function useCookbook(): { items: CookbookItem[]; isLoading: boolean } {
  // TODO: Replace with Vite plugin bundled data (Task 2)
  return { items: [], isLoading: false }
}
