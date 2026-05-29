package schema

import (
	"fmt"
	"strings"
)

// handlerTree represents a hierarchical tree of handler names.
// Handler names like "service.domain.action" are split into a tree structure:
//
//	service
//	  └─ domain
//	       └─ action (handler)
type handlerTree struct {
	children map[string]*handlerTree // Nested namespaces
	handlers map[string]string       // Handler names at this level (name -> full qualified name)
}

// newHandlerTree creates a new empty handler tree.
func newHandlerTree() *handlerTree {
	return &handlerTree{
		children: make(map[string]*handlerTree),
		handlers: make(map[string]string),
	}
}

// parseHandlerTree builds a handler tree from a list of handler names.
// Handler names are expected in the format "service.action" or "service.domain.action".
func parseHandlerTree(handlerNames []string) *handlerTree {
	root := newHandlerTree()

	for _, name := range handlerNames {
		parts := strings.Split(name, ".")
		if len(parts) < 2 {
			// Invalid handler name, skip
			continue
		}
		// Skip names with empty segments (e.g. "service..action",
		// ".service.action", "service.action.") which would otherwise
		// create empty keys in the tree.
		if hasEmptySegment(parts) {
			continue
		}

		current := root

		// Navigate/create the tree structure for all parts except the last
		for i := 0; i < len(parts)-1; i++ {
			part := parts[i]
			if current.children[part] == nil {
				current.children[part] = newHandlerTree()
			}
			current = current.children[part]
		}

		// The last part is the handler name
		handlerName := parts[len(parts)-1]
		current.handlers[handlerName] = name
	}

	return root
}

// hasEmptySegment reports whether any of the dot-separated parts is empty.
func hasEmptySegment(parts []string) bool {
	for _, part := range parts {
		if part == "" {
			return true
		}
	}
	return false
}

// findNode finds a node at the given dot-separated path.
// Returns nil if the path does not exist.
func (t *handlerTree) findNode(path string) *handlerTree {
	parts := strings.Split(path, ".")
	current := t

	for _, part := range parts {
		if current.children[part] == nil {
			return nil
		}
		current = current.children[part]
	}

	return current
}

// validate checks the tree for naming conflicts.
// A conflict occurs when a name is used as both a handler and a namespace.
func (t *handlerTree) validate() error {
	return t.validateNode("")
}

func (t *handlerTree) validateNode(path string) error {
	// Check for conflicts at this level
	for handlerName := range t.handlers {
		if _, exists := t.children[handlerName]; exists {
			fullPath := handlerName
			if path != "" {
				fullPath = path + "." + handlerName
			}
			return fmt.Errorf("%w: %q is used as both a handler and a namespace", ErrNamingConflict, fullPath)
		}
	}

	// Recursively validate children
	for name, child := range t.children {
		childPath := name
		if path != "" {
			childPath = path + "." + name
		}
		if err := child.validateNode(childPath); err != nil {
			return err
		}
	}

	return nil
}
