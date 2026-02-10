package domain

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

// Payment method domain errors
var (
	ErrInvalidProvider         = errors.New("invalid provider: must be STRIPE")
	ErrInvalidProviderCustomer = errors.New("invalid provider customer ID")
	ErrInvalidProviderMethod   = errors.New("invalid provider method ID")
	ErrInvalidMethodType       = errors.New("invalid method type")
	ErrInvalidPaymentStatus    = errors.New("invalid payment method status")
	ErrPaymentMethodRemoved    = errors.New("cannot modify a removed payment method")
	ErrPaymentMethodExpired    = errors.New("cannot set expired payment method as default")
)

// PaymentProvider represents an external payment provider
type PaymentProvider string

// Payment provider constants
const (
	PaymentProviderStripe PaymentProvider = "STRIPE"
)

// IsValid checks if the payment provider is valid
func (p PaymentProvider) IsValid() bool {
	switch p {
	case PaymentProviderStripe:
		return true
	default:
		return false
	}
}

// PaymentMethodType represents the type of payment method
type PaymentMethodType string

// Payment method type constants
const (
	PaymentMethodTypeCard        PaymentMethodType = "CARD"
	PaymentMethodTypeBankAccount PaymentMethodType = "BANK_ACCOUNT"
	PaymentMethodTypeSEPA        PaymentMethodType = "SEPA"
)

// IsValid checks if the payment method type is valid
func (mt PaymentMethodType) IsValid() bool {
	switch mt {
	case PaymentMethodTypeCard, PaymentMethodTypeBankAccount, PaymentMethodTypeSEPA:
		return true
	default:
		return false
	}
}

// PaymentMethodStatus represents the lifecycle state of a payment method
type PaymentMethodStatus string

// Payment method status constants
const (
	PaymentMethodStatusActive  PaymentMethodStatus = "ACTIVE"
	PaymentMethodStatusExpired PaymentMethodStatus = "EXPIRED"
	PaymentMethodStatusRemoved PaymentMethodStatus = "REMOVED"
)

// IsValid checks if the payment method status is valid
func (s PaymentMethodStatus) IsValid() bool {
	switch s {
	case PaymentMethodStatusActive, PaymentMethodStatusExpired, PaymentMethodStatusRemoved:
		return true
	default:
		return false
	}
}

// Stripe ID format validators
var (
	stripeCustomerIDRegex = regexp.MustCompile(`^cus_[a-zA-Z0-9]{10,}$`)
	stripeMethodIDRegex   = regexp.MustCompile(`^pm_[a-zA-Z0-9]{10,}$`)
)

// PaymentMethodMetadata contains non-sensitive display information about a payment method
type PaymentMethodMetadata struct {
	Last4    string `json:"last4,omitempty"`
	Brand    string `json:"brand,omitempty"`
	ExpMonth int    `json:"exp_month,omitempty"`
	ExpYear  int    `json:"exp_year,omitempty"`
}

// PaymentMethod represents a tokenized payment method linked to a party via an external provider.
//
// The Version field implements optimistic concurrency control to prevent lost updates
// in concurrent scenarios. The persistence layer should use this field in UPDATE
// statements (e.g., WHERE id = ? AND version = ?) to detect conflicts.
type PaymentMethod struct {
	id                 uuid.UUID
	partyID            uuid.UUID
	provider           PaymentProvider
	providerCustomerID string
	providerMethodID   string
	methodType         PaymentMethodType
	isDefault          bool
	metadata           *PaymentMethodMetadata
	status             PaymentMethodStatus
	createdAt          time.Time
	updatedAt          time.Time
	version            int64
}

// NewPaymentMethod creates a new payment method with validation
func NewPaymentMethod(
	partyID uuid.UUID,
	provider PaymentProvider,
	providerCustomerID string,
	providerMethodID string,
	methodType PaymentMethodType,
	isDefault bool,
	metadata *PaymentMethodMetadata,
) (*PaymentMethod, error) {
	if !provider.IsValid() {
		return nil, ErrInvalidProvider
	}

	if !stripeCustomerIDRegex.MatchString(providerCustomerID) {
		return nil, ErrInvalidProviderCustomer
	}

	if !stripeMethodIDRegex.MatchString(providerMethodID) {
		return nil, ErrInvalidProviderMethod
	}

	if !methodType.IsValid() {
		return nil, ErrInvalidMethodType
	}

	now := time.Now()
	return &PaymentMethod{
		id:                 uuid.New(),
		partyID:            partyID,
		provider:           provider,
		providerCustomerID: providerCustomerID,
		providerMethodID:   providerMethodID,
		methodType:         methodType,
		isDefault:          isDefault,
		metadata:           metadata,
		status:             PaymentMethodStatusActive,
		createdAt:          now,
		updatedAt:          now,
		version:            1,
	}, nil
}

