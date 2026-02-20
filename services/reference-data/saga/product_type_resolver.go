package saga

import (
	"context"
	"fmt"

	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/shared/platform/tenant"
)

// ProductTypeSagaResolver resolves saga definitions based on an account's product type.
//
// Resolution rules:
//   - If the product type has a non-empty DefaultSagaPrefix, the saga name is
//     "{prefix}.{operation}". If no active saga with that name exists,
//     ErrSagaNotFound is returned — there is NO fallback to the generic operation.
//   - If the product type has an empty DefaultSagaPrefix, the generic "{operation}"
//     saga is resolved via the underlying Registry.
//   - If the product type code is not found in the cache, the generic "{operation}"
//     saga is resolved as a fallback.
type ProductTypeSagaResolver struct {
	registry         Registry
	accountTypeCache *cache.LocalAccountTypeCache
}

// NewProductTypeSagaResolver creates a new ProductTypeSagaResolver.
func NewProductTypeSagaResolver(registry Registry, accountTypeCache *cache.LocalAccountTypeCache) *ProductTypeSagaResolver {
	return &ProductTypeSagaResolver{
		registry:         registry,
		accountTypeCache: accountTypeCache,
	}
}

// ResolveForProductType resolves the saga definition for a given product type and operation.
//
// If the product type has a DefaultSagaPrefix, the saga name "{prefix}.{operation}" is
// resolved. If that saga is not found, ErrSagaNotFound is returned (no fallback to generic).
//
// If the product type has no DefaultSagaPrefix, or if the product type is not found in
// the cache, the generic "{operation}" saga is resolved.
func (r *ProductTypeSagaResolver) ResolveForProductType(
	ctx context.Context,
	tenantID tenant.TenantID,
	productTypeCode string,
	operation string,
) (*Definition, error) {
	cached, err := r.accountTypeCache.GetOrLoad(ctx, tenantID, productTypeCode)
	if err != nil {
		// Product type not found or unavailable — fall back to generic operation.
		return r.registry.GetActive(ctx, operation)
	}

	prefix := cached.Definition.DefaultSagaPrefix
	if prefix == "" {
		// No prefix defined — resolve generic operation.
		return r.registry.GetActive(ctx, operation)
	}

	// Prefix defined — resolve "{prefix}.{operation}" with no fallback.
	sagaName := prefix + "." + operation
	def, err := r.registry.GetActive(ctx, sagaName)
	if err != nil {
		return nil, fmt.Errorf("%w: no saga '%s' found for product type '%s' operation '%s'",
			ErrSagaNotFound, sagaName, productTypeCode, operation)
	}
	return def, nil
}
