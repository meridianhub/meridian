package domain

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Mock implementations to verify interfaces compile correctly

// mockDataSetRepository is a minimal mock for testing interface compliance.
type mockDataSetRepository struct {
	savedDataSets   []DataSetDefinition
	findByCodeFunc  func(ctx context.Context, code string) (DataSetDefinition, error)
	existsByCodeErr error
}

func (m *mockDataSetRepository) Save(_ context.Context, dataset DataSetDefinition) error {
	m.savedDataSets = append(m.savedDataSets, dataset)
	return nil
}

func (m *mockDataSetRepository) FindByCode(ctx context.Context, code string) (DataSetDefinition, error) {
	if m.findByCodeFunc != nil {
		return m.findByCodeFunc(ctx, code)
	}
	return DataSetDefinition{}, ErrDataSetNotFound
}

func (m *mockDataSetRepository) FindByCodeAndVersion(_ context.Context, _ string, _ int) (DataSetDefinition, error) {
	return DataSetDefinition{}, ErrDataSetNotFound
}

func (m *mockDataSetRepository) List(_ context.Context, _ DataSetFilters) ([]DataSetDefinition, string, error) {
	return nil, "", nil
}

func (m *mockDataSetRepository) ExistsByCode(_ context.Context, _ string) (bool, error) {
	if m.existsByCodeErr != nil {
		return false, m.existsByCodeErr
	}
	return len(m.savedDataSets) > 0, nil
}

// Verify mockDataSetRepository implements DataSetRepository
var _ DataSetRepository = (*mockDataSetRepository)(nil)

// mockObservationRepository is a minimal mock for testing interface compliance.
type mockObservationRepository struct {
	recordedObservations []MarketPriceObservation
	findByIDFunc         func(ctx context.Context, id uuid.UUID) (MarketPriceObservation, error)
}

func (m *mockObservationRepository) Record(_ context.Context, obs MarketPriceObservation) error {
	m.recordedObservations = append(m.recordedObservations, obs)
	return nil
}

func (m *mockObservationRepository) FindByID(ctx context.Context, id uuid.UUID) (MarketPriceObservation, error) {
	if m.findByIDFunc != nil {
		return m.findByIDFunc(ctx, id)
	}
	return MarketPriceObservation{}, ErrObservationNotFound
}

func (m *mockObservationRepository) Query(_ context.Context, _ ObservationQuery) ([]MarketPriceObservation, string, error) {
	return nil, "", nil
}

func (m *mockObservationRepository) GetLatest(_ context.Context, _ string, _ string) (MarketPriceObservation, error) {
	return MarketPriceObservation{}, ErrObservationNotFound
}

func (m *mockObservationRepository) RetrieveObservation(_ context.Context, _ string, _ string, _ time.Time) (MarketPriceObservation, error) {
	return MarketPriceObservation{}, ErrObservationNotFound
}

func (m *mockObservationRepository) CountByDataset(_ context.Context, _ string, _ bool) (int64, error) {
	return int64(len(m.recordedObservations)), nil
}

// Verify mockObservationRepository implements ObservationRepository
var _ ObservationRepository = (*mockObservationRepository)(nil)

// mockSourceRepository is a minimal mock for testing interface compliance.
type mockSourceRepository struct {
	savedSources   []DataSource
	findByIDFunc   func(ctx context.Context, id uuid.UUID) (DataSource, error)
	findByCodeFunc func(ctx context.Context, code string) (DataSource, error)
}

func (m *mockSourceRepository) Save(_ context.Context, source DataSource) error {
	m.savedSources = append(m.savedSources, source)
	return nil
}

func (m *mockSourceRepository) FindByID(ctx context.Context, id uuid.UUID) (DataSource, error) {
	if m.findByIDFunc != nil {
		return m.findByIDFunc(ctx, id)
	}
	return DataSource{}, ErrDataSourceNotFound
}

