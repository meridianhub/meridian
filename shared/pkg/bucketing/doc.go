// Package bucketing provides canonical bucket ID calculation for the Universal Asset System.
//
// All services MUST use this library to compute bucket IDs. Direct string construction
// of bucket IDs is forbidden to prevent Bucket Drift — the condition where the same
// instrument and attributes produce different bucket IDs across services.
//
// Bucket ID format: "dimension_instrument_attr1=val1_attr2=val2"
//
// Examples:
//
//	GBP (CURRENCY dimension)                -> "currency_gbp"
//	KWH (ENERGY dimension, source=solar)    -> "energy_kwh_source=solar"
//	GPU_HOUR (COMPUTE dimension, tier=prem) -> "compute_gpu_hour_tier=prem"
package bucketing
