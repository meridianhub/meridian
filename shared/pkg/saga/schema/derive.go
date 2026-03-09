package schema

import (
	"fmt"
	"strings"

	"github.com/meridianhub/meridian/shared/pkg/saga"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// DeriveSchema builds a Schema from the handler registry using proto reflection.
// Handlers without ProtoRequestType get empty params; handlers without ProtoResponseType
// get empty returns. ParamOverrides are applied after proto-derived fields.
func DeriveSchema(registry *saga.HandlerRegistry) (*Schema, error) {
	allMeta := registry.AllWithMetadata()

	s := &Schema{
		Handlers: make(map[string]*HandlerDef, len(allMeta)),
	}

	for name, meta := range allMeta {
		hd, err := DeriveHandlerDef(name, meta)
		if err != nil {
			return nil, fmt.Errorf("handler %s: %w", name, err)
		}
		s.Handlers[name] = hd
	}

	return s, nil
}

// DeriveHandlerDef builds a single HandlerDef from handler metadata.
// Returns an error if a ParamOverride references a field not in the proto
// without providing an explicit Type.
// Exported for use by contract tests (Task 4).
func DeriveHandlerDef(_ string, meta *saga.HandlerMetadata) (*HandlerDef, error) {
	hd := &HandlerDef{
		Params:  make(map[string]*FieldDef),
		Returns: make(map[string]*FieldDef),
		Version: 1,
	}

	if meta == nil {
		return hd, nil
	}

	hd.Description = meta.Description
	hd.Compensate = meta.Compensate
	hd.Version = meta.Version
	if hd.Version == 0 {
		hd.Version = 1
	}
	hd.Deprecated = meta.DeprecatedMessage != ""

	// Set compensation strategy
	if meta.Compensate != "" {
		hd.CompensationStrategy = CompensationStrategyAuto
	} else if meta.CompensationStrategy != "" {
		hd.CompensationStrategy = CompensationStrategy(meta.CompensationStrategy)
	}

	// Derive params from proto request type
	if meta.ProtoRequestType != nil {
		deriveFields(meta.ProtoRequestType.ProtoReflect().Descriptor(), hd.Params)
	}

	// Apply param overrides
	if err := applyParamOverrides(hd.Params, meta.ParamOverrides); err != nil {
		return nil, err
	}

	// Derive returns from proto response type
	if meta.ProtoResponseType != nil {
		deriveFields(meta.ProtoResponseType.ProtoReflect().Descriptor(), hd.Returns)
	}

	// Convert handler conversions to schema conversion rules
	for _, conv := range meta.Conversions {
		hd.Conversions = append(hd.Conversions, ConversionRule{
			FromVersion:  conv.FromVersion,
			FromName:     conv.FromName,
			ParamMapping: conv.ParamMapping,
			Defaults:     conv.Defaults,
			Sunset:       conv.Sunset,
		})
	}

	return hd, nil
}

// deriveFields populates a field map from a proto message descriptor.
func deriveFields(md protoreflect.MessageDescriptor, fields map[string]*FieldDef) {
	fds := md.Fields()
	for i := 0; i < fds.Len(); i++ {
		fd := fds.Get(i)
		fieldName := string(fd.Name())

		fieldDef := deriveFieldDef(fd)
		fields[fieldName] = fieldDef
	}
}

// deriveFieldDef converts a single proto field descriptor to a FieldDef.
func deriveFieldDef(fd protoreflect.FieldDescriptor) *FieldDef {
	// Handle map fields first (they appear as repeated message with map_entry option)
	if fd.IsMap() {
		keyFD := fd.MapKey()
		valFD := fd.MapValue()
		return &FieldDef{
			Type:      TypeMap,
			KeyType:   protoKindToFieldType(keyFD.Kind(), keyFD),
			ValueType: protoKindToFieldType(valFD.Kind(), valFD),
		}
	}

	// Handle repeated (non-map) fields
	if fd.IsList() {
		itemType := protoKindToFieldType(fd.Kind(), fd)
		return &FieldDef{
			Type:     TypeArray,
			ItemType: itemType,
		}
	}

	// Scalar and enum fields
	ft := protoKindToFieldType(fd.Kind(), fd)
	def := &FieldDef{
		Type: ft,
	}

	// For enum fields, extract values with prefix stripping
	if fd.Kind() == protoreflect.EnumKind {
		def.Values = deriveEnumValues(fd.Enum())
	}

	return def
}

// protoKindToFieldType maps a protoreflect.Kind to our FieldType.
func protoKindToFieldType(kind protoreflect.Kind, _ protoreflect.FieldDescriptor) FieldType {
	switch kind {
	case protoreflect.StringKind:
		return TypeString
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return TypeInt32
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return TypeInt64
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return TypeUint32
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return TypeString // no TypeUint64; use string to preserve full unsigned range
	case protoreflect.BoolKind:
		return TypeBool
	case protoreflect.BytesKind:
		return TypeString // base64
	case protoreflect.EnumKind:
		return TypeEnum
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return TypeMap
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		return TypeString // floats as string to preserve precision
	default:
		return TypeString
	}
}

// deriveEnumValues extracts enum value names with prefix stripping,
// skipping the 0-valued UNSPECIFIED entry.
func deriveEnumValues(ed protoreflect.EnumDescriptor) []string {
	vals := ed.Values()
	raw := make([]string, 0, vals.Len())
	for i := 0; i < vals.Len(); i++ {
		v := vals.Get(i)
		if v.Number() == 0 {
			continue // Skip UNSPECIFIED
		}
		raw = append(raw, string(v.Name()))
	}
	return StripEnumPrefix(raw)
}

// StripEnumPrefix removes the common ENUM_NAME_ prefix from proto enum values.
// For example: ["POSTING_DIRECTION_DEBIT", "POSTING_DIRECTION_CREDIT"] -> ["DEBIT", "CREDIT"].
func StripEnumPrefix(values []string) []string {
	if len(values) == 0 {
		return values
	}

	// Find common prefix by splitting on '_' and finding shared segments
	prefix := findCommonEnumPrefix(values)
	if prefix == "" {
		return values
	}

	result := make([]string, len(values))
	for i, v := range values {
		result[i] = strings.TrimPrefix(v, prefix)
	}
	return result
}

// findCommonEnumPrefix finds the common UPPER_CASE_ prefix across all values.
func findCommonEnumPrefix(values []string) string {
	if len(values) == 0 {
		return ""
	}

	// Split first value into segments
	firstParts := strings.Split(values[0], "_")
	if len(firstParts) <= 1 {
		return ""
	}

	// Try progressively shorter prefixes (all but last segment, all but last 2, etc.)
	for prefixLen := len(firstParts) - 1; prefixLen > 0; prefixLen-- {
		candidate := strings.Join(firstParts[:prefixLen], "_") + "_"

		allMatch := true
		for _, v := range values {
			if !strings.HasPrefix(v, candidate) {
				allMatch = false
				break
			}
		}

		if allMatch {
			return candidate
		}
	}

	return ""
}

// applyParamOverrides applies ParamOverrides to the derived params map.
// Returns an error if an override references a field not in the proto
// without providing an explicit Type — this catches naming drift between
// proto fields and handler annotations.
func applyParamOverrides(params map[string]*FieldDef, overrides map[string]saga.ParamOverride) error {
	if overrides == nil {
		return nil
	}

	for fieldName, override := range overrides {
		// Handle derived fields: remove from params
		if override.Derived {
			delete(params, fieldName)
			continue
		}

		fd, exists := params[fieldName]
		if !exists {
			// Override for a field not in proto — require explicit Type
			if override.Type == "" {
				return fmt.Errorf("%w: %q", ErrOverrideMissingType, fieldName)
			}
			fd = &FieldDef{Type: FieldType(override.Type)}
			params[fieldName] = fd
		}

		// Apply type override
		if override.Type != "" {
			fd.Type = FieldType(override.Type)
		}

		// Apply required override
		if override.Required != nil {
			fd.Required = *override.Required
		}

		// Apply alias: rename the field
		if override.Alias != "" {
			delete(params, fieldName)
			params[override.Alias] = fd
		}
	}
	return nil
}