func (m *mockSourceRepository) FindByCode(ctx context.Context, code string) (DataSource, error) {
	if m.findByCodeFunc != nil {
		return m.findByCodeFunc(ctx, code)
	}
	return DataSource{}, ErrDataSourceNotFound
}

func (m *mockSourceRepository) List(_ context.Context, _ bool, _ int, _ string) ([]DataSource, string, error) {
	return nil, "", nil
}

func (m *mockSourceRepository) Deprecate(_ context.Context, _ string) error {
	return nil
}

func (m *mockSourceRepository) Delete(_ context.Context, _ string) error {
	return nil
}

// Verify mockSourceRepository implements SourceRepository
var _ SourceRepository = (*mockSourceRepository)(nil)

// Tests for interface compilation and basic mock behavior

func TestDataSetRepository_InterfaceCompiles(t *testing.T) {
	// This test verifies that the interface can be implemented
	var repo DataSetRepository = &mockDataSetRepository{}
	require.NotNil(t, repo)
}

func TestObservationRepository_InterfaceCompiles(t *testing.T) {
	// This test verifies that the interface can be implemented
	var repo ObservationRepository = &mockObservationRepository{}
	require.NotNil(t, repo)
}

func TestSourceRepository_InterfaceCompiles(t *testing.T) {
	// This test verifies that the interface can be implemented
	var repo SourceRepository = &mockSourceRepository{}
	require.NotNil(t, repo)
}

func TestDataSetRepository_Save(t *testing.T) {
	repo := &mockDataSetRepository{}
	ctx := context.Background()

	dataset, err := NewDataSetDefinition(
		"LBMA_GOLD_PRICE",
		"LBMA Gold Price",
		"London Bullion Market Association Gold Price",
		DataCategoryPricing,
		"value > 0",
		"instrument + ':' + date",
		"Invalid price for ${instrument}",
	)
	require.NoError(t, err)

	err = repo.Save(ctx, dataset)
	require.NoError(t, err)

	assert.Len(t, repo.savedDataSets, 1)
	assert.Equal(t, "LBMA_GOLD_PRICE", repo.savedDataSets[0].Code())
}

func TestDataSetRepository_FindByCode_NotFound(t *testing.T) {
	repo := &mockDataSetRepository{}
	ctx := context.Background()

	_, err := repo.FindByCode(ctx, "NONEXISTENT")
	assert.ErrorIs(t, err, ErrDataSetNotFound)
}

func TestObservationRepository_Record(t *testing.T) {
	repo := &mockObservationRepository{}
	ctx := context.Background()

	now := time.Now()
	obs, err := NewMarketPriceObservation(
		"LBMA_GOLD_PRICE",
		uuid.New(),
		"XAU:2024-01-15",
		decimal.NewFromFloat(2024.50),
		"USD/oz",
		now,
		now,
		now.Add(24*time.Hour),
		uuid.New(),
		QualityLevelActual,
		85,
		ObservationContext{},
	)
	require.NoError(t, err)

	err = repo.Record(ctx, obs)
	require.NoError(t, err)

	assert.Len(t, repo.recordedObservations, 1)
	assert.Equal(t, "LBMA_GOLD_PRICE", repo.recordedObservations[0].DataSetCode())
}

func TestObservationRepository_FindByID_NotFound(t *testing.T) {
	repo := &mockObservationRepository{}
	ctx := context.Background()

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrObservationNotFound)
}

func TestSourceRepository_Save(t *testing.T) {
	repo := &mockSourceRepository{}
	ctx := context.Background()

	source, err := NewDataSource(
		"BLOOMBERG",
		"Bloomberg Market Data",
		"Real-time market data from Bloomberg",
		SourceTypeAPI,
		90,
	)
	require.NoError(t, err)

	err = repo.Save(ctx, source)
	require.NoError(t, err)

	assert.Len(t, repo.savedSources, 1)
	assert.Equal(t, "BLOOMBERG", repo.savedSources[0].Code())
}

