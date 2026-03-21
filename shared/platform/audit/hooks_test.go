package audit

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// testEntity is a mock entity that implements the Auditable interface.
type testEntity struct {
	ID        uuid.UUID `gorm:"type:uuid;primaryKey"`
	Name      string    `gorm:"type:varchar(100)"`
	Status    string    `gorm:"type:varchar(20)"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (e testEntity) AuditID() string {
	return e.ID.String()
}

func (e testEntity) AuditTableName() string {
	return "test_entity"
}

func (testEntity) TableName() string {
	return "test_entity"
}

// testStringIDEntity is a mock entity with a string ID.
type testStringIDEntity struct {
	ID        string `gorm:"type:varchar(50);primaryKey"`
	Name      string `gorm:"type:varchar(100)"`
	CreatedAt time.Time
}

func (e testStringIDEntity) AuditID() string {
	return e.ID
}

func (e testStringIDEntity) AuditTableName() string {
	return "test_string_entity"
}

func (testStringIDEntity) TableName() string {
	return "test_string_entity"
}

// testAuditOutbox is a SQLite-compatible version of AuditOutbox for testing.
// SQLite doesn't support PostgreSQL-specific types like uuid, jsonb, or gen_random_uuid().
type testAuditOutbox struct {
	ID            string `gorm:"primaryKey"`
	Table         string `gorm:"column:table_name"`
	Operation     string
	RecordID      string
	OldValues     string
	NewValues     string
	Status        string
	CreatedAt     time.Time
	RetryCount    int
	LastError     *string
	ChangedBy     *string
	TransactionID *string
	ClientIP      *string
	UserAgent     *string
}

func (testAuditOutbox) TableName() string {
	return "audit_outbox"
}

// setupHooksTestDB creates an in-memory SQLite database for testing hooks.
func setupHooksTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Create tables - use testAuditOutbox which is SQLite-compatible
	err = db.AutoMigrate(&testEntity{}, &testStringIDEntity{}, &testAuditOutbox{})
	require.NoError(t, err)

	return db
}

func TestRecordCreate(t *testing.T) {
	db := setupHooksTestDB(t)

	t.Run("records INSERT audit entry", func(t *testing.T) {
		entity := testEntity{
			ID:        uuid.New(),
			Name:      "Test Entity",
			Status:    "active",
			CreatedAt: time.Now(),
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return RecordCreate(tx, entity)
		})
		require.NoError(t, err)

		// Verify audit outbox entry was created
		var outbox testAuditOutbox
		err = db.First(&outbox).Error
		require.NoError(t, err)

		assert.Equal(t, "test_entity", outbox.Table)
		assert.Equal(t, "INSERT", outbox.Operation)
		assert.Equal(t, entity.ID.String(), outbox.RecordID)
		assert.Empty(t, outbox.OldValues)
		assert.NotEmpty(t, outbox.NewValues)
		assert.Equal(t, "pending", outbox.Status)
		assert.NotNil(t, outbox.ChangedBy)
		assert.Equal(t, DefaultAuditUser, *outbox.ChangedBy)

		// Verify new values JSON contains entity data
		var newVals map[string]interface{}
		err = json.Unmarshal([]byte(outbox.NewValues), &newVals)
		require.NoError(t, err)
		assert.Equal(t, "Test Entity", newVals["Name"])
	})

	t.Run("records with user from context", func(t *testing.T) {
		entity := testEntity{
			ID:     uuid.New(),
			Name:   "Test with User",
			Status: "active",
		}

		ctx := context.WithValue(context.Background(), auth.UserIDContextKey, "user-123")

		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return RecordCreate(tx, entity)
		})
		require.NoError(t, err)

		// Find the most recent audit entry
		var outbox testAuditOutbox
		err = db.Order("created_at DESC").First(&outbox).Error
		require.NoError(t, err)

		assert.NotNil(t, outbox.ChangedBy)
		assert.Equal(t, "user-123", *outbox.ChangedBy)
	})

	t.Run("works with string ID entity", func(t *testing.T) {
		entity := testStringIDEntity{
			ID:   "tenant-abc",
			Name: "String ID Entity",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return RecordCreate(tx, entity)
		})
		require.NoError(t, err)

		var outbox testAuditOutbox
		err = db.Order("created_at DESC").First(&outbox).Error
		require.NoError(t, err)

		assert.Equal(t, "test_string_entity", outbox.Table)
		assert.Equal(t, "tenant-abc", outbox.RecordID)
	})
}

func TestRecordDelete(t *testing.T) {
	db := setupHooksTestDB(t)

	t.Run("records DELETE audit entry", func(t *testing.T) {
		entity := testEntity{
			ID:     uuid.New(),
			Name:   "Entity to Delete",
			Status: "deleted",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return RecordDelete(tx, entity)
		})
		require.NoError(t, err)

		var outbox testAuditOutbox
		err = db.First(&outbox).Error
		require.NoError(t, err)

		assert.Equal(t, "test_entity", outbox.Table)
		assert.Equal(t, "DELETE", outbox.Operation)
		assert.Equal(t, entity.ID.String(), outbox.RecordID)
		assert.NotEmpty(t, outbox.OldValues)
		assert.Empty(t, outbox.NewValues)
		assert.Equal(t, "pending", outbox.Status)

		// Verify old values JSON contains entity data
		var oldVals map[string]interface{}
		err = json.Unmarshal([]byte(outbox.OldValues), &oldVals)
		require.NoError(t, err)
		assert.Equal(t, "Entity to Delete", oldVals["Name"])
	})
}

func TestCaptureOldValue(t *testing.T) {
	db := setupHooksTestDB(t)

	t.Run("captures old value in context", func(t *testing.T) {
		// Create an entity in the database first
		original := testEntity{
			ID:        uuid.New(),
			Name:      "Original Name",
			Status:    "active",
			CreatedAt: time.Now(),
		}
		err := db.Create(&original).Error
		require.NoError(t, err)

		// Simulate BeforeUpdate: capture old value
		updated := testEntity{
			ID:     original.ID,
			Name:   "Updated Name",
			Status: "modified",
		}

		err = db.Transaction(func(tx *gorm.DB) error {
			if err := CaptureOldValue(tx, updated); err != nil {
				return err
			}

			// Verify old value is in context
			key := oldValueKey(updated.AuditTableName())
			oldVal := tx.Statement.Context.Value(key)
			assert.NotNil(t, oldVal)

			typedOld, ok := oldVal.(testEntity)
			assert.True(t, ok)
			assert.Equal(t, "Original Name", typedOld.Name)
			assert.Equal(t, "active", typedOld.Status)

			return nil
		})
		require.NoError(t, err)
	})

	t.Run("skips capture for nil UUID", func(t *testing.T) {
		entity := testEntity{
			ID:   uuid.Nil,
			Name: "No ID",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return CaptureOldValue(tx, entity)
		})
		assert.NoError(t, err)
	})

	t.Run("skips capture for empty string ID", func(t *testing.T) {
		entity := testStringIDEntity{
			ID:   "",
			Name: "No ID",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return CaptureOldValue(tx, entity)
		})
		assert.NoError(t, err)
	})
}

func TestRecordUpdate(t *testing.T) {
	db := setupHooksTestDB(t)

	t.Run("records UPDATE with old and new values", func(t *testing.T) {
		// Create original entity
		original := testEntity{
			ID:        uuid.New(),
			Name:      "Before Update",
			Status:    "pending",
			CreatedAt: time.Now(),
		}
		err := db.Create(&original).Error
		require.NoError(t, err)

		// Simulate the full update flow
		updated := testEntity{
			ID:        original.ID,
			Name:      "After Update",
			Status:    "completed",
			UpdatedAt: time.Now(),
		}

		err = db.Transaction(func(tx *gorm.DB) error {
			// Capture old value (simulates BeforeUpdate)
			if err := CaptureOldValue(tx, updated); err != nil {
				return err
			}

			// Record update (simulates AfterUpdate)
			return RecordUpdate(tx, updated)
		})
		require.NoError(t, err)

		// Verify audit entry
		var outbox testAuditOutbox
		err = db.First(&outbox).Error
		require.NoError(t, err)

		assert.Equal(t, "test_entity", outbox.Table)
		assert.Equal(t, "UPDATE", outbox.Operation)
		assert.NotEmpty(t, outbox.OldValues)
		assert.NotEmpty(t, outbox.NewValues)

		// Verify old values
		var oldVals map[string]interface{}
		err = json.Unmarshal([]byte(outbox.OldValues), &oldVals)
		require.NoError(t, err)
		assert.Equal(t, "Before Update", oldVals["Name"])
		assert.Equal(t, "pending", oldVals["Status"])

		// Verify new values
		var newVals map[string]interface{}
		err = json.Unmarshal([]byte(outbox.NewValues), &newVals)
		require.NoError(t, err)
		assert.Equal(t, "After Update", newVals["Name"])
		assert.Equal(t, "completed", newVals["Status"])
	})

	t.Run("skips update for nil UUID", func(t *testing.T) {
		// Use a fresh database for this test
		freshDB := setupHooksTestDB(t)

		entity := testEntity{
			ID:   uuid.Nil,
			Name: "No ID",
		}

		err := freshDB.Transaction(func(tx *gorm.DB) error {
			return RecordUpdate(tx, entity)
		})
		assert.NoError(t, err)

		// Verify no audit entry was created
		var count int64
		freshDB.Model(&testAuditOutbox{}).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	t.Run("skips update when old value not captured", func(t *testing.T) {
		// Use a fresh database for this test
		freshDB := setupHooksTestDB(t)

		entity := testEntity{
			ID:     uuid.New(),
			Name:   "Missing Old",
			Status: "active",
		}

		err := freshDB.Transaction(func(tx *gorm.DB) error {
			// Call RecordUpdate without calling CaptureOldValue first
			return RecordUpdate(tx, entity)
		})
		assert.NoError(t, err)

		// Verify no audit entry was created
		var count int64
		freshDB.Model(&testAuditOutbox{}).Count(&count)
		assert.Equal(t, int64(0), count)
	})
}

func TestOldValueKey(t *testing.T) {
	t.Run("generates unique keys for different tables", func(t *testing.T) {
		key1 := oldValueKey("customer")
		key2 := oldValueKey("account")

		assert.NotEqual(t, key1, key2)
		assert.Equal(t, contextKey("audit:old_value:customer"), key1)
		assert.Equal(t, contextKey("audit:old_value:account"), key2)
	})
}

func TestRecordAuditNilTransaction(t *testing.T) {
	entity := testEntity{
		ID:   uuid.New(),
		Name: "Test",
	}

	err := RecordCreate[testEntity](nil, entity)
	assert.ErrorIs(t, err, ErrNilTransaction)

	err = RecordUpdate[testEntity](nil, entity)
	assert.ErrorIs(t, err, ErrNilTransaction)

	err = RecordDelete[testEntity](nil, entity)
	assert.ErrorIs(t, err, ErrNilTransaction)
}

func TestAuditOutboxTableName(t *testing.T) {
	outbox := AuditOutbox{}
	assert.Equal(t, "audit_outbox", outbox.TableName())
}

func TestRecordUpdateManual(t *testing.T) {
	db := setupHooksTestDB(t)

	t.Run("records UPDATE with explicit old and new values", func(t *testing.T) {
		oldEntity := testEntity{
			ID:     uuid.New(),
			Name:   "Old Name",
			Status: "pending",
		}
		newEntity := testEntity{
			ID:     oldEntity.ID,
			Name:   "New Name",
			Status: "active",
		}

		err := db.Transaction(func(tx *gorm.DB) error {
			return RecordUpdateManual(tx, oldEntity, newEntity)
		})
		require.NoError(t, err)

		var outbox testAuditOutbox
		err = db.Order("created_at DESC").First(&outbox).Error
		require.NoError(t, err)

		assert.Equal(t, "test_entity", outbox.Table)
		assert.Equal(t, OperationUpdate, outbox.Operation)
		assert.Equal(t, oldEntity.ID.String(), outbox.RecordID)
		assert.NotEmpty(t, outbox.OldValues)
		assert.NotEmpty(t, outbox.NewValues)

		var oldVals map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(outbox.OldValues), &oldVals))
		assert.Equal(t, "Old Name", oldVals["Name"])

		var newVals map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(outbox.NewValues), &newVals))
		assert.Equal(t, "New Name", newVals["Name"])
	})

	t.Run("returns error on nil transaction", func(t *testing.T) {
		oldEntity := testEntity{ID: uuid.New(), Name: "Old"}
		newEntity := testEntity{ID: oldEntity.ID, Name: "New"}

		err := RecordUpdateManual[testEntity](nil, oldEntity, newEntity)
		assert.ErrorIs(t, err, ErrNilTransaction)
	})
}