// ReconstructPaymentMethod recreates a PaymentMethod from persistence layer data.
// This should only be used by repositories when loading from database.
func ReconstructPaymentMethod(
	id uuid.UUID,
	partyID uuid.UUID,
	provider PaymentProvider,
	providerCustomerID string,
	providerMethodID string,
	methodType PaymentMethodType,
	isDefault bool,
	metadata *PaymentMethodMetadata,
	status PaymentMethodStatus,
	createdAt time.Time,
	updatedAt time.Time,
	version int64,
) *PaymentMethod {
	return &PaymentMethod{
		id:                 id,
		partyID:            partyID,
		provider:           provider,
		providerCustomerID: providerCustomerID,
		providerMethodID:   providerMethodID,
		methodType:         methodType,
		isDefault:          isDefault,
		metadata:           metadata,
		status:             status,
		createdAt:          createdAt,
		updatedAt:          updatedAt,
		version:            version,
	}
}

// ID returns the payment method's unique identifier.
func (pm *PaymentMethod) ID() uuid.UUID { return pm.id }

// PartyID returns the party this payment method belongs to.
func (pm *PaymentMethod) PartyID() uuid.UUID { return pm.partyID }

// Provider returns the external payment provider.
func (pm *PaymentMethod) Provider() PaymentProvider { return pm.provider }

// ProviderCustomerID returns the provider's customer token (e.g., cus_xxx).
func (pm *PaymentMethod) ProviderCustomerID() string { return pm.providerCustomerID }

// ProviderMethodID returns the provider's payment method token (e.g., pm_xxx).
func (pm *PaymentMethod) ProviderMethodID() string { return pm.providerMethodID }

// MethodType returns the type of payment method (CARD, BANK_ACCOUNT, SEPA).
func (pm *PaymentMethod) MethodType() PaymentMethodType { return pm.methodType }

// IsDefault returns whether this is the party's default payment method.
func (pm *PaymentMethod) IsDefault() bool { return pm.isDefault }

// Metadata returns non-sensitive display information about the payment method.
func (pm *PaymentMethod) Metadata() *PaymentMethodMetadata { return pm.metadata }

// Status returns the current lifecycle status of the payment method.
func (pm *PaymentMethod) Status() PaymentMethodStatus { return pm.status }

// CreatedAt returns when the payment method was created.
func (pm *PaymentMethod) CreatedAt() time.Time { return pm.createdAt }

// UpdatedAt returns when the payment method was last updated.
func (pm *PaymentMethod) UpdatedAt() time.Time { return pm.updatedAt }

// Version returns the optimistic locking version.
func (pm *PaymentMethod) Version() int64 { return pm.version }

// SetDefault marks this payment method as the default for the party.
// Cannot set a removed or expired method as default.
func (pm *PaymentMethod) SetDefault(isDefault bool) error {
	if pm.status == PaymentMethodStatusRemoved {
		return ErrPaymentMethodRemoved
	}
	if isDefault && pm.status == PaymentMethodStatusExpired {
		return ErrPaymentMethodExpired
	}

	pm.isDefault = isDefault
	pm.updatedAt = time.Now()
	pm.version++
	return nil
}

// Expire transitions the payment method to EXPIRED status.
// If the method is currently default, it is also unset as default.
func (pm *PaymentMethod) Expire() error {
	if pm.status == PaymentMethodStatusRemoved {
		return ErrPaymentMethodRemoved
	}

	pm.status = PaymentMethodStatusExpired
	pm.isDefault = false
	pm.updatedAt = time.Now()
	pm.version++
	return nil
}

// Remove transitions the payment method to REMOVED status (soft delete).
// If the method is currently default, it is also unset as default.
func (pm *PaymentMethod) Remove() error {
	if pm.status == PaymentMethodStatusRemoved {
		return ErrPaymentMethodRemoved
	}

	pm.status = PaymentMethodStatusRemoved
	pm.isDefault = false
	pm.updatedAt = time.Now()
	pm.version++
	return nil
}
