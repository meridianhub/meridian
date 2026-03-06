declare module 'virtual:cookbook-data' {
  import type { CookbookRegistry } from '@/features/cookbook/hooks/use-cookbook'
  const data: CookbookRegistry
  export default data
}
