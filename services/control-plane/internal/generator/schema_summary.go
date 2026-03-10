package generator

import (
	"fmt"
	"strings"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// BuildManifestSchemaSummary generates a compact schema summary of the Manifest proto message
// using proto reflection. The output is optimized for LLM understanding when generating
// manifest YAML files.
func BuildManifestSchemaSummary() string {
	var sb strings.Builder

	sb.WriteString("## Manifest Schema Summary\n\n")
	sb.WriteString("The Manifest is the atomic configuration snapshot for a Meridian tenant.\n\n")

	msg := &controlplanev1.Manifest{}
	md := msg.ProtoReflect().Descriptor()

	writeMessageSummary(&sb, md, 0, 2)

	return sb.String()
}

// writeMessageSummary writes a summary of a proto message descriptor up to maxDepth levels deep.
func writeMessageSummary(sb *strings.Builder, md protoreflect.MessageDescriptor, depth, maxDepth int) {
	fields := md.Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		indent := strings.Repeat("  ", depth)
		fieldName := string(fd.Name())
		typeSummary := fieldTypeSummary(fd)

		required := isRequiredField(fd)
		requiredStr := ""
		if required {
			requiredStr = " *(required)*"
		}

		if fd.IsList() {
			fmt.Fprintf(sb, "%s- `%s`: repeated %s%s\n", indent, fieldName, typeSummary, requiredStr)
		} else {
			fmt.Fprintf(sb, "%s- `%s`: %s%s\n", indent, fieldName, typeSummary, requiredStr)
		}

		// Recurse into nested messages up to maxDepth
		if depth < maxDepth && fd.Kind() == protoreflect.MessageKind && !isWellKnownType(fd) {
			writeMessageSummary(sb, fd.Message(), depth+1, maxDepth)
		}
	}
}

// fieldTypeSummary returns a compact type description for a field descriptor.
func fieldTypeSummary(fd protoreflect.FieldDescriptor) string {
	switch fd.Kind() {
	case protoreflect.EnumKind:
		ed := fd.Enum()
		values := enumValueNames(ed)
		if len(values) <= 5 {
			return fmt.Sprintf("enum(%s)", strings.Join(values, "|"))
		}
		return fmt.Sprintf("enum(%s|...)", strings.Join(values[:3], "|"))
	case protoreflect.MessageKind:
		if isWellKnownType(fd) {
			return wellKnownTypeName(fd)
		}
		return string(fd.Message().Name())
	case protoreflect.BoolKind:
		return "bool"
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return "int32"
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return "uint32"
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return "int64"
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return "uint64"
	case protoreflect.FloatKind:
		return "float"
	case protoreflect.DoubleKind:
		return "double"
	case protoreflect.StringKind:
		return "string"
	case protoreflect.BytesKind:
		return "bytes"
	case protoreflect.GroupKind:
		return "group"
	}
	return fd.Kind().String()
}

// enumValueNames returns the names of all enum values, excluding the UNSPECIFIED value.
func enumValueNames(ed protoreflect.EnumDescriptor) []string {
	values := ed.Values()
	names := make([]string, 0, values.Len())
	for i := 0; i < values.Len(); i++ {
		name := string(values.Get(i).Name())
		// Skip UNSPECIFIED values to reduce noise
		if strings.Contains(name, "UNSPECIFIED") {
			continue
		}
		names = append(names, name)
	}
	return names
}

// isWellKnownType returns true if the field is a well-known proto type that
// should not be expanded further.
func isWellKnownType(fd protoreflect.FieldDescriptor) bool {
	if fd.Kind() != protoreflect.MessageKind {
		return false
	}
	fullName := string(fd.Message().FullName())
	return strings.HasPrefix(fullName, "google.protobuf.") ||
		strings.HasPrefix(fullName, "google.type.")
}

// wellKnownTypeName returns a compact name for well-known proto types.
func wellKnownTypeName(fd protoreflect.FieldDescriptor) string {
	fullName := string(fd.Message().FullName())
	parts := strings.Split(fullName, ".")
	return parts[len(parts)-1]
}

// isRequiredField checks if a field is marked required via buf validate annotations.
// Since we can't easily read buf validate options at runtime without the extension descriptors,
// we use a heuristic: message fields with non-nil defaults that appear in the proto are likely required.
// For accuracy, we check the field name against known required fields in the Manifest.
func isRequiredField(fd protoreflect.FieldDescriptor) bool {
	// Fields marked as required in the Manifest proto via buf validate
	knownRequired := map[string]bool{
		"version":  true,
		"metadata": true,
	}
	return knownRequired[string(fd.Name())]
}
