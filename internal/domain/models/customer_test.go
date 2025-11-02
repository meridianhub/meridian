package models

import (
	"testing"

	"github.com/google/uuid"
)

func TestCustomer_TableName(t *testing.T) {
	customer := Customer{}
	if customer.TableName() != "customers" {
		t.Errorf("TableName() = %v, want %v", customer.TableName(), "customers")
	}
}

func TestCustomer_Creation(t *testing.T) {
	customer := Customer{
		BaseModel: BaseModel{
			ID: uuid.New(),
		},
		CustomerNumber: "CUST001",
		FirstName:      "John",
		LastName:       "Doe",
		Email:          "john.doe@example.com",
		Phone:          "+44123456789",
		Status:         "active",
	}

	if customer.ID == uuid.Nil {
		t.Error("ID should not be Nil")
	}

	if customer.CustomerNumber != "CUST001" {
		t.Errorf("CustomerNumber = %v, want CUST001", customer.CustomerNumber)
	}

	if customer.FirstName != "John" {
		t.Errorf("FirstName = %v, want John", customer.FirstName)
	}

	if customer.LastName != "Doe" {
		t.Errorf("LastName = %v, want Doe", customer.LastName)
	}

	if customer.Email != "john.doe@example.com" {
		t.Errorf("Email = %v, want john.doe@example.com", customer.Email)
	}

	if customer.Phone != "+44123456789" {
		t.Errorf("Phone = %v, want +44123456789", customer.Phone)
	}

	if customer.Status != "active" {
		t.Errorf("Status = %v, want active", customer.Status)
	}
}

func TestCustomer_DefaultValues(t *testing.T) {
	customer := Customer{}

	if customer.CustomerNumber != "" {
		t.Errorf("Default CustomerNumber should be empty, got %v", customer.CustomerNumber)
	}

	if customer.FirstName != "" {
		t.Errorf("Default FirstName should be empty, got %v", customer.FirstName)
	}

	if customer.LastName != "" {
		t.Errorf("Default LastName should be empty, got %v", customer.LastName)
	}

	if customer.Email != "" {
		t.Errorf("Default Email should be empty, got %v", customer.Email)
	}

	if customer.Phone != "" {
		t.Errorf("Default Phone should be empty, got %v", customer.Phone)
	}

	if customer.Status != "" {
		t.Errorf("Default Status should be empty, got %v", customer.Status)
	}
}

func TestCustomer_WithAccounts(t *testing.T) {
	customerID := uuid.New()
	account1ID := uuid.New()
	account2ID := uuid.New()

	customer := Customer{
		BaseModel: BaseModel{
			ID: customerID,
		},
		CustomerNumber: "CUST002",
		FirstName:      "Jane",
		LastName:       "Smith",
		Status:         "active",
		Accounts: []Account{
			{
				BaseModel: BaseModel{
					ID: account1ID,
				},
				AccountNumber: "GB29NWBK60161331926819",
				CustomerID:    customerID,
			},
			{
				BaseModel: BaseModel{
					ID: account2ID,
				},
				AccountNumber: "GB82WEST12345698765432",
				CustomerID:    customerID,
			},
		},
	}

	if len(customer.Accounts) != 2 {
		t.Errorf("Expected 2 accounts, got %d", len(customer.Accounts))
	}

	if customer.Accounts[0].CustomerID != customerID {
		t.Error("Account 1 CustomerID should match customer ID")
	}

	if customer.Accounts[1].CustomerID != customerID {
		t.Error("Account 2 CustomerID should match customer ID")
	}
}

func TestCustomer_StatusValues(t *testing.T) {
	tests := []struct {
		name   string
		status string
	}{
		{
			name:   "active status",
			status: "active",
		},
		{
			name:   "suspended status",
			status: "suspended",
		},
		{
			name:   "closed status",
			status: "closed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			customer := Customer{
				Status: tt.status,
			}

			if customer.Status != tt.status {
				t.Errorf("Status = %v, want %v", customer.Status, tt.status)
			}
		})
	}
}
