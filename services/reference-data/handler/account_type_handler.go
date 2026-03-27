package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	refcel "github.com/meridianhub/meridian/services/reference-data/cel"
	"github.com/meridianhub/meridian/services/reference-data/registry"
)

// AccountTypeService implements the AccountTypeRegistryService gRPC service.
type AccountTypeService struct {
	pb.UnimplementedAccountTypeRegistryServiceServer
	registry           accounttype.Registry
	instrumentRegistry registry.InstrumentRegistry
	compiler           *refcel.Compiler
	logger             *slog.Logger
}

// ErrAccountTypeRegistryNil is returned when attempting to create a service with a nil registry.
var ErrAccountTypeRegistryNil = errors.New("account type registry cannot be nil")

// NewAccountTypeService creates a new account type service.
func NewAccountTypeService(
	reg accounttype.Registry,
	instrumentReg registry.InstrumentRegistry,
	compiler *refcel.Compiler,
	logger *slog.Logger,
) (*AccountTypeService, error) {
	if reg == nil {
		return nil, ErrAccountTypeRegistryNil
	}
	if compiler == nil {
		return nil, ErrCompilerNil
	}
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	return &AccountTypeService{
		registry:           reg,
		instrumentRegistry: instrumentReg,
		compiler:           compiler,
		logger:             logger,
	}, nil
}

// accountTypeErrorMapping maps domain errors to gRPC status codes.
var accountTypeErrorMapping = []struct {
	sentinel error
	code     codes.Code
	message  string
	logLevel slog.Level
}{
	{accounttype.ErrNotFound, codes.NotFound, "account type not found", slog.LevelWarn},
	{accounttype.ErrOptimisticLock, codes.Aborted, "account type was modified by another transaction", slog.LevelWarn},
	{accounttype.ErrNotDraft, codes.FailedPrecondition, "account type must be in DRAFT status", slog.LevelWarn},
	{accounttype.ErrNotActive, codes.FailedPrecondition, "account type must be in ACTIVE status", slog.LevelWarn},
	{accounttype.ErrFieldImmutable, codes.InvalidArgument, "immutable field", slog.LevelWarn},
	{accounttype.ErrInvalidCEL, codes.InvalidArgument, "invalid CEL expression", slog.LevelWarn},
	{accounttype.ErrActiveCodeExists, codes.AlreadyExists, "active account type already exists", slog.LevelWarn},
	{accounttype.ErrInvalidBehaviorClass, codes.InvalidArgument, "invalid behavior class", slog.LevelWarn},
	{accounttype.ErrInvalidNormalBalance, codes.InvalidArgument, "invalid normal balance", slog.LevelWarn},
	{accounttype.ErrConversionMethodPair, codes.InvalidArgument, "default_conversion_method_id and default_conversion_method_version must both be set or both be empty", slog.LevelWarn},
	{accounttype.ErrSuccessorWriteOnce, codes.FailedPrecondition, "successor_id is write-once and already set", slog.LevelWarn},
	{accounttype.ErrInvalidInstrument, codes.FailedPrecondition, "instrument validation failed", slog.LevelWarn},
	{accounttype.ErrInvalidConversionMethod, codes.FailedPrecondition, "conversion method validation failed", slog.LevelWarn},
	{accounttype.ErrInvalidValuationMethod, codes.FailedPrecondition, "valuation method validation failed", slog.LevelWarn},
	{accounttype.ErrInvalidAttributeSchema, codes.InvalidArgument, "invalid attribute schema", slog.LevelWarn},
	{accounttype.ErrAttributesInvalid, codes.InvalidArgument, "attributes validation failed", slog.LevelWarn},
}

// mapDomainError converts accounttype domain errors to gRPC status codes.
func (s *AccountTypeService) mapDomainError(ctx context.Context, err error, operation, identifier string) error {
	for _, m := range accountTypeErrorMapping {
		if errors.Is(err, m.sentinel) {
			s.logger.Log(ctx, m.logLevel, m.message,
				"operation", operation,
				"identifier", identifier,
				"error", err)
			return status.Errorf(m.code, "%s: %v", m.message, err)
		}
	}
	s.logger.ErrorContext(ctx, "internal error",
		"operation", operation,
		"identifier", identifier,
		"error", err)
	return status.Errorf(codes.Internal, "internal error: %v", err)
}

// --- Proto <-> Domain mapping ---

