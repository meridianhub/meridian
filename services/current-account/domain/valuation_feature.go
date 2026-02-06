package domain

import (
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
)

// Type aliases re-exporting shared valuationfeature types.
// This maintains backwards compatibility for code within the current-account service
// that references domain.ValuationFeature and related types.

type ValuationFeature = vf.ValuationFeature
type ValuationFeatureLifecycleStatus = vf.LifecycleStatus

const (
	ValuationFeatureLifecycleStatusInitiated  = vf.LifecycleStatusInitiated
	ValuationFeatureLifecycleStatusActive     = vf.LifecycleStatusActive
	ValuationFeatureLifecycleStatusTerminated = vf.LifecycleStatusTerminated
)

// Error aliases
var (
	ErrInvalidValuationFeatureTransition = vf.ErrInvalidLifecycleTransition
	ErrValuationFeatureNotActive         = vf.ErrNotActive
	ErrInvalidValuationFeatureParameters = vf.ErrInvalidParameters
	ErrInvalidTemporalRange              = vf.ErrInvalidTemporalRange
	ErrInstrumentCodeEmpty               = vf.ErrInstrumentCodeEmpty
)

// NewValuationFeature delegates to the shared package constructor.
var NewValuationFeature = vf.NewValuationFeature
