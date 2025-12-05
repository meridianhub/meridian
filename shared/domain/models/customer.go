// Package models defines domain models for the Meridian banking platform.
package models

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

// TableName overrides the table name used by Customer to `current_account.customers`
func (Customer) TableName() string {
	return "current_account.customers"
}