func accountTypeToProto(def *accounttype.Definition) *pb.AccountTypeDefinition {
	if def == nil {
		return nil
	}

	proto := &pb.AccountTypeDefinition{
		Id:                def.ID.String(),
		Code:              def.Code,
		Version:           int32(def.Version),
		DisplayName:       def.DisplayName,
		Description:       def.Description,
		NormalBalance:     domainNormalBalanceToProto(def.NormalBalance),
		BehaviorClass:     domainBehaviorClassToProto(def.BehaviorClass),
		InstrumentCode:    def.InstrumentCode,
		DefaultSagaPrefix: def.DefaultSagaPrefix,
		ValidationCel:     def.ValidationCEL,
		BucketingCel:      def.BucketingCEL,
		EligibilityCel:    def.EligibilityCEL,
		AttributeSchema:   string(def.AttributeSchema),
		Attributes:        anyMapToStringMap(def.Attributes),
		Status:            domainAccountTypeStatusToProto(def.Status),
		IsSystem:          def.IsSystem,
		CreatedAt:         timestamppb.New(def.CreatedAt),
		UpdatedAt:         timestamppb.New(def.UpdatedAt),
	}

	if def.DefaultConversionMethodID != nil {
		proto.DefaultConversionMethodId = def.DefaultConversionMethodID.String()
	}
	if def.DefaultConversionMethodVersion != nil {
		proto.DefaultConversionMethodVersion = int32(*def.DefaultConversionMethodVersion)
	}
	if def.SuccessorID != nil {
		proto.SuccessorId = def.SuccessorID.String()
	}
	if def.ActivatedAt != nil {
		proto.ActivatedAt = timestamppb.New(*def.ActivatedAt)
	}
	if def.DeprecatedAt != nil {
		proto.DeprecatedAt = timestamppb.New(*def.DeprecatedAt)
	}

	if len(def.ValuationMethods) > 0 {
		proto.ValuationMethods = make([]*pb.ValuationMethodTemplate, len(def.ValuationMethods))
		for i, vmt := range def.ValuationMethods {
			proto.ValuationMethods[i] = valuationMethodToProto(&vmt)
		}
	}

	return proto
}

func valuationMethodToProto(vmt *accounttype.ValuationMethodTemplate) *pb.ValuationMethodTemplate {
	proto := &pb.ValuationMethodTemplate{
		Id:                     vmt.ID.String(),
		AccountTypeId:          vmt.AccountTypeID.String(),
		InputInstrument:        vmt.InputInstrument,
		ValuationMethodId:      vmt.ValuationMethodID.String(),
		ValuationMethodVersion: int32(vmt.ValuationMethodVersion),
		Parameters:             anyMapToStringMap(vmt.Parameters),
		Status:                 domainAccountTypeStatusToProto(vmt.Status),
	}
	if vmt.SuccessorID != nil {
		proto.SuccessorId = vmt.SuccessorID.String()
	}
	return proto
}

// --- Enum mappings ---

func domainAccountTypeStatusToProto(s accounttype.Status) pb.AccountTypeStatus {
	switch s {
	case accounttype.StatusDraft:
		return pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_DRAFT
	case accounttype.StatusActive:
		return pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_ACTIVE
	case accounttype.StatusDeprecated:
		return pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_DEPRECATED
	default:
		return pb.AccountTypeStatus_ACCOUNT_TYPE_STATUS_UNSPECIFIED
	}
}

func domainNormalBalanceToProto(n accounttype.NormalBalance) pb.NormalBalance {
	switch n {
	case accounttype.NormalBalanceDebit:
		return pb.NormalBalance_NORMAL_BALANCE_DEBIT
	case accounttype.NormalBalanceCredit:
		return pb.NormalBalance_NORMAL_BALANCE_CREDIT
	default:
		return pb.NormalBalance_NORMAL_BALANCE_UNSPECIFIED
	}
}

func domainBehaviorClassToProto(b accounttype.BehaviorClass) pb.BehaviorClass {
	switch b {
	case accounttype.BehaviorClassCustomer:
		return pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER
	case accounttype.BehaviorClassClearing:
		return pb.BehaviorClass_BEHAVIOR_CLASS_CLEARING
	case accounttype.BehaviorClassNostro:
		return pb.BehaviorClass_BEHAVIOR_CLASS_NOSTRO
	case accounttype.BehaviorClassVostro:
		return pb.BehaviorClass_BEHAVIOR_CLASS_VOSTRO
	case accounttype.BehaviorClassHolding:
		return pb.BehaviorClass_BEHAVIOR_CLASS_HOLDING
	case accounttype.BehaviorClassSuspense:
		return pb.BehaviorClass_BEHAVIOR_CLASS_SUSPENSE
	case accounttype.BehaviorClassRevenue:
		return pb.BehaviorClass_BEHAVIOR_CLASS_REVENUE
	case accounttype.BehaviorClassExpense:
		return pb.BehaviorClass_BEHAVIOR_CLASS_EXPENSE
	case accounttype.BehaviorClassInventory:
		return pb.BehaviorClass_BEHAVIOR_CLASS_INVENTORY
	default:
		return pb.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED
	}
}