func TestSourceRepository_FindByID_NotFound(t *testing.T) {
	repo := &mockSourceRepository{}
	ctx := context.Background()

	_, err := repo.FindByID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrDataSourceNotFound)
}

func TestSourceRepository_FindByCode_NotFound(t *testing.T) {
	repo := &mockSourceRepository{}
	ctx := context.Background()

	_, err := repo.FindByCode(ctx, "NONEXISTENT")
	assert.ErrorIs(t, err, ErrDataSourceNotFound)
}

// Tests for filter/query structs

func TestDataSetFilters_Initialization(t *testing.T) {
	// Test zero value initialization
	var filters DataSetFilters
	assert.Nil(t, filters.Category)
	assert.Nil(t, filters.Status)
	assert.Equal(t, 0, filters.Limit)
	assert.Equal(t, "", filters.PageToken)

	// Test with values
	category := DataCategoryPricing
	status := DataSetStatusActive
	filtersWithValues := DataSetFilters{
		Category:  &category,
		Status:    &status,
		Limit:     100,
		PageToken: "1234567890_550e8400-e29b-41d4-a716-446655440000",
	}

	assert.Equal(t, DataCategoryPricing, *filtersWithValues.Category)
	assert.Equal(t, DataSetStatusActive, *filtersWithValues.Status)
	assert.Equal(t, 100, filtersWithValues.Limit)
	assert.Equal(t, "1234567890_550e8400-e29b-41d4-a716-446655440000", filtersWithValues.PageToken)
}

func TestObservationQuery_Initialization(t *testing.T) {
	// Test zero value initialization
	var query ObservationQuery
	assert.Equal(t, "", query.DataSetCode)
	assert.Nil(t, query.ResolutionKey)
	assert.Nil(t, query.ObservedAfter)
	assert.Nil(t, query.ObservedBefore)
	assert.Nil(t, query.QualityLevel)
	assert.False(t, query.IncludeSuperseded)
	assert.Equal(t, 0, query.Limit)

	// Test with values
	resolutionKey := "XAU:2024-01-15"
	observedAfter := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	observedBefore := time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC)
	qualityLevel := QualityLevelVerified

	queryWithValues := ObservationQuery{
		DataSetCode:       "LBMA_GOLD_PRICE",
		ResolutionKey:     &resolutionKey,
		ObservedAfter:     &observedAfter,
		ObservedBefore:    &observedBefore,
		QualityLevel:      &qualityLevel,
		IncludeSuperseded: true,
		Limit:             50,
	}

	assert.Equal(t, "LBMA_GOLD_PRICE", queryWithValues.DataSetCode)
	assert.Equal(t, "XAU:2024-01-15", *queryWithValues.ResolutionKey)
	assert.Equal(t, observedAfter, *queryWithValues.ObservedAfter)
	assert.Equal(t, observedBefore, *queryWithValues.ObservedBefore)
	assert.Equal(t, QualityLevelVerified, *queryWithValues.QualityLevel)
	assert.True(t, queryWithValues.IncludeSuperseded)
	assert.Equal(t, 50, queryWithValues.Limit)
}

func TestObservationQuery_PartialFilters(t *testing.T) {
	// Test query with only some filters set (common use case)
	query := ObservationQuery{
		DataSetCode: "LBMA_GOLD_PRICE",
		Limit:       100,
	}

	assert.Equal(t, "LBMA_GOLD_PRICE", query.DataSetCode)
	assert.Nil(t, query.ResolutionKey)
	assert.Nil(t, query.ObservedAfter)
	assert.Nil(t, query.ObservedBefore)
	assert.Nil(t, query.QualityLevel)
	assert.False(t, query.IncludeSuperseded)
	assert.Equal(t, 100, query.Limit)
}

// Tests for domain errors

