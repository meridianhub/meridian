import { useCallback } from 'react'
import { Input } from '@/components/ui/input'
import type { FieldDefinition, ResourceSchema } from '../lib/resource-schema-registry'

interface ResourceFormProps {
  schema: ResourceSchema
  values: Record<string, string>
  onChange: (values: Record<string, string>) => void
  disabled?: boolean
}

function FieldInput({
  field,
  value,
  onChange,
  disabled,
}: {
  field: FieldDefinition
  value: string
  onChange: (value: string) => void
  disabled?: boolean
}) {
  if (field.type === 'select' && field.options) {
    return (
      <select
        id={`resource-field-${field.name}`}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className="border-input bg-transparent h-9 w-full rounded-md border px-3 py-1 text-sm shadow-xs focus-visible:border-ring focus-visible:ring-ring/50 focus-visible:ring-[3px] outline-none disabled:opacity-50"
        aria-label={field.label}
      >
        <option value="">Select {field.label}</option>
        {field.options.map((opt) => (
          <option key={opt.value} value={opt.value}>
            {opt.label}
          </option>
        ))}
      </select>
    )
  }

  return (
    <Input
      id={`resource-field-${field.name}`}
      type={field.type === 'number' ? 'number' : 'text'}
      value={value}
      onChange={(e) => onChange(e.target.value)}
      placeholder={field.placeholder}
      disabled={disabled}
      aria-label={field.label}
    />
  )
}

export function ResourceForm({ schema, values, onChange, disabled }: ResourceFormProps) {
  const handleFieldChange = useCallback(
    (fieldName: string, fieldValue: string) => {
      onChange({ ...values, [fieldName]: fieldValue })
    },
    [values, onChange],
  )

  return (
    <div className="space-y-4" data-testid="resource-form">
      {schema.fields.map((field) => (
        <div key={field.name} className="space-y-1.5">
          <label
            htmlFor={`resource-field-${field.name}`}
            className="text-sm font-medium text-foreground"
          >
            {field.label}
            {field.required && <span className="text-destructive ml-0.5">*</span>}
          </label>
          <FieldInput
            field={field}
            value={values[field.name] ?? ''}
            onChange={(v) => handleFieldChange(field.name, v)}
            disabled={disabled}
          />
        </div>
      ))}
    </div>
  )
}
