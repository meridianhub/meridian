package domain

import (
	vf "github.com/meridianhub/meridian/shared/pkg/valuationfeature"
)

// ValuationFeature re-exports the shared valuation feature domain type.
type ValuationFeature = vf.ValuationFeature

// ValuationFeatureLifecycleStatus re-exports the shared lifecycle status type.
type ValuationFeatureLifecycleStatus = vf.LifecycleStatus

// Lifecycle status constants for valuation features.
const (
	ValuationFeatureLifecycleStatusInitiated  = vf.LifecycleStatusInitiated
	ValuationFeatureLifecycleStatusActive     = vf.LifecycleStatusActive
	ValuationFeatureLifecycleStatusTerminated = vf.LifecycleStatusTerminated
)

// Valuation feature domain error aliases.
var (
	ErrInvalidValuationFeatureTransition = vf.ErrInvalidLifecycleTransition
	ErrValuationFeatureNotActive         = vf.ErrNotActive
	ErrInvalidValuationFeatureParameters = vf.ErrInvalidParameters
	ErrValuationFeatureInstrumentEmpty   = vf.ErrInstrumentCodeEmpty
)

// NewValuationFeature delegates to the shared package constructor.
var NewValuationFeature = vf.NewValuationFeature
