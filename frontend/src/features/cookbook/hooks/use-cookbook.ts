export interface CookbookItem {
  name: string
  type: 'registry:pattern' | 'registry:ui'
  title: string
  description?: string
  registryDependencies?: string[]
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

import cookbookData from 'virtual:cookbook-data'

export function useCookbook(): { items: CookbookItem[]; isLoading: boolean } {
  return { items: cookbookData.items, isLoading: false }
}
