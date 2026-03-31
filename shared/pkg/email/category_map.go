package email

// defaultTemplateCategoryMap maps template names to their allowed category.
// This prevents callers from self-declaring a category to bypass consent checks
// (e.g., declaring a marketing template as TRANSACTIONAL).
var defaultTemplateCategoryMap = map[string]string{
	// Transactional - legally required, cannot be suppressed.
	"dunning-notice":       CategoryTransactional,
	"account-frozen":       CategoryTransactional,
	"payment-confirmation": CategoryTransactional,
	"invoice-delivery":     CategoryTransactional,

	// Operational - service-related, opt-out model.
	"dunning-resolved": CategoryOperational,
	"welcome":          CategoryOperational,
	"service-update":   CategoryOperational,

	// Marketing - promotional, opt-in model.
	"promotional-offer": CategoryMarketing,
}

// DefaultTemplateCategoryMap returns a copy of the default template-to-category
// mapping. Each call returns a fresh map, so callers cannot mutate the canonical
// enforcement rules.
func DefaultTemplateCategoryMap() map[string]string {
	cp := make(map[string]string, len(defaultTemplateCategoryMap))
	for k, v := range defaultTemplateCategoryMap {
		cp[k] = v
	}
	return cp
}
