import type { Plugin } from 'vite'
import { readFileSync, existsSync } from 'node:fs'
import { resolve } from 'node:path'

const VIRTUAL_MODULE_ID = 'virtual:cookbook-data'
const RESOLVED_VIRTUAL_MODULE_ID = '\0' + VIRTUAL_MODULE_ID

interface RegistryEntry {
  name: string
  type: string
  title: string
  description?: string
  categories?: string[]
}

interface CookbookBundlerOptions {
  /** Path to the cookbook directory. Defaults to `../cookbook` relative to frontend/. */
  cookbookDir?: string
}

function loadCookbookData(cookbookDir: string): string {
  const registryPath = resolve(cookbookDir, 'registry.json')
  if (!existsSync(registryPath)) {
    console.warn(`[cookbook-bundler] registry.json not found at ${registryPath}`)
    return JSON.stringify({ name: 'meridian-cookbook', items: [] })
  }

  const registry = JSON.parse(readFileSync(registryPath, 'utf-8')) as {
    name: string
    items: RegistryEntry[]
  }

  const items = registry.items.map((entry) => {
    const isPattern = entry.type === 'registry:pattern'
    const subdir = isPattern ? 'patterns' : 'ui'
    const metaFile = isPattern ? 'pattern.json' : 'component.json'
    const metaPath = resolve(cookbookDir, subdir, entry.name, metaFile)

    if (!existsSync(metaPath)) {
      return entry
    }

    const detail = JSON.parse(readFileSync(metaPath, 'utf-8'))
    const filesWithContent = (detail.files ?? []).map((file: { path: string; [k: string]: unknown }) => {
      const filePath = resolve(cookbookDir, file.path)
      if (!filePath.startsWith(cookbookDir + '/')) {
        console.warn(`[cookbook-bundler] Skipping file outside cookbook directory: ${file.path}`)
        return file
      }
      if (!existsSync(filePath)) return file
      return { ...file, content: readFileSync(filePath, 'utf-8') }
    })
    return {
      ...entry,
      description: detail.description ?? entry.description,
      categories: detail.categories ?? entry.categories,
      meta: detail.meta,
      files: filesWithContent,
    }
  })

  return JSON.stringify({ name: registry.name, items })
}

export default function cookbookBundler(
  options: CookbookBundlerOptions = {},
): Plugin {
  const cookbookDir = options.cookbookDir ?? resolve(__dirname, '../../cookbook')

  return {
    name: 'cookbook-bundler',

    resolveId(id: string) {
      if (id === VIRTUAL_MODULE_ID) {
        return RESOLVED_VIRTUAL_MODULE_ID
      }
    },

    load(id: string) {
      if (id === RESOLVED_VIRTUAL_MODULE_ID) {
        const data = loadCookbookData(cookbookDir)
        return `export default ${data};`
      }
    },

    configureServer(server) {
      server.watcher.add(cookbookDir)
      server.watcher.on('change', (file) => {
        if (file.startsWith(cookbookDir)) {
          const mod = server.moduleGraph.getModuleById(
            RESOLVED_VIRTUAL_MODULE_ID,
          )
          if (mod) {
            server.moduleGraph.invalidateModule(mod)
            server.ws.send({ type: 'full-reload' })
          }
        }
      })
    },
  }
}