func TestDomainErrors_AreDistinct(t *testing.T) {
	// Verify that each error is distinct and can be identified with errors.Is
	errorsToTest := []error{
		ErrDataSetNotFound,
		ErrDataSetDeprecated,
		ErrInvalidDataCategory,
		ErrInvalidDataSetStatus,
		ErrDuplicateDataSetCode,
		ErrVersionMismatch,
		ErrObservationNotFound,
		ErrDataSourceNotFound,
		ErrDuplicateDataSourceCode,
	}

	// Create a map to check uniqueness
	seen := make(map[string]bool)
	for _, err := range errorsToTest {
		errStr := err.Error()
		assert.False(t, seen[errStr], "duplicate error message: %s", errStr)
		seen[errStr] = true
	}
}

func TestDomainErrors_WorkWithErrorsIs(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		target   error
		expected bool
	}{
		{
			name:     "ErrDataSetNotFound matches itself",
			err:      ErrDataSetNotFound,
			target:   ErrDataSetNotFound,
			expected: true,
		},
		{
			name:     "ErrObservationNotFound matches itself",
			err:      ErrObservationNotFound,
			target:   ErrObservationNotFound,
			expected: true,
		},
		{
			name:     "ErrDataSourceNotFound matches itself",
			err:      ErrDataSourceNotFound,
			target:   ErrDataSourceNotFound,
			expected: true,
		},
		{
			name:     "ErrDataSetNotFound does not match ErrObservationNotFound",
			err:      ErrDataSetNotFound,
			target:   ErrObservationNotFound,
			expected: false,
		},
		{
			name:     "ErrDuplicateDataSetCode does not match ErrDuplicateDataSourceCode",
			err:      ErrDuplicateDataSetCode,
			target:   ErrDuplicateDataSourceCode,
			expected: false,
		},
		{
			name:     "ErrVersionMismatch matches itself",
			err:      ErrVersionMismatch,
			target:   ErrVersionMismatch,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := errors.Is(tt.err, tt.target)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestDomainErrors_WrappedErrorsWorkWithErrorsIs(t *testing.T) {
	// Test that wrapped errors can still be identified using fmt.Errorf
	wrappedDataSetErr := fmt.Errorf("operation failed: %w", ErrDataSetNotFound)
	// Proper wrapping with fmt.Errorf and %w works
	assert.True(t, errors.Is(wrappedDataSetErr, ErrDataSetNotFound))
}

func TestDomainErrors_HaveDescriptiveMessages(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		containsKeyword string
	}{
		{
			name:            "ErrDataSetNotFound mentions dataset",
			err:             ErrDataSetNotFound,
			containsKeyword: "dataset",
		},
		{
			name:            "ErrObservationNotFound mentions observation",
			err:             ErrObservationNotFound,
			containsKeyword: "observation",
		},
		{
			name:            "ErrDataSourceNotFound mentions source",
			err:             ErrDataSourceNotFound,
			containsKeyword: "source",
		},
		{
			name:            "ErrVersionMismatch mentions version",
			err:             ErrVersionMismatch,
			containsKeyword: "version",
		},
		{
			name:            "ErrDuplicateDataSetCode mentions code",
			err:             ErrDuplicateDataSetCode,
			containsKeyword: "code",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Contains(t, tt.err.Error(), tt.containsKeyword)
		})
	}
}

// Tests for repository interface method signatures

func TestDataSetRepository_MethodSignatures(t *testing.T) {
	// This test documents the expected method signatures
	var repo DataSetRepository = &mockDataSetRepository{}
	ctx := context.Background()

	// Save takes context and DataSetDefinition, returns error
	saveErr := repo.Save(ctx, DataSetDefinition{})
	require.NoError(t, saveErr)

	// FindByCode takes context and string, returns (DataSetDefinition, error)
	_, findErr := repo.FindByCode(ctx, "code")
	require.Error(t, findErr) // Expected: ErrDataSetNotFound

	// FindByCodeAndVersion takes context, string, int, returns (DataSetDefinition, error)
	_, findVerErr := repo.FindByCodeAndVersion(ctx, "code", 1)
	require.Error(t, findVerErr)

	// List takes context and DataSetFilters, returns ([]DataSetDefinition, string, error)
	datasets, nextToken, listErr := repo.List(ctx, DataSetFilters{})
	require.NoError(t, listErr)
	assert.Empty(t, datasets)
	assert.Empty(t, nextToken)

	// ExistsByCode takes context and string, returns (bool, error)
	exists, existsErr := repo.ExistsByCode(ctx, "code")
	require.NoError(t, existsErr)
	assert.True(t, exists) // We saved one above
}

