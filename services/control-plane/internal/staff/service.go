// Package staff provides tenant-scoped staff identity management for the Admin Console.
//
// Staff users are employees who operate the Admin Console, distinct from Party
// (customers with ledger positions). Each staff user exists within a tenant schema
// (org_{id}) and has a role (admin/operator/auditor) that maps to the shared
// RBAC system in shared/platform/auth.
package staff

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

// Status constants for staff user lifecycle.
const (
	StatusInvited   = "invited"
	StatusActive    = "active"
	StatusSuspended = "suspended"
)

// Errors returned by the staff service.
var (
	ErrStaffNotFound      = errors.New("staff user not found")
	ErrEmailAlreadyExists = errors.New("email already exists")
	ErrEmailRequired      = errors.New("email is required")
	ErrInvalidRole        = errors.New("invalid role: must be admin, operator, or auditor")
	ErrInvalidStatus      = errors.New("invalid status transition")
	ErrStaffSuspended     = errors.New("staff user is suspended")
	ErrAPIKeyNotFound     = errors.New("API key not found")
	ErrAPIKeyRevoked      = errors.New("API key has been revoked")
	ErrAPIKeyExpired      = errors.New("API key has expired")
	ErrInvalidAPIKey      = errors.New("invalid API key")
)

// validRoles is the set of valid staff roles.
var validRoles = map[string]bool{
	"admin":    true,
	"operator": true,
	"auditor":  true,
}

// User represents a staff user in the system.
type User struct {
	ID             uuid.UUID
	Email          string
	Name           string
	Role           string
	Status         string
	AuthProviderID string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// APIKeyResult is returned when creating an API key. PlaintextKey is only
// available at creation time and must not be stored.
type APIKeyResult struct {
	ID           uuid.UUID
	KeyPrefix    string
	PlaintextKey string
	Name         string
	Scopes       []string
	RateLimitRPS int
	ExpiresAt    *time.Time
	CreatedAt    time.Time
}

// Service provides staff identity management operations.
// All operations are tenant-scoped via context.
type Service struct {
	gormDB *gorm.DB
	logger *slog.Logger
}

// NewService creates a new staff service.
func NewService(gormDB *gorm.DB, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		gormDB: gormDB,
		logger: logger,
	}
}

