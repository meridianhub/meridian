package staff

import (
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// UserEntity is the GORM entity for the staff_user table.
type UserEntity struct {
	ID             uuid.UUID `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	Email          string    `gorm:"column:email;type:varchar(255);not null"`
	Name           *string   `gorm:"column:name;type:varchar(255)"`
	Role           string    `gorm:"column:role;type:varchar(50);not null;default:operator"`
	Status         string    `gorm:"column:status;type:varchar(20);not null;default:invited"`
	AuthProviderID *string   `gorm:"column:auth_provider_id;type:varchar(255)"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;default:now()"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null;default:now()"`
}

// TableName returns the table name for GORM.
func (UserEntity) TableName() string {
	return "staff_user"
}

// APIKeyEntity is the GORM entity for the api_key table.
type APIKeyEntity struct {
	ID           uuid.UUID      `gorm:"column:id;type:uuid;primaryKey;default:gen_random_uuid()"`
	StaffUserID  uuid.UUID      `gorm:"column:staff_user_id;type:uuid;not null"`
	KeyPrefix    string         `gorm:"column:key_prefix;type:varchar(100);not null"`
	KeyHash      []byte         `gorm:"column:key_hash;type:bytea;not null"`
	Name         *string        `gorm:"column:name;type:varchar(255)"`
	Scopes       pq.StringArray `gorm:"column:scopes;type:text[]"`
	RateLimitRPS int            `gorm:"column:rate_limit_rps;type:integer;not null;default:100"`
	LastUsedAt   *time.Time     `gorm:"column:last_used_at"`
	ExpiresAt    *time.Time     `gorm:"column:expires_at"`
	CreatedAt    time.Time      `gorm:"column:created_at;not null;default:now()"`
	RevokedAt    *time.Time     `gorm:"column:revoked_at"`
}

// TableName returns the table name for GORM.
func (APIKeyEntity) TableName() string {
	return "api_key"
}
