package schema

import (
	"fmt"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// ResolveProtoTypes resolves proto-referenced handlers in the schema by looking up
// proto service/method descriptors and populating Params/Returns from proto reflection.
// Handlers without ProtoRef are left unchanged (composite handlers or inline-param handlers).
// Uses the global proto registry by default; pass a custom resolver for testing.
func (s *Schema) ResolveProtoTypes(files *protoregistry.Files) error {
	if files == nil {
		files = protoregistry.GlobalFiles
	}
	for handlerName, handler := range s.Handlers {
		if handler.ProtoRef == nil {
			// Composite handlers intentionally have no proto_ref - they orchestrate
			// multiple sub-operations and define their own parameter handling.
			// Non-composite handlers without proto_ref define params inline.
			continue
		}
		if err := resolveHandlerProto(handlerName, handler, files); err != nil {
			return fmt.Errorf("handler %s: %w", handlerName, err)
		}
	}
	return nil
}

// resolveHandlerProto resolves a single handler's proto reference into Params/Returns.
func resolveHandlerProto(_ string, handler *HandlerDef, files *protoregistry.Files) error {
	ref := handler.ProtoRef

	// Parse "package.Service/Method" into service and method names
	parts := strings.SplitN(ref.FullMethod, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("%w: %s", ErrInvalidProtoRPC, ref.FullMethod)
	}
	serviceFQN := protoreflect.FullName(parts[0])
	methodName := protoreflect.Name(parts[1])

	// Look up the service descriptor
	serviceDesc, err := findServiceDescriptor(files, serviceFQN)
	if err != nil {
		return err
	}

	// Look up the method
	methodDesc := serviceDesc.Methods().ByName(methodName)
	if methodDesc == nil {
		return fmt.Errorf("%w: %s in service %s", ErrProtoMethodNotFound, methodName, serviceFQN)
	}

	// Resolve params from request message
	reqMsg := methodDesc.Input()
	params, err := resolveExposedFields(reqMsg, ref.ExposedParams, ref.ParamAliases)
	if err != nil {
		return fmt.Errorf("params: %w", err)
	}
	handler.Params = params

	// Resolve returns from response message
	respMsg := methodDesc.Output()
	returns, err := resolveExposedFields(respMsg, ref.ExposedReturns, nil)
	if err != nil {
		return fmt.Errorf("returns: %w", err)
	}
	handler.Returns = returns

	return nil
}

// findServiceDescriptor searches the proto registry for a service by fully-qualified name.
func findServiceDescriptor(files *protoregistry.Files, fqn protoreflect.FullName) (protoreflect.ServiceDescriptor, error) {
	desc, err := files.FindDescriptorByName(fqn)
	if err != nil {
		return nil, fmt.Errorf("%w: %s (%w)", ErrProtoServiceNotFound, fqn, err)
	}
	sd, ok := desc.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%w: %s is not a service descriptor", ErrProtoServiceNotFound, fqn)
	}
	return sd, nil
}

// resolveExposedFields builds a FieldDef map from a proto message descriptor,
// filtered to only the exposed field paths. If exposed is nil/empty, all top-level
// fields are included. Aliases are applied to param fields.
// Returns ErrProtoFieldPathNotFound if any exposed path cannot be resolved.
func resolveExposedFields(md protoreflect.MessageDescriptor, exposed []string, aliases map[string]string) (map[string]*FieldDef, error) {
	fields := make(map[string]*FieldDef)

	if len(exposed) == 0 {
		// Include all top-level fields
		fds := md.Fields()
		for i := 0; i < fds.Len(); i++ {
			fd := fds.Get(i)
			fieldName := string(fd.Name())
			fields[fieldName] = deriveFieldDef(fd)
		}
	} else {
		// Only include exposed field paths
		for _, path := range exposed {
			fd := resolveFieldPath(md, path)
			if fd == nil {
				return nil, fmt.Errorf("%w: %q in message %s", ErrProtoFieldPathNotFound, path, md.FullName())
			}
			// Use the leaf field name as the key
			leafName := leafFieldName(path)
			if _, dup := fields[leafName]; dup {
				return nil, fmt.Errorf("%w: %q from path %q in message %s", ErrDuplicateLeafName, leafName, path, md.FullName())
			}
			fields[leafName] = deriveFieldDef(fd)
		}
	}

	// Apply aliases with validation
	for original, alias := range aliases {
		def, ok := fields[original]
		if !ok {
			return nil, fmt.Errorf("%w: %q (alias target: %q)", ErrUnknownAliasSource, original, alias)
		}
		if _, collision := fields[alias]; collision && alias != original {
			return nil, fmt.Errorf("%w: %q (from alias of %q)", ErrAliasCollision, alias, original)
		}
		delete(fields, original)
		fields[alias] = def
	}

	return fields, nil
}

// resolveFieldPath resolves a dot-separated field path (e.g., "log.status_tracking.current_status")
// through nested proto message descriptors. Returns the leaf field descriptor, or nil if not found.
func resolveFieldPath(md protoreflect.MessageDescriptor, path string) protoreflect.FieldDescriptor {
	parts := strings.Split(path, ".")
	current := md

	for i, part := range parts {
		fd := current.Fields().ByName(protoreflect.Name(part))
		if fd == nil {
			return nil
		}
		// If this is the last part, return the field descriptor
		if i == len(parts)-1 {
			return fd
		}
		// Otherwise, navigate into the nested message
		if fd.Kind() != protoreflect.MessageKind {
			return nil // Can't traverse into non-message field
		}
		current = fd.Message()
	}
	return nil
}

// leafFieldName returns the last segment of a dot-separated path.
func leafFieldName(path string) string {
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		return path[idx+1:]
	}
	return path
}

// HasProtoRef returns true if the handler uses proto-referenced format.
func (h *HandlerDef) HasProtoRef() bool {
	return h.ProtoRef != nil
}

// IsComposite returns true if the handler is a composite handler that
// orchestrates multiple sub-operations and intentionally has no proto_ref.
func (h *HandlerDef) IsComposite() bool {
	return h.Composite
}
