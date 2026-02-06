package persistence

import (
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
)

// ValuationFeatureEntity is a type alias re-exporting the shared entity.
// This maintains backwards compatibility for code within the current-account service.
type ValuationFeatureEntity = vf.Entity