// InviteStaff creates a new staff user with status=invited.
func (s *Service) InviteStaff(ctx context.Context, email, name, role string) (*User, error) {
	if !validRoles[role] {
		return nil, ErrInvalidRole
	}

	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return nil, ErrEmailRequired
	}

	now := time.Now()
	entity := &UserEntity{
		ID:        uuid.New(),
		Email:     email,
		Role:      role,
		Status:    StatusInvited,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if name != "" {
		entity.Name = &name
	}

	err := db.WithGormTenantTransaction(ctx, s.gormDB, func(tx *gorm.DB) error {
		if err := tx.Create(entity).Error; err != nil {
			if isDuplicateKeyError(err) {
				return ErrEmailAlreadyExists
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.logger.InfoContext(ctx, "staff user invited",
		"staff_id", entity.ID,
		"email", email,
		"role", role)

	return entityToUser(entity), nil
}

// ActivateStaff sets a staff user's status to active and links their auth provider ID.
func (s *Service) ActivateStaff(ctx context.Context, staffID uuid.UUID, authProviderID string) error {
	return db.WithGormTenantTransaction(ctx, s.gormDB, func(tx *gorm.DB) error {
		var entity UserEntity
		if err := tx.Where("id = ?", staffID).First(&entity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStaffNotFound
			}
			return err
		}

		if entity.Status != StatusInvited {
			return fmt.Errorf("%w: can only activate invited staff (current: %s)", ErrInvalidStatus, entity.Status)
		}

		now := time.Now()
		return tx.Model(&UserEntity{}).
			Where("id = ?", staffID).
			Updates(map[string]interface{}{
				"status":           StatusActive,
				"auth_provider_id": authProviderID,
				"updated_at":       now,
			}).Error
	})
}

// SuspendStaff sets a staff user's status to suspended.
func (s *Service) SuspendStaff(ctx context.Context, staffID uuid.UUID) error {
	return db.WithGormTenantTransaction(ctx, s.gormDB, func(tx *gorm.DB) error {
		var entity UserEntity
		if err := tx.Where("id = ?", staffID).First(&entity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStaffNotFound
			}
			return err
		}

		if entity.Status == StatusSuspended {
			return nil // already suspended, idempotent
		}

		now := time.Now()
		return tx.Model(&UserEntity{}).
			Where("id = ?", staffID).
			Updates(map[string]interface{}{
				"status":     StatusSuspended,
				"updated_at": now,
			}).Error
	})
}

// GetStaff retrieves a staff user by ID.
func (s *Service) GetStaff(ctx context.Context, staffID uuid.UUID) (*User, error) {
	var user *User
	err := db.WithGormTenantTransaction(ctx, s.gormDB, func(tx *gorm.DB) error {
		var entity UserEntity
		if err := tx.Where("id = ?", staffID).First(&entity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStaffNotFound
			}
			return err
		}
		user = entityToUser(&entity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

// ListStaff returns all staff users for the tenant.
func (s *Service) ListStaff(ctx context.Context) ([]User, error) {
	var users []User
	err := db.WithGormTenantTransaction(ctx, s.gormDB, func(tx *gorm.DB) error {
		var entities []UserEntity
		if err := tx.Order("created_at ASC").Find(&entities).Error; err != nil {
			return err
		}
		users = make([]User, len(entities))
		for i := range entities {
			users[i] = *entityToUser(&entities[i])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return users, nil
}

// CreateAPIKey generates a new API key for a staff user.
// The plaintext key is returned only once and must not be stored by the service.
func (s *Service) CreateAPIKey(ctx context.Context, staffUserID uuid.UUID, tenantSlug, name string, scopes []string, ttl time.Duration) (*APIKeyResult, error) {
	if err := s.verifyStaffNotSuspended(ctx, staffUserID); err != nil {
		return nil, err
	}

	plaintextKey, keyPrefix, keyHash, err := generateAPIKeyMaterial(tenantSlug)
	if err != nil {
		return nil, err
	}

	entity := buildAPIKeyEntity(staffUserID, keyPrefix, keyHash, name, scopes, ttl)

	err = db.WithGormTenantTransaction(ctx, s.gormDB, func(tx *gorm.DB) error {
		return tx.Create(entity).Error
	})
	if err != nil {
		return nil, err
	}

	s.logger.InfoContext(ctx, "API key created",
		"key_id", entity.ID,
		"staff_user_id", staffUserID,
		"key_prefix", keyPrefix)

	return &APIKeyResult{
		ID:           entity.ID,
		KeyPrefix:    keyPrefix,
		PlaintextKey: plaintextKey,
		Name:         name,
		Scopes:       scopes,
		RateLimitRPS: entity.RateLimitRPS,
		ExpiresAt:    entity.ExpiresAt,
		CreatedAt:    entity.CreatedAt,
	}, nil
}

// verifyStaffNotSuspended checks that the staff user exists and is not suspended.
func (s *Service) verifyStaffNotSuspended(ctx context.Context, staffUserID uuid.UUID) error {
	var staffEntity UserEntity
	err := db.WithGormTenantTransaction(ctx, s.gormDB, func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", staffUserID).First(&staffEntity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStaffNotFound
			}
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	if staffEntity.Status == StatusSuspended {
		return ErrStaffSuspended
	}
	return nil
}

// generateAPIKeyMaterial generates the cryptographic key material for an API key.
// Returns the plaintext key, prefix for routing, and SHA-256 hash.
func generateAPIKeyMaterial(tenantSlug string) (plaintextKey, keyPrefix string, keyHash [32]byte, err error) {
	keyBytes := make([]byte, 32)
	if _, err = rand.Read(keyBytes); err != nil {
		return "", "", [32]byte{}, fmt.Errorf("generate key entropy: %w", err)
	}
	entropy := base64.RawURLEncoding.EncodeToString(keyBytes)

	plaintextKey = fmt.Sprintf("pk_%s_%s", tenantSlug, entropy)
	keyPrefix = fmt.Sprintf("pk_%s_%s", tenantSlug, entropy[:8])
	keyHash = sha256.Sum256([]byte(plaintextKey))

	return plaintextKey, keyPrefix, keyHash, nil
}

// buildAPIKeyEntity constructs an APIKeyEntity from the given parameters.
func buildAPIKeyEntity(staffUserID uuid.UUID, keyPrefix string, keyHash [32]byte, name string, scopes []string, ttl time.Duration) *APIKeyEntity {
	now := time.Now()
	entity := &APIKeyEntity{
		ID:           uuid.New(),
		StaffUserID:  staffUserID,
		KeyPrefix:    keyPrefix,
		KeyHash:      keyHash[:],
		RateLimitRPS: 100,
		CreatedAt:    now,
	}
	if name != "" {
		entity.Name = &name
	}
	if len(scopes) > 0 {
		entity.Scopes = pq.StringArray(scopes)
	}
	if ttl > 0 {
		expiresAt := now.Add(ttl)
		entity.ExpiresAt = &expiresAt
	}
	return entity
}

// RevokeAPIKey marks an API key as revoked by its prefix.
func (s *Service) RevokeAPIKey(ctx context.Context, keyPrefix string) error {
	return db.WithGormTenantTransaction(ctx, s.gormDB, func(tx *gorm.DB) error {
		now := time.Now()
		result := tx.Model(&APIKeyEntity{}).
			Where("key_prefix = ? AND revoked_at IS NULL", keyPrefix).
			Update("revoked_at", now)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrAPIKeyNotFound
		}
		return nil
	})
}

// ValidateAPIKey verifies an API key and updates last_used_at.
// Returns the staff user for RBAC integration.
func (s *Service) ValidateAPIKey(ctx context.Context, keyPrefix, plaintextKey string) (*User, error) {
	var user *User

	err := db.WithGormTenantTransaction(ctx, s.gormDB, func(tx *gorm.DB) error {
		var keyEntity APIKeyEntity
		if err := tx.Where("key_prefix = ? AND revoked_at IS NULL", keyPrefix).First(&keyEntity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAPIKeyNotFound
			}
			return err
		}

		// Check expiry
		if keyEntity.ExpiresAt != nil && keyEntity.ExpiresAt.Before(time.Now()) {
			return ErrAPIKeyExpired
		}

		// Constant-time hash comparison
		expectedHash := sha256.Sum256([]byte(plaintextKey))
		if subtle.ConstantTimeCompare(keyEntity.KeyHash, expectedHash[:]) != 1 {
			return ErrInvalidAPIKey
		}

		// Update last_used_at
		now := time.Now()
		tx.Model(&APIKeyEntity{}).
			Where("id = ?", keyEntity.ID).
			Update("last_used_at", now)

		// Load staff user
		var staffEntity UserEntity
		if err := tx.Where("id = ?", keyEntity.StaffUserID).First(&staffEntity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStaffNotFound
			}
			return err
		}

		if staffEntity.Status == StatusSuspended {
			return ErrStaffSuspended
		}

		user = entityToUser(&staffEntity)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

// APIKeyValidation holds the full result of validating an API key,
// including key metadata needed by the gateway.
type APIKeyValidation struct {
	User         *User
	Scopes       []string
	RateLimitRPS int
}

// ValidateAPIKeyFull verifies an API key and returns the full validation result
// including scopes and rate limit. Used by the AuthService gRPC handler.
func (s *Service) ValidateAPIKeyFull(ctx context.Context, keyPrefix, plaintextKey string) (*APIKeyValidation, error) {
	var result *APIKeyValidation

	err := db.WithGormTenantTransaction(ctx, s.gormDB, func(tx *gorm.DB) error {
		var keyEntity APIKeyEntity
		if err := tx.Where("key_prefix = ? AND revoked_at IS NULL", keyPrefix).First(&keyEntity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAPIKeyNotFound
			}
			return err
		}

		// Check expiry
		if keyEntity.ExpiresAt != nil && keyEntity.ExpiresAt.Before(time.Now()) {
			return ErrAPIKeyExpired
		}

		// Constant-time hash comparison
		expectedHash := sha256.Sum256([]byte(plaintextKey))
		if subtle.ConstantTimeCompare(keyEntity.KeyHash, expectedHash[:]) != 1 {
			return ErrInvalidAPIKey
		}

		// Update last_used_at
		now := time.Now()
		tx.Model(&APIKeyEntity{}).
			Where("id = ?", keyEntity.ID).
			Update("last_used_at", now)

		// Load staff user
		var staffEntity UserEntity
		if err := tx.Where("id = ?", keyEntity.StaffUserID).First(&staffEntity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStaffNotFound
			}
			return err
		}

		if staffEntity.Status == StatusSuspended {
			return ErrStaffSuspended
		}

		result = &APIKeyValidation{
			User:         entityToUser(&staffEntity),
			Scopes:       []string(keyEntity.Scopes),
			RateLimitRPS: keyEntity.RateLimitRPS,
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// MapRoleToAuth converts a staff role string to the shared auth.Role type.
func MapRoleToAuth(staffRole string) (auth.Role, error) {
	switch staffRole {
	case "admin":
		return auth.RoleAdmin, nil
	case "operator":
		return auth.RoleOperator, nil
	case "auditor":
		return auth.RoleAuditor, nil
	default:
		return "", fmt.Errorf("%w: %s", ErrInvalidRole, staffRole)
	}
}

func entityToUser(e *UserEntity) *User {
	user := &User{
		ID:        e.ID,
		Email:     e.Email,
		Role:      e.Role,
		Status:    e.Status,
		CreatedAt: e.CreatedAt,
		UpdatedAt: e.UpdatedAt,
	}
	if e.Name != nil {
		user.Name = *e.Name
	}
	if e.AuthProviderID != nil {
		user.AuthProviderID = *e.AuthProviderID
	}
	return user
}

func isDuplicateKeyError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return errors.Is(err, gorm.ErrDuplicatedKey) ||
		strings.Contains(errStr, "23505") ||
		strings.Contains(errStr, "duplicate key") ||
		strings.Contains(errStr, "unique constraint")
}
