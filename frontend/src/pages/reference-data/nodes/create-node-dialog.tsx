import * as React from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { useApiClients } from '@/api/context'
import { handleConnectError } from '@/lib/error-handling'
import { KeyValueEditor } from './key-value-editor'

// Pattern: starts with uppercase letter, then uppercase letters, digits, underscore, dot, or hyphen
const CODE_PATTERN = /^[A-Z][A-Z0-9_.-]*$/

interface FormData {
  code: string
  displayName: string
  nodeType: string
  parentNodeId: string
  description: string
  attributes: Record<string, string>
}

interface FormErrors {
  code?: string
  displayName?: string
  nodeType?: string
  general?: string
}

export interface CreateNodeDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Pre-select parent when adding child */
  defaultParentId?: string
}

function emptyForm(defaultParentId?: string): FormData {
  return {
    code: '',
    displayName: '',
    nodeType: '',
    parentNodeId: defaultParentId ?? '',
    description: '',
    attributes: {},
  }
}

function validate(formData: FormData): FormErrors {
  const errors: FormErrors = {}
  const code = formData.code.trim()
  if (!code) {
    errors.code = 'Code is required'
  } else if (code.length < 2) {
    errors.code = 'Code must be at least 2 characters'
  } else if (code.length > 100) {
    errors.code = 'Code must be at most 100 characters'
  } else if (!CODE_PATTERN.test(code)) {
    errors.code = 'Code must start with an uppercase letter and contain only uppercase letters, digits, underscores, dots, or hyphens'
  }
  const displayName = formData.displayName.trim()
  if (!displayName) {
    errors.displayName = 'Display name is required'
  } else if (displayName.length > 255) {
    errors.displayName = 'Display name must be at most 255 characters'
  }
  return errors
}

