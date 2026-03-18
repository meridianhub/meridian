package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	commonv1 "github.com/meridianhub/meridian/api/proto/meridian/common/v1"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
)

func TestProtoLineageToDomain_NilInput(t *testing.T) {
	lineage, err := protoLineageToDomain(nil)
	assert.NoError(t, err)
	assert.Nil(t, lineage)
}

func TestProtoLineageToDomain_ValidInput(t *testing.T) {
	txID := uuid.New()
	parentID := uuid.New()
	child1 := uuid.New()
	child2 := uuid.New()
	related1 := uuid.New()

	proto := &positionkeepingv1.TransactionLineage{
		TransactionId:         txID.String(),
		TransactionType:       "PAYMENT",
		ParentTransactionId:   parentID.String(),
		ChildTransactionIds:   []string{child1.String(), child2.String()},
		RelatedTransactionIds: []string{related1.String()},
	}

	lineage, err := protoLineageToDomain(proto)

	require.NoError(t, err)
	require.NotNil(t, lineage)
	assert.Equal(t, txID, lineage.TransactionID())
	assert.Equal(t, "PAYMENT", lineage.TransactionType())
	require.NotNil(t, lineage.ParentTransactionID())
	assert.Equal(t, parentID, *lineage.ParentTransactionID())
	assert.Len(t, lineage.ChildTransactionIDs(), 2)
	assert.Len(t, lineage.RelatedTransactionIDs(), 1)
}

func TestProtoLineageToDomain_NoParent(t *testing.T) {
	txID := uuid.New()

	proto := &positionkeepingv1.TransactionLineage{
		TransactionId:       txID.String(),
		TransactionType:     "TRANSFER",
		ParentTransactionId: "", // No parent
	}

	lineage, err := protoLineageToDomain(proto)

	require.NoError(t, err)
	require.NotNil(t, lineage)
	assert.Nil(t, lineage.ParentTransactionID())
}

func TestProtoLineageToDomain_InvalidTransactionID(t *testing.T) {
	proto := &positionkeepingv1.TransactionLineage{
		TransactionId:   "not-a-uuid",
		TransactionType: "PAYMENT",
	}

	lineage, err := protoLineageToDomain(proto)

	require.Error(t, err)
	assert.Nil(t, lineage)
}

func TestProtoLineageToDomain_InvalidParentTransactionID(t *testing.T) {
	txID := uuid.New()

	proto := &positionkeepingv1.TransactionLineage{
		TransactionId:       txID.String(),
		TransactionType:     "PAYMENT",
		ParentTransactionId: "not-a-uuid",
	}

	lineage, err := protoLineageToDomain(proto)

	require.Error(t, err)
	assert.Nil(t, lineage)
}

func TestProtoLineageToDomain_InvalidChildTransactionID(t *testing.T) {
	txID := uuid.New()

	proto := &positionkeepingv1.TransactionLineage{
		TransactionId:       txID.String(),
		TransactionType:     "PAYMENT",
		ChildTransactionIds: []string{uuid.NewString(), "not-a-uuid"},
	}

	lineage, err := protoLineageToDomain(proto)

	require.Error(t, err)
	assert.Nil(t, lineage)
}

func TestProtoLineageToDomain_InvalidRelatedTransactionID(t *testing.T) {
	txID := uuid.New()

	proto := &positionkeepingv1.TransactionLineage{
		TransactionId:         txID.String(),
		TransactionType:       "PAYMENT",
		RelatedTransactionIds: []string{"not-a-uuid"},
	}

	lineage, err := protoLineageToDomain(proto)

	require.Error(t, err)
	assert.Nil(t, lineage)
}

func TestToProtoAccountServiceDomain(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected commonv1.AccountServiceDomain
	}{
		{
			name:     "current account",
			input:    "CURRENT_ACCOUNT",
			expected: commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_CURRENT_ACCOUNT,
		},
		{
			name:     "internal account",
			input:    "INTERNAL_ACCOUNT",
			expected: commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_INTERNAL_ACCOUNT,
		},
		{
			name:     "empty string returns unspecified",
			input:    "",
			expected: commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_UNSPECIFIED,
		},
		{
			name:     "unknown value returns unspecified",
			input:    "UNKNOWN_SERVICE",
			expected: commonv1.AccountServiceDomain_ACCOUNT_SERVICE_DOMAIN_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := toProtoAccountServiceDomain(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToProtoTransactionLogEntry_NilInput(t *testing.T) {
	result := toProtoTransactionLogEntry(nil)
	assert.Nil(t, result)
}
