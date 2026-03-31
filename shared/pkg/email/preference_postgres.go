package email

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/db"
	"gorm.io/gorm"
)

var _ PreferenceRepository = (*PostgresPreferenceRepository)(nil)

// communicationPreferenceEntity is the GORM model for the communication_preferences table.
type communicationPreferenceEntity struct {
	ID               uuid.UUID `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID         string    `gorm:"not null"`
	PartyID          string    `gorm:"not null"`
	Channel          string    `gorm:"not null;size:50"`
	Category         string    `gorm:"not null;size:50"`
	OptedIn          bool      `gorm:"not null"`
	ConsentSource    string    `gorm:"not null;size:255"`
	ConsentGrantedAt time.Time `gorm:"not null"`
	ConsentText      string    `gorm:"not null;type:text"`
	UpdatedAt        time.Time `gorm:"not null"`
	CreatedAt        time.Time `gorm:"not null"`
}

func (communicationPreferenceEntity) TableName() string {
	return "communication_preferences"
}

// partyGlobalUnsubscribeEntity is the GORM model for the party_global_unsubscribe table.
type partyGlobalUnsubscribeEntity struct {
	ID           uuid.UUID `gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	TenantID     string    `gorm:"not null"`
	PartyID      string    `gorm:"not null"`
	Unsubscribed bool      `gorm:"not null;default:false"`
	UpdatedAt    time.Time `gorm:"not null"`
	CreatedAt    time.Time `gorm:"not null"`
}

func (partyGlobalUnsubscribeEntity) TableName() string {
	return "party_global_unsubscribe"
}

// PostgresPreferenceRepository implements PreferenceRepository using GORM.
type PostgresPreferenceRepository struct {
	db *gorm.DB
}

// NewPostgresPreferenceRepository creates a new preference repository.
func NewPostgresPreferenceRepository(gormDB *gorm.DB) *PostgresPreferenceRepository {
	return &PostgresPreferenceRepository{db: gormDB}
}

// GetGlobalUnsubscribe returns whether the party has globally unsubscribed.
// Returns false when no row exists.
func (r *PostgresPreferenceRepository) GetGlobalUnsubscribe(ctx context.Context, tenantID, partyID string) (bool, error) {
	var unsubscribed bool
	err := db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		var entity partyGlobalUnsubscribeEntity
		if err := tx.Where("tenant_id = ? AND party_id = ?", tenantID, partyID).
			First(&entity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		unsubscribed = entity.Unsubscribed
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("email: failed to check global unsubscribe: %w", err)
	}
	return unsubscribed, nil
}

// GetPreference returns the preference for a specific channel+category pair.
// Returns nil, nil when no row exists.
func (r *PostgresPreferenceRepository) GetPreference(ctx context.Context, tenantID, partyID, channel, category string) (*CommunicationPreference, error) {
	var pref *CommunicationPreference
	err := db.WithGormTenantTransaction(ctx, r.db, func(tx *gorm.DB) error {
		var entity communicationPreferenceEntity
		if err := tx.Where("tenant_id = ? AND party_id = ? AND channel = ? AND category = ?",
			tenantID, partyID, channel, category).
			First(&entity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		pref = &CommunicationPreference{
			TenantID:         entity.TenantID,
			PartyID:          entity.PartyID,
			Channel:          entity.Channel,
			Category:         entity.Category,
			OptedIn:          entity.OptedIn,
			ConsentSource:    entity.ConsentSource,
			ConsentGrantedAt: entity.ConsentGrantedAt,
			ConsentText:      entity.ConsentText,
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("email: failed to get preference: %w", err)
	}
	return pref, nil
}
