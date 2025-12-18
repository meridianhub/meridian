// Package models defines domain models for the Meridian banking platform.
package models

import (
	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/gorm"
)

// Customer represents a bank customer in the system
type Customer struct {
	BaseModel

	// Customer identification
	CustomerNumber string `gorm:"type:varchar(50);uniqueIndex;not null" json:"customer_number"`
	FirstName      string `gorm:"type:varchar(100);not null" json:"first_name"`
	LastName       string `gorm:"type:varchar(100);not null" json:"last_name"`
	Email          string `gorm:"type:varchar(255);uniqueIndex" json:"email,omitempty"`
	Phone          string `gorm:"type:varchar(20)" json:"phone,omitempty"`

	// Status
	Status string `gorm:"type:varchar(20);not null;default:'active'" json:"status"` // active, suspended, closed

	// Relationships
	Accounts []Account `gorm:"foreignKey:CustomerID;constraint:OnDelete:RESTRICT" json:"accounts,omitempty"`
}

// TableName overrides the table name.
// Uses singular unqualified name to allow PostgreSQL search_path to route queries
// to tenant-specific schemas (e.g., tenant_acme_bank.customer).
func (Customer) TableName() string {
	return "customer"
}

// AuditID returns the record ID as a string for audit logging.
// Implements the audit.Auditable interface.
func (c Customer) AuditID() string {
	return c.ID.String()
}

// AuditTableName returns the table name for audit logging.
// Implements the audit.Auditable interface.
func (c Customer) AuditTableName() string {
	return c.TableName()
}

// AfterCreate is a GORM hook that runs after INSERT operations on Customer.
// It writes an audit outbox entry with the new customer data.
func (c *Customer) AfterCreate(tx *gorm.DB) error {
	return audit.RecordCreate(tx, *c)
}

// BeforeUpdate is a GORM hook that runs before UPDATE operations on Customer.
// It captures the old values BEFORE the update happens and stores them in the transaction context.
func (c *Customer) BeforeUpdate(tx *gorm.DB) error {
	// First, call the base model's BeforeUpdate to handle UpdatedBy
	if err := c.BaseModel.BeforeUpdate(tx); err != nil {
		return err
	}
	return audit.CaptureOldValue(tx, *c)
}

// AfterUpdate is a GORM hook that runs after UPDATE operations on Customer.
// It retrieves the old values from context and writes an audit outbox entry.
func (c *Customer) AfterUpdate(tx *gorm.DB) error {
	return audit.RecordUpdate(tx, *c)
}

// AfterDelete is a GORM hook that runs after DELETE operations on Customer.
// It writes an audit outbox entry with the deleted customer data.
func (c *Customer) AfterDelete(tx *gorm.DB) error {
	return audit.RecordDelete(tx, *c)
}