export function CreateNodeDialog({ open, onOpenChange, defaultParentId }: CreateNodeDialogProps) {
  const clients = useApiClients()
  const queryClient = useQueryClient()
  const [formData, setFormData] = React.useState<FormData>(() => emptyForm(defaultParentId))
  const [errors, setErrors] = React.useState<FormErrors>({})

  // Reset form when dialog opens/closes or defaultParentId changes
  React.useEffect(() => {
    if (!open) {
      setFormData(emptyForm(defaultParentId))
      setErrors({})
    } else {
      setFormData(emptyForm(defaultParentId))
    }
  }, [open, defaultParentId])

  // Fetch root nodes for parent selection
  const { data: rootsData } = useQuery({
    queryKey: ['node-roots', ''],
    queryFn: async () => {
      const result = await clients.node.getChildren({ parentId: '', activeOnly: true })
      return result.nodes ?? []
    },
    enabled: open,
    staleTime: 30_000,
  })

  const roots = rootsData ?? []

  const mutation = useMutation({
    mutationFn: async () => {
      // User-provided extra attributes are merged first; code, displayName, and
      // description always win to prevent the KeyValueEditor from overriding them.
      const attrs: Record<string, unknown> = {
        ...formData.attributes,
        code: formData.code.trim(),
        displayName: formData.displayName.trim(),
      }
      if (formData.description.trim()) {
        attrs.description = formData.description.trim()
      }

      return clients.node.createNode({
        nodeType: formData.nodeType.trim() || undefined,
        parentId: formData.parentNodeId.trim() || undefined,
        attributes: { fields: Object.fromEntries(
          Object.entries(attrs).map(([k, v]) => [k, { kind: { case: 'stringValue', value: String(v) } }])
        ) },
      })
    },
    onSuccess: (_result) => {
      // Invalidate root nodes and the parent's children
      void queryClient.invalidateQueries({ queryKey: ['node-roots'] })
      if (formData.parentNodeId.trim()) {
        void queryClient.invalidateQueries({
          queryKey: ['node-children', formData.parentNodeId.trim()],
        })
      }
      onOpenChange(false)
    },
    onError: (error: unknown) => {
      const result = handleConnectError(error)
      setErrors({ general: result.message })
    },
  })

  function handleChange<K extends keyof FormData>(field: K, value: FormData[K]) {
    setFormData((prev) => ({ ...prev, [field]: value }))
    if (field in errors && errors[field as keyof FormErrors]) {
      setErrors((prev) => ({ ...prev, [field]: undefined }))
    }
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault()
    const validationErrors = validate(formData)
    if (Object.keys(validationErrors).length > 0) {
      setErrors(validationErrors)
      return
    }
    setErrors({})
    mutation.mutate()
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Create Node</DialogTitle>
          <DialogDescription>
            Create a new reference data node. Nodes form hierarchical trees for region, zone, meter, and other classifications.
          </DialogDescription>
        </DialogHeader>

        <form onSubmit={(e) => void handleSubmit(e)} id="create-node-form">
          <div className="space-y-4 py-2">
            {errors.general && (
              <div
                role="alert"
                className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive"
              >
                {errors.general}
              </div>
            )}

            <div className="space-y-1">
              <label htmlFor="node-code" className="text-sm font-medium">
                Code <span className="text-destructive">*</span>
              </label>
              <Input
                id="node-code"
                value={formData.code}
                onChange={(e) => handleChange('code', e.target.value)}
                placeholder="REGION_EU"
                aria-describedby={errors.code ? 'node-code-error' : undefined}
                aria-label="Code"
              />
              {errors.code && (
                <p id="node-code-error" className="text-sm text-destructive">
                  {errors.code}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="node-display-name" className="text-sm font-medium">
                Display Name <span className="text-destructive">*</span>
              </label>
              <Input
                id="node-display-name"
                value={formData.displayName}
                onChange={(e) => handleChange('displayName', e.target.value)}
                placeholder="Europe Region"
                aria-describedby={errors.displayName ? 'node-display-name-error' : undefined}
                aria-label="Display Name"
              />
              {errors.displayName && (
                <p id="node-display-name-error" className="text-sm text-destructive">
                  {errors.displayName}
                </p>
              )}
            </div>

            <div className="space-y-1">
              <label htmlFor="node-type" className="text-sm font-medium">
                Node Type
              </label>
              <Input
                id="node-type"
                value={formData.nodeType}
                onChange={(e) => handleChange('nodeType', e.target.value)}
                placeholder="region"
                aria-label="Node Type"
              />
              <p className="text-xs text-muted-foreground">
                Classification tag (e.g. region, zone, meter)
              </p>
            </div>

            <div className="space-y-1">
              <label htmlFor="node-parent" className="text-sm font-medium">
                Parent Node
              </label>
              <select
                id="node-parent"
                value={formData.parentNodeId}
                onChange={(e) => handleChange('parentNodeId', e.target.value)}
                aria-label="Parent Node"
                className="h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-xs"
              >
                <option value="">(Root Level)</option>
                {roots.map((node) => (
                  <option key={node.id} value={node.id}>
                    {node.id} {node.nodeType ? `[${node.nodeType}]` : ''}
                  </option>
                ))}
              </select>
              <p className="text-xs text-muted-foreground">
                Leave empty to create a root-level node.
              </p>
            </div>

            <div className="space-y-1">
              <label htmlFor="node-description" className="text-sm font-medium">
                Description
              </label>
              <textarea
                id="node-description"
                value={formData.description}
                onChange={(e) => handleChange('description', e.target.value)}
                placeholder="Optional description..."
                maxLength={1000}
                aria-label="Description"
                rows={3}
                className="w-full rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-xs resize-none focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
              />
            </div>

            <div className="space-y-1">
              <label className="text-sm font-medium">Additional Attributes</label>
              <KeyValueEditor
                value={formData.attributes}
                onChange={(attrs) => handleChange('attributes', attrs)}
              />
            </div>
          </div>
        </form>

        <DialogFooter>
          <Button variant="outline" type="button" onClick={() => onOpenChange(false)}>
            Cancel
          </Button>
          <Button
            type="submit"
            form="create-node-form"
            disabled={mutation.isPending}
          >
            {mutation.isPending ? 'Creating...' : 'Create Node'}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
