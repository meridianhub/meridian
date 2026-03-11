module.exports = {
  meta: {
    type: 'suggestion',
    docs: {
      description: 'Enforce PageShell and PageHeader usage in feature pages',
    },
    schema: [],
  },
  create(context) {
    const filename = (context.filename || context.getFilename()).replace(/\\/g, '/')
    // Only apply to feature page files
    if (!filename.includes('/features/') || !filename.includes('/pages/')) {
      return {}
    }
    // Only apply to .tsx files (skip .ts utility/types files)
    if (!filename.endsWith('.tsx')) {
      return {}
    }
    // Skip test files, dialog files, and tab components
    if (
      filename.includes('.test.') ||
      filename.includes('.spec.') ||
      filename.includes('dialog') ||
      filename.includes('/tabs/')
    ) {
      return {}
    }
    // Skip hub/dashboard pages (excluded from alignment per PRD)
    if (
      filename.includes('/dashboard/') ||
      filename.includes('/economy/') ||
      filename.includes('/cookbook/') ||
      filename.includes('/mcp-config/') ||
      filename.includes('/tenants/') ||
      filename.includes('/manifests/')
    ) {
      return {}
    }

    let hasPageShell = false
    let hasPageHeader = false

    return {
      ImportDeclaration(node) {
        const source = node.source.value
        const isPageImport =
          source.includes('@/shared') ||
          /^(\.\.?\/)*page-header$/.test(source) ||
          /^(\.\.?\/)*page-shell$/.test(source)
        if (isPageImport) {
          node.specifiers.forEach((spec) => {
            if (spec.imported && spec.imported.name === 'PageShell') hasPageShell = true
            if (spec.imported && spec.imported.name === 'PageHeader') hasPageHeader = true
          })
        }
      },
      'Program:exit'() {
        if (!hasPageShell) {
          context.report({
            loc: { line: 1, column: 0 },
            message: 'Feature pages must import PageShell from @/shared',
          })
        }
        if (!hasPageHeader) {
          context.report({
            loc: { line: 1, column: 0 },
            message: 'Feature pages must import PageHeader from @/shared',
          })
        }
      },
    }
  },
}
