package email

// DefaultTemplateCategoryMap maps template names to their allowed category.
// This prevents callers from self-declaring a category to bypass consent checks
// (e.g., declaring a marketing template as TRANSACTIONAL).
var DefaultTemplateCategoryMap = map[string]string{
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
