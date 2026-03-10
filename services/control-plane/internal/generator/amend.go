package generator

import (
	"fmt"
	"sort"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"gopkg.in/yaml.v3"
)

// AmendImpact describes what changed between the original and amended manifests.
type AmendImpact struct {
	// Added lists resources present in the amended manifest but not in the original.
	// Format: "type:code" (e.g., "instrument:CARBON_CREDIT", "saga:carbon_offset_flow").
	Added []string

	// Modified lists resources present in both but with differences.
	// Format: "type:code" (e.g., "account_type:TRADING_ACCOUNT").
	Modified []string

	// Removed lists resources present in the original but absent from the amended manifest.
	// These are flagged as warnings since the user may not have intended to remove them.
	// Format: "type:code" (e.g., "instrument:USD").
	Removed []string
}

// ToDecisions converts the impact analysis into human-readable decision strings
// suitable for inclusion in GenerationMetadata.decisions.
func (a *AmendImpact) ToDecisions() []string {
	decisions := make([]string, 0, len(a.Added)+len(a.Modified)+len(a.Removed))

	for _, r := range a.Added {
		decisions = append(decisions, fmt.Sprintf("Added %s", r))
	}
	for _, r := range a.Modified {
		decisions = append(decisions, fmt.Sprintf("Modified %s", r))
	}
	for _, r := range a.Removed {
		decisions = append(decisions, fmt.Sprintf("Warning: Removed %s (was present in original manifest)", r))
	}

	return decisions
}

// ComputeAmendImpact compares the original and amended manifest YAML strings to detect
// what resources were added, modified, or removed. Both inputs must be valid YAML.
// If either cannot be parsed, an empty impact is returned.
func ComputeAmendImpact(originalYAML, amendedYAML string) AmendImpact {
	var origDoc, amendDoc manifestYAMLDoc
	if err := yaml.Unmarshal([]byte(originalYAML), &origDoc); err != nil {
		return AmendImpact{}
	}
	if err := yaml.Unmarshal([]byte(amendedYAML), &amendDoc); err != nil {
		return AmendImpact{}
	}

	impact := AmendImpact{}

	// Compare instruments by code.
	diffResources(
		extractCodes(origDoc.Instruments, "code"),
		extractCodes(amendDoc.Instruments, "code"),
		"instrument",
		&impact,
	)

	// Compare account types by code.
	diffResources(
		extractCodes(origDoc.AccountTypes, "code"),
		extractCodes(amendDoc.AccountTypes, "code"),
		"account_type",
		&impact,
	)

	// Compare sagas by name.
	diffResources(
		extractCodes(origDoc.Sagas, "name"),
		extractCodes(amendDoc.Sagas, "name"),
		"saga",
		&impact,
	)

	sort.Strings(impact.Added)
	sort.Strings(impact.Modified)
	sort.Strings(impact.Removed)

	return impact
}

// extractCodes extracts identifier values from a slice of YAML maps using the given key.
func extractCodes(items []map[string]interface{}, key string) map[string]bool {
	codes := make(map[string]bool, len(items))
	for _, item := range items {
		if code, ok := item[key].(string); ok && code != "" {
			codes[code] = true
		}
	}
	return codes
}

// diffResources compares two sets of resource identifiers and populates the impact.
// Resources in amended but not original are Added.
// Resources in original but not amended are Removed.
// Resources in both are considered potentially Modified (conservative — we don't deep-diff content).
func diffResources(original, amended map[string]bool, resourceType string, impact *AmendImpact) {
	for code := range amended {
		if original[code] {
			// Present in both — conservatively mark as modified.
			// A true deep diff would compare full content, but for impact reporting
			// we flag anything that existed before and still exists.
			impact.Modified = append(impact.Modified, fmt.Sprintf("%s:%s", resourceType, code))
		} else {
			impact.Added = append(impact.Added, fmt.Sprintf("%s:%s", resourceType, code))
		}
	}
	for code := range original {
		if !amended[code] {
			impact.Removed = append(impact.Removed, fmt.Sprintf("%s:%s", resourceType, code))
		}
	}
}

// protoManifestToYAMLMap converts a proto Manifest to a YAML-friendly map structure.
// This produces a map suitable for yaml.Marshal that mirrors the manifest YAML format.
func protoManifestToYAMLMap(m *controlplanev1.Manifest) map[string]interface{} {
	if m == nil {
		return map[string]interface{}{}
	}

	result := make(map[string]interface{})

	if m.Version != "" {
		result["version"] = m.Version
	}
	if m.Metadata != nil {
		result["metadata"] = metadataToMap(m.Metadata)
	}
	if len(m.Instruments) > 0 {
		result["instruments"] = instrumentsToMaps(m.Instruments)
	}
	if len(m.AccountTypes) > 0 {
		result["account_types"] = accountTypesToMaps(m.AccountTypes)
	}
	if len(m.Sagas) > 0 {
		result["sagas"] = sagasToMaps(m.Sagas)
	}

	return result
}

func metadataToMap(md *controlplanev1.ManifestMetadata) map[string]interface{} {
	meta := map[string]interface{}{}
	if md.Name != "" {
		meta["name"] = md.Name
	}
	if md.Description != "" {
		meta["description"] = md.Description
	}
	return meta
}

func instrumentsToMaps(instruments []*controlplanev1.InstrumentDefinition) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(instruments))
	for _, inst := range instruments {
		item := map[string]interface{}{"code": inst.Code}
		if inst.Name != "" {
			item["name"] = inst.Name
		}
		if inst.Type != controlplanev1.InstrumentType_INSTRUMENT_TYPE_UNSPECIFIED {
			item["type"] = inst.Type.String()
		}
		if inst.Dimensions != nil {
			dims := map[string]interface{}{}
			if inst.Dimensions.Unit != "" {
				dims["unit"] = inst.Dimensions.Unit
			}
			if inst.Dimensions.Precision != 0 {
				dims["precision"] = inst.Dimensions.Precision
			}
			item["dimensions"] = dims
		}
		out = append(out, item)
	}
	return out
}

func accountTypesToMaps(accountTypes []*controlplanev1.AccountTypeDefinition) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(accountTypes))
	for _, at := range accountTypes {
		item := map[string]interface{}{"code": at.Code}
		if at.Name != "" {
			item["name"] = at.Name
		}
		if len(at.AllowedInstruments) > 0 {
			item["allowed_instruments"] = at.AllowedInstruments
		}
		out = append(out, item)
	}
	return out
}

func sagasToMaps(sagas []*controlplanev1.SagaDefinition) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(sagas))
	for _, saga := range sagas {
		item := map[string]interface{}{"name": saga.Name}
		if saga.Trigger != "" {
			item["trigger"] = saga.Trigger
		}
		if saga.Script != "" {
			item["script"] = saga.Script
		}
		out = append(out, item)
	}
	return out
}
