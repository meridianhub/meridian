package organization

// OrgIDKey is the universal key for organization ID across all transport layers:
// JWT claims, context values, Kafka headers, gRPC metadata, HTTP headers.
const OrgIDKey = "x-org-id"
