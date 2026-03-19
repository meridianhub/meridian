package handler

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
)

func TestFilterByBehaviorClass(t *testing.T) {
	t.Parallel()

	defs := []*accounttype.Definition{
		{Code: "CUSTOMER_1", BehaviorClass: accounttype.BehaviorClassCustomer},
		{Code: "CLEARING_1", BehaviorClass: accounttype.BehaviorClassClearing},
		{Code: "CUSTOMER_2", BehaviorClass: accounttype.BehaviorClassCustomer},
		{Code: "NOSTRO_1", BehaviorClass: accounttype.BehaviorClassNostro},
	}

	t.Run("UNSPECIFIED returns all definitions", func(t *testing.T) {
		t.Parallel()
		result := filterByBehaviorClass(defs, pb.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED)
		assert.Len(t, result, 4)
	})

	t.Run("CUSTOMER filters to customer only", func(t *testing.T) {
		t.Parallel()
		result := filterByBehaviorClass(defs, pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER)
		assert.Len(t, result, 2)
		for _, d := range result {
			assert.Equal(t, accounttype.BehaviorClassCustomer, d.BehaviorClass)
		}
	})

	t.Run("CLEARING filters to clearing only", func(t *testing.T) {
		t.Parallel()
		result := filterByBehaviorClass(defs, pb.BehaviorClass_BEHAVIOR_CLASS_CLEARING)
		assert.Len(t, result, 1)
		assert.Equal(t, "CLEARING_1", result[0].Code)
	})

	t.Run("HOLDING returns empty when no matches", func(t *testing.T) {
		t.Parallel()
		result := filterByBehaviorClass(defs, pb.BehaviorClass_BEHAVIOR_CLASS_HOLDING)
		assert.Empty(t, result)
	})

	t.Run("empty input returns empty", func(t *testing.T) {
		t.Parallel()
		result := filterByBehaviorClass(nil, pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER)
		assert.Empty(t, result)
	})
}

func TestPaginateDefinitions(t *testing.T) {
	t.Parallel()

	makeDefs := func(codes ...string) []*accounttype.Definition {
		defs := make([]*accounttype.Definition, len(codes))
		for i, c := range codes {
			defs[i] = &accounttype.Definition{Code: c}
		}
		return defs
	}

	t.Run("empty list returns nil", func(t *testing.T) {
		t.Parallel()
		page, token := paginateDefinitions(nil, 10, "")
		assert.Nil(t, page)
		assert.Empty(t, token)
	})

	t.Run("all items fit in one page", func(t *testing.T) {
		t.Parallel()
		defs := makeDefs("A", "B", "C")
		page, token := paginateDefinitions(defs, 10, "")
		assert.Len(t, page, 3)
		assert.Empty(t, token, "no next page token when all fit")
	})

	t.Run("items exceed page size", func(t *testing.T) {
		t.Parallel()
		defs := makeDefs("A", "B", "C", "D", "E")
		page, token := paginateDefinitions(defs, 2, "")
		assert.Len(t, page, 2)
		assert.Equal(t, "B", token, "next page token is last item's code")
	})

	t.Run("page token skips past earlier items", func(t *testing.T) {
		t.Parallel()
		defs := makeDefs("A", "B", "C", "D", "E")
		page, token := paginateDefinitions(defs, 2, "B")
		assert.Len(t, page, 2)
		assert.Equal(t, "C", page[0].Code)
		assert.Equal(t, "D", page[1].Code)
		assert.Equal(t, "D", token)
	})

	t.Run("page token past all items returns nil", func(t *testing.T) {
		t.Parallel()
		defs := makeDefs("A", "B", "C")
		page, token := paginateDefinitions(defs, 10, "Z")
		assert.Nil(t, page)
		assert.Empty(t, token)
	})

	t.Run("exact boundary with page size", func(t *testing.T) {
		t.Parallel()
		defs := makeDefs("A", "B", "C")
		page, token := paginateDefinitions(defs, 3, "")
		assert.Len(t, page, 3)
		assert.Empty(t, token, "no next page when exactly full")
	})
}

func TestNormalizeAccountTypePageSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    int
		expected int
	}{
		{"zero returns default", 0, DefaultPageSize},
		{"negative returns default", -1, DefaultPageSize},
		{"valid value passes through", 25, 25},
		{"max value passes through", MaxPageSize, MaxPageSize},
		{"above max clamped to max", MaxPageSize + 1, MaxPageSize},
		{"one returns one", 1, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, normalizeAccountTypePageSize(tc.input))
		})
	}
}

func TestDomainBehaviorClassToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		domain   accounttype.BehaviorClass
		expected pb.BehaviorClass
	}{
		{"CUSTOMER", accounttype.BehaviorClassCustomer, pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER},
		{"CLEARING", accounttype.BehaviorClassClearing, pb.BehaviorClass_BEHAVIOR_CLASS_CLEARING},
		{"NOSTRO", accounttype.BehaviorClassNostro, pb.BehaviorClass_BEHAVIOR_CLASS_NOSTRO},
		{"VOSTRO", accounttype.BehaviorClassVostro, pb.BehaviorClass_BEHAVIOR_CLASS_VOSTRO},
		{"HOLDING", accounttype.BehaviorClassHolding, pb.BehaviorClass_BEHAVIOR_CLASS_HOLDING},
		{"SUSPENSE", accounttype.BehaviorClassSuspense, pb.BehaviorClass_BEHAVIOR_CLASS_SUSPENSE},
		{"REVENUE", accounttype.BehaviorClassRevenue, pb.BehaviorClass_BEHAVIOR_CLASS_REVENUE},
		{"EXPENSE", accounttype.BehaviorClassExpense, pb.BehaviorClass_BEHAVIOR_CLASS_EXPENSE},
		{"INVENTORY", accounttype.BehaviorClassInventory, pb.BehaviorClass_BEHAVIOR_CLASS_INVENTORY},
		{"unknown defaults to UNSPECIFIED", accounttype.BehaviorClass("UNKNOWN"), pb.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED},
		{"empty defaults to UNSPECIFIED", accounttype.BehaviorClass(""), pb.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, domainBehaviorClassToProto(tc.domain))
		})
	}
}

func TestProtoBehaviorClassToDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		proto    pb.BehaviorClass
		expected accounttype.BehaviorClass
	}{
		{"UNSPECIFIED", pb.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED, ""},
		{"CUSTOMER", pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER, accounttype.BehaviorClassCustomer},
		{"CLEARING", pb.BehaviorClass_BEHAVIOR_CLASS_CLEARING, accounttype.BehaviorClassClearing},
		{"NOSTRO", pb.BehaviorClass_BEHAVIOR_CLASS_NOSTRO, accounttype.BehaviorClassNostro},
		{"VOSTRO", pb.BehaviorClass_BEHAVIOR_CLASS_VOSTRO, accounttype.BehaviorClassVostro},
		{"HOLDING", pb.BehaviorClass_BEHAVIOR_CLASS_HOLDING, accounttype.BehaviorClassHolding},
		{"SUSPENSE", pb.BehaviorClass_BEHAVIOR_CLASS_SUSPENSE, accounttype.BehaviorClassSuspense},
		{"REVENUE", pb.BehaviorClass_BEHAVIOR_CLASS_REVENUE, accounttype.BehaviorClassRevenue},
		{"EXPENSE", pb.BehaviorClass_BEHAVIOR_CLASS_EXPENSE, accounttype.BehaviorClassExpense},
		{"INVENTORY", pb.BehaviorClass_BEHAVIOR_CLASS_INVENTORY, accounttype.BehaviorClassInventory},
		{"unknown value defaults to empty", pb.BehaviorClass(999), ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, protoBehaviorClassToDomain(tc.proto))
		})
	}
}

func TestParseConversionMethodPair(t *testing.T) {
	t.Parallel()

	t.Run("empty ID returns nil pointers", func(t *testing.T) {
		t.Parallel()
		id, version, err := parseConversionMethodPair("", 0)
		assert.NoError(t, err)
		assert.Nil(t, id)
		assert.Nil(t, version)
	})

	t.Run("valid ID and version", func(t *testing.T) {
		t.Parallel()
		testID := uuid.New()
		id, version, err := parseConversionMethodPair(testID.String(), 5)
		require.NoError(t, err)
		assert.Equal(t, testID, *id)
		assert.Equal(t, 5, *version)
	})

	t.Run("invalid UUID returns InvalidArgument", func(t *testing.T) {
		t.Parallel()
		_, _, err := parseConversionMethodPair("not-a-uuid", 1)
		require.Error(t, err)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "invalid default_conversion_method_id")
	})

	t.Run("version zero returns InvalidArgument", func(t *testing.T) {
		t.Parallel()
		testID := uuid.New()
		_, _, err := parseConversionMethodPair(testID.String(), 0)
		require.Error(t, err)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "must be >= 1")
	})

	t.Run("negative version returns InvalidArgument", func(t *testing.T) {
		t.Parallel()
		testID := uuid.New()
		_, _, err := parseConversionMethodPair(testID.String(), -1)
		require.Error(t, err)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("version 1 is minimum valid", func(t *testing.T) {
		t.Parallel()
		testID := uuid.New()
		id, version, err := parseConversionMethodPair(testID.String(), 1)
		require.NoError(t, err)
		assert.Equal(t, testID, *id)
		assert.Equal(t, 1, *version)
	})
}
