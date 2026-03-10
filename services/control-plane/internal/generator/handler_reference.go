// Package generator provides context component generators for AI-assisted manifest generation.
// These generators produce structured text documents optimized for LLM consumption.
package generator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
)

// BuildHandlerReferenceCard generates a markdown-style reference document listing all
// handlers registered in the schema registry. The output is optimized for LLM consumption:
// compact but complete, grouped by service domain.
func BuildHandlerReferenceCard(registry *schema.Registry) string {
	var sb strings.Builder

	sb.WriteString("## Handler Reference Card\n\n")
	sb.WriteString("Use these handler names in Starlark saga scripts via `ctx.<service>.<handler>(...)`.\n\n")

	// Group handlers by service domain (prefix before the first dot)
	type handlerEntry struct {
		fullName string
		def      *schema.HandlerDef
	}

	byService := make(map[string][]handlerEntry)
	for _, name := range registry.ListHandlers() {
		def, err := registry.GetHandler(name)
		if err != nil {
			continue
		}
		service := servicePrefix(name)
		byService[service] = append(byService[service], handlerEntry{fullName: name, def: def})
	}

	// Sort service names for deterministic output
	services := make([]string, 0, len(byService))
	for svc := range byService {
		services = append(services, svc)
	}
	sort.Strings(services)

	for _, svc := range services {
		handlers := byService[svc]
		sort.Slice(handlers, func(i, j int) bool {
			return handlers[i].fullName < handlers[j].fullName
		})

		sb.WriteString(fmt.Sprintf("### %s\n\n", svc))

		for _, h := range handlers {
			writeHandlerEntry(&sb, h.fullName, h.def)
		}
	}

	return sb.String()
}

// writeHandlerEntry writes a single handler entry to the string builder.
func writeHandlerEntry(sb *strings.Builder, fullName string, def *schema.HandlerDef) {
	// Handler name and deprecation status
	if def.Deprecated {
		fmt.Fprintf(sb, "#### `%s` *(deprecated)*\n", fullName)
	} else {
		fmt.Fprintf(sb, "#### `%s`\n", fullName)
	}

	if def.Description != "" {
		fmt.Fprintf(sb, "%s\n", def.Description)
	}

	// Parameters
	if len(def.Params) > 0 {
		sb.WriteString("**Params:**\n")
		paramNames := sortedKeys(def.Params)
		for _, paramName := range paramNames {
			field := def.Params[paramName]
			fmt.Fprintf(sb, "- `%s`: %s", paramName, formatFieldType(field))
			if field.Required {
				sb.WriteString(" *(required)*")
			}
			if field.Description != "" {
				fmt.Fprintf(sb, " — %s", field.Description)
			}
			sb.WriteString("\n")
		}
	}

	// Returns
	if len(def.Returns) > 0 {
		sb.WriteString("**Returns:**\n")
		returnNames := sortedKeys(def.Returns)
		for _, returnName := range returnNames {
			field := def.Returns[returnName]
			fmt.Fprintf(sb, "- `%s`: %s", returnName, formatFieldType(field))
			if field.Description != "" {
				fmt.Fprintf(sb, " — %s", field.Description)
			}
			sb.WriteString("\n")
		}
	}

	// Compensation
	switch {
	case def.Compensate != "":
		fmt.Fprintf(sb, "**Compensation:** `%s`\n", def.Compensate)
	case def.CompensationStrategy == schema.CompensationStrategyNone:
		sb.WriteString("**Compensation:** none\n")
	case def.CompensationStrategy == schema.CompensationStrategySagaManaged:
		sb.WriteString("**Compensation:** saga-managed\n")
	}

	sb.WriteString("\n")
}

// servicePrefix extracts the service domain from a fully-qualified handler name.
// For "position_keeping.initiate_log" it returns "position_keeping".
// For handlers with no dot, returns the full name.
func servicePrefix(handlerName string) string {
	if idx := strings.Index(handlerName, "."); idx >= 0 {
		return handlerName[:idx]
	}
	return handlerName
}

// formatFieldType returns a compact string representation of a field type.
func formatFieldType(field *schema.FieldDef) string {
	switch field.Type {
	case schema.TypeEnum:
		return fmt.Sprintf("enum(%s)", strings.Join(field.Values, "|"))
	case schema.TypeArray:
		if field.ItemType != "" {
			return fmt.Sprintf("array<%s>", field.ItemType)
		}
		return "array"
	case schema.TypeMap:
		if field.KeyType != "" && field.ValueType != "" {
			return fmt.Sprintf("map<%s,%s>", field.KeyType, field.ValueType)
		}
		return "map"
	case schema.TypeString, schema.TypeInt32, schema.TypeInt64, schema.TypeUint32,
		schema.TypeBool, schema.TypeDecimal, schema.TypeUUID:
		return string(field.Type)
	}
	return string(field.Type)
}

// sortedKeys returns the keys of a FieldDef map in sorted order.
func sortedKeys(m map[string]*schema.FieldDef) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
