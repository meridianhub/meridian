// Package mapping re-exports the shared mapping engine for gateway-internal use.
// The engine implementation lives in shared/pkg/mapping to allow other services
// (e.g. reference-data) to use it without creating circular imports.
package mapping

import sharedmapping "github.com/meridianhub/meridian/shared/pkg/mapping"

// Re-export error sentinels.
var (
	ErrFieldExtraction  = sharedmapping.ErrFieldExtraction
	ErrTransform        = sharedmapping.ErrTransform
	ErrValidation       = sharedmapping.ErrValidation
	ErrCELCompilation   = sharedmapping.ErrCELCompilation
	ErrCELEvaluation    = sharedmapping.ErrCELEvaluation
	ErrIdempotencyKey   = sharedmapping.ErrIdempotencyKey
	ErrInvalidJSON      = sharedmapping.ErrInvalidJSON
	ErrEnumNotMapped    = sharedmapping.ErrEnumNotMapped
	ErrDateParse        = sharedmapping.ErrDateParse
	ErrAttributeFlatten = sharedmapping.ErrAttributeFlatten
)

// Engine is an alias for the shared mapping engine.
// See shared/pkg/mapping.Engine for documentation.
type Engine = sharedmapping.Engine

// InboundResult is an alias for the shared inbound transform result.
// See shared/pkg/mapping.InboundResult for documentation.
type InboundResult = sharedmapping.InboundResult

// Re-export constructors.
var (
	NewEngine              = sharedmapping.NewEngine
	NewEngineWithCacheSize = sharedmapping.NewEngineWithCacheSize
	EscapeJSONPath         = sharedmapping.EscapeJSONPath
)
