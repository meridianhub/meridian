package auth

import "sort"

// VerifyMethodCoverage checks that every method in expectedMethods has a corresponding
// entry in the MethodRBACConfig permissions map, and that no extra methods exist in the
// config that aren't in the expected list. Returns sorted (missing, extra) method slices.
func VerifyMethodCoverage(cfg MethodRBACConfig, expectedMethods []string) (missing, extra []string) {
	expected := make(map[string]bool, len(expectedMethods))
	for _, m := range expectedMethods {
		expected[m] = true
	}

	configured := make(map[string]bool, len(cfg.Permissions))
	for m := range cfg.Permissions {
		configured[m] = true
	}

	for _, m := range expectedMethods {
		if !configured[m] {
			missing = append(missing, m)
		}
	}

	for m := range cfg.Permissions {
		if !expected[m] {
			extra = append(extra, m)
		}
	}

	sort.Strings(missing)
	sort.Strings(extra)

	return missing, extra
}