func TestObservationRepository_MethodSignatures(t *testing.T) {
	// This test documents the expected method signatures
	var repo ObservationRepository = &mockObservationRepository{}
	ctx := context.Background()

	// Record takes context and MarketPriceObservation, returns error
	recordErr := repo.Record(ctx, MarketPriceObservation{})
	require.NoError(t, recordErr)

	// FindByID takes context and uuid.UUID, returns (MarketPriceObservation, error)
	_, findErr := repo.FindByID(ctx, uuid.New())
	require.Error(t, findErr) // Expected: ErrObservationNotFound

	// Query takes context and ObservationQuery, returns ([]MarketPriceObservation, string, error)
	observations, nextToken, queryErr := repo.Query(ctx, ObservationQuery{})
	require.NoError(t, queryErr)
	assert.Empty(t, observations)
	assert.Empty(t, nextToken)

	// GetLatest takes context, string, string, returns (MarketPriceObservation, error)
	_, latestErr := repo.GetLatest(ctx, "code", "key")
	require.Error(t, latestErr)
}

func TestSourceRepository_MethodSignatures(t *testing.T) {
	// This test documents the expected method signatures
	var repo SourceRepository = &mockSourceRepository{}
	ctx := context.Background()

	// Save takes context and DataSource, returns error
	saveErr := repo.Save(ctx, DataSource{})
	require.NoError(t, saveErr)

	// FindByID takes context and uuid.UUID, returns (DataSource, error)
	_, findErr := repo.FindByID(ctx, uuid.New())
	require.Error(t, findErr) // Expected: ErrDataSourceNotFound

	// FindByCode takes context and string, returns (DataSource, error)
	_, codeErr := repo.FindByCode(ctx, "code")
	require.Error(t, codeErr)

	// List takes context, bool, int, string, returns ([]DataSource, string, error)
	sources, nextToken, listErr := repo.List(ctx, true, 50, "")
	require.NoError(t, listErr)
	assert.Empty(t, sources)
	assert.Empty(t, nextToken)
}

// Test that filter structs work with optional pointer fields

func TestDataSetFilters_AllCombinations(t *testing.T) {
	categories := []*DataCategory{nil, ptr(DataCategoryPricing), ptr(DataCategoryContextual)}
	statuses := []*DataSetStatus{nil, ptr(DataSetStatusDraft), ptr(DataSetStatusActive), ptr(DataSetStatusDeprecated)}

	for _, category := range categories {
		for _, status := range statuses {
			filters := DataSetFilters{
				Category:  category,
				Status:    status,
				Limit:     10,
				PageToken: "",
			}

			// Verify filters can be created and accessed without panic
			_ = filters.Category
			_ = filters.Status
			assert.Equal(t, 10, filters.Limit)
		}
	}
}

func TestObservationQuery_QualityLevelFilters(t *testing.T) {
	qualityLevels := []*QualityLevel{
		nil,
		ptr(QualityLevelEstimate),
		ptr(QualityLevelActual),
		ptr(QualityLevelVerified),
	}

	for _, ql := range qualityLevels {
		query := ObservationQuery{
			DataSetCode:  "TEST",
			QualityLevel: ql,
		}

		// Verify query can be created and accessed without panic
		_ = query.QualityLevel
		assert.Equal(t, "TEST", query.DataSetCode)
	}
}

// Helper function for creating pointers to values
func ptr[T any](v T) *T {
	return &v
}
