package models

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestBaseModel_BeforeCreate(t *testing.T) {
	tests := []struct {
		name    string
		initial uuid.UUID
		wantNil bool
	}{
		{
			name:    "generates UUID when nil",
			initial: uuid.Nil,
			wantNil: false,
		},
		{
			name:    "preserves existing UUID",
			initial: uuid.New(),
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := &BaseModel{ID: tt.initial}
			err := base.BeforeCreate(nil)
			if err != nil {
				t.Errorf("BeforeCreate() error = %v", err)
			}

			if tt.initial == uuid.Nil {
				if base.ID == uuid.Nil {
					t.Error("BeforeCreate() should generate UUID when initial is Nil")
				}
			} else {
				if base.ID != tt.initial {
					t.Error("BeforeCreate() should preserve existing UUID")
				}
			}
		})
	}
}

func TestBaseModel_Fields(t *testing.T) {
	base := BaseModel{
		ID:        uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if base.ID == uuid.Nil {
		t.Error("ID should not be Nil")
	}

	if base.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}

	if base.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}

	if base.DeletedAt != nil {
		t.Error("DeletedAt should be nil by default")
	}
}

func TestBaseModel_SoftDelete(t *testing.T) {
	now := time.Now()
	base := BaseModel{
		ID:        uuid.New(),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		DeletedAt: &now,
	}

	if base.DeletedAt == nil {
		t.Error("DeletedAt should be set")
	}

	if !base.DeletedAt.Equal(now) {
		t.Error("DeletedAt should match the set time")
	}
}

func TestBaseModel_BeforeCreate_WithGormDB(t *testing.T) {
	// Test that BeforeCreate accepts *gorm.DB parameter
	base := &BaseModel{}
	var db *gorm.DB // nil is fine for this test

	err := base.BeforeCreate(db)
	if err != nil {
		t.Errorf("BeforeCreate() with gorm.DB error = %v", err)
	}

	if base.ID == uuid.Nil {
		t.Error("BeforeCreate() should generate UUID")
	}
}