func protoBehaviorClassToDomain(b pb.BehaviorClass) accounttype.BehaviorClass {
	switch b {
	case pb.BehaviorClass_BEHAVIOR_CLASS_UNSPECIFIED:
		return ""
	case pb.BehaviorClass_BEHAVIOR_CLASS_CUSTOMER:
		return accounttype.BehaviorClassCustomer
	case pb.BehaviorClass_BEHAVIOR_CLASS_CLEARING:
		return accounttype.BehaviorClassClearing
	case pb.BehaviorClass_BEHAVIOR_CLASS_NOSTRO:
		return accounttype.BehaviorClassNostro
	case pb.BehaviorClass_BEHAVIOR_CLASS_VOSTRO:
		return accounttype.BehaviorClassVostro
	case pb.BehaviorClass_BEHAVIOR_CLASS_HOLDING:
		return accounttype.BehaviorClassHolding
	case pb.BehaviorClass_BEHAVIOR_CLASS_SUSPENSE:
		return accounttype.BehaviorClassSuspense
	case pb.BehaviorClass_BEHAVIOR_CLASS_REVENUE:
		return accounttype.BehaviorClassRevenue
	case pb.BehaviorClass_BEHAVIOR_CLASS_EXPENSE:
		return accounttype.BehaviorClassExpense
	case pb.BehaviorClass_BEHAVIOR_CLASS_INVENTORY:
		return accounttype.BehaviorClassInventory
	default:
		return ""
	}
}

func protoBehaviorClassToDomainString(b pb.BehaviorClass) string {
	return string(protoBehaviorClassToDomain(b))
}

func protoBehaviorNormalBalanceToDomainString(n pb.NormalBalance) string {
	switch n {
	case pb.NormalBalance_NORMAL_BALANCE_UNSPECIFIED:
		return ""
	case pb.NormalBalance_NORMAL_BALANCE_DEBIT:
		return "DEBIT"
	case pb.NormalBalance_NORMAL_BALANCE_CREDIT:
		return "CREDIT"
	default:
		return ""
	}
}

// --- Map utilities ---

// parseConversionMethodPair validates and parses the conversion method ID/version pair.
// Returns nil pointers if ID is empty. Returns InvalidArgument if ID is invalid or version < 1.
func parseConversionMethodPair(idStr string, version int32) (*uuid.UUID, *int, error) {
	if idStr == "" {
		return nil, nil, nil
	}
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil, nil, status.Errorf(codes.InvalidArgument, "invalid default_conversion_method_id: %v", err)
	}
	if version < 1 {
		return nil, nil, status.Errorf(codes.InvalidArgument, "default_conversion_method_version must be >= 1 when default_conversion_method_id is set")
	}
	v := int(version)
	return &id, &v, nil
}

// toRawJSON converts a proto string field to json.RawMessage, returning nil for
// empty strings so that PostgreSQL jsonb columns receive NULL rather than
// invalid empty-string JSON.
func toRawJSON(s string) json.RawMessage {
	if s == "" {
		return nil
	}
	return json.RawMessage(s)
}

func stringMapToAnyMap(m map[string]string) map[string]any {
	if m == nil {
		return nil
	}
	result := make(map[string]any, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

func anyMapToStringMap(m map[string]any) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}

	prev := make([]int, lb+1)
	curr := make([]int, lb+1)

	for j := 0; j <= lb; j++ {
		prev[j] = j
	}

	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, min(prev[j]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}

	return prev[lb]
}

// findSuggestions returns up to 3 closest matches by Levenshtein distance (max distance 3).
func findSuggestions(query string, candidates []string) []string {
	type scored struct {
		value    string
		distance int
	}

	var matches []scored
	for _, c := range candidates {
		d := levenshtein(strings.ToUpper(query), strings.ToUpper(c))
		if d <= 3 && d > 0 {
			matches = append(matches, scored{value: c, distance: d})
		}
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].distance < matches[j].distance
	})

	limit := 3
	if len(matches) < limit {
		limit = len(matches)
	}

	result := make([]string, limit)
	for i := 0; i < limit; i++ {
		result[i] = matches[i].value
	}
	return result
}
