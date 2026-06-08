package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// errBranchTest is a generic, non-sentinel error used to exercise the
// Internal-error fall-through branches in the payment-method handlers.
var errBranchTest = errors.New("database unavailable")

func TestAddPaymentMethod_BranchCoverage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns INVALID_ARGUMENT for malformed party ID", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            "not-a-uuid",
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "pm_testmethod12345",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
		})

		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("returns INTERNAL when party verification fails with generic error", func(t *testing.T) {
		partyRepo := newMockRepository()
		partyRepo.findByIDErr = errBranchTest
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            uuid.New().String(),
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "pm_testmethod12345",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
		})

		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("returns INVALID_ARGUMENT for unspecified provider", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            party.ID().String(),
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_UNSPECIFIED,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "pm_testmethod12345",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
		})

		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("returns INVALID_ARGUMENT for unspecified method type", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            party.ID().String(),
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "pm_testmethod12345",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_UNSPECIFIED,
		})

		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("returns INTERNAL when create fails with generic error", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		pmRepo.createErr = errBranchTest
		party := createTestParty(t, partyRepo)
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            party.ID().String(),
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "pm_testmethod12345",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
		})

		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("adds SEPA method with card expiry metadata", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		resp, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            party.ID().String(),
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "pm_testmethod12345",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA,
			Metadata: map[string]string{
				"last4":     "4242",
				"brand":     "visa",
				"exp_month": "12",
				"exp_year":  "2030",
			},
		})

		require.NoError(t, err)
		assert.Equal(t, pb.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA, resp.PaymentMethod.MethodType)
		assert.Equal(t, "12", resp.PaymentMethod.Metadata["exp_month"])
		assert.Equal(t, "2030", resp.PaymentMethod.Metadata["exp_year"])
	})
}

func TestRemovePaymentMethod_BranchCoverage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns UNIMPLEMENTED when pmRepo not configured", func(t *testing.T) {
		partyRepo := newMockRepository()
		svc := newTestService(partyRepo)

		_, err := svc.RemovePaymentMethod(ctx, &pb.RemovePaymentMethodRequest{
			Id:      uuid.New().String(),
			Version: 1,
		})

		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})

	t.Run("returns INVALID_ARGUMENT for malformed ID", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.RemovePaymentMethod(ctx, &pb.RemovePaymentMethodRequest{
			Id:      "not-a-uuid",
			Version: 1,
		})

		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("returns INTERNAL when FindByID fails with generic error", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		pmRepo.findByIDErr = errBranchTest
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.RemovePaymentMethod(ctx, &pb.RemovePaymentMethodRequest{
			Id:      uuid.New().String(),
			Version: 1,
		})

		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("returns FAILED_PRECONDITION when removing already-removed method", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), false)
		require.NoError(t, pm.Remove())
		pmRepo.methods[pm.ID()] = pm
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.RemovePaymentMethod(ctx, &pb.RemovePaymentMethodRequest{
			Id:      pm.ID().String(),
			Version: pm.Version(),
		})

		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	})

	t.Run("returns INTERNAL when update fails with generic error", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), false)
		pmRepo.methods[pm.ID()] = pm
		pmRepo.updateErr = errBranchTest
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.RemovePaymentMethod(ctx, &pb.RemovePaymentMethodRequest{
			Id:      pm.ID().String(),
			Version: pm.Version(),
		})

		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestSetDefaultPaymentMethod_BranchCoverage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns UNIMPLEMENTED when pmRepo not configured", func(t *testing.T) {
		partyRepo := newMockRepository()
		svc := newTestService(partyRepo)

		_, err := svc.SetDefaultPaymentMethod(ctx, &pb.SetDefaultPaymentMethodRequest{
			Id: uuid.New().String(),
		})

		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})

	t.Run("returns INVALID_ARGUMENT for malformed ID", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.SetDefaultPaymentMethod(ctx, &pb.SetDefaultPaymentMethodRequest{
			Id: "not-a-uuid",
		})

		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("returns INTERNAL when FindByID fails with generic error", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		pmRepo.findByIDErr = errBranchTest
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.SetDefaultPaymentMethod(ctx, &pb.SetDefaultPaymentMethodRequest{
			Id: uuid.New().String(),
		})

		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("returns INTERNAL when update fails with generic error", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), false)
		pmRepo.methods[pm.ID()] = pm
		pmRepo.updateErr = errBranchTest
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.SetDefaultPaymentMethod(ctx, &pb.SetDefaultPaymentMethodRequest{
			Id: pm.ID().String(),
		})

		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("returns FAILED_PRECONDITION for expired payment method", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), false)
		require.NoError(t, pm.Expire())
		pmRepo.methods[pm.ID()] = pm
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.SetDefaultPaymentMethod(ctx, &pb.SetDefaultPaymentMethodRequest{
			Id: pm.ID().String(),
		})

		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	})

	t.Run("returns ABORTED on version conflict during update", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), false)
		pmRepo.methods[pm.ID()] = pm
		pmRepo.updateErr = persistence.ErrVersionConflict
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.SetDefaultPaymentMethod(ctx, &pb.SetDefaultPaymentMethodRequest{
			Id: pm.ID().String(),
		})

		assert.Equal(t, codes.Aborted, status.Code(err))
	})
}

func TestListPaymentMethods_BranchCoverage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns UNIMPLEMENTED when pmRepo not configured", func(t *testing.T) {
		partyRepo := newMockRepository()
		svc := newTestService(partyRepo)

		_, err := svc.ListPaymentMethods(ctx, &pb.ListPaymentMethodsRequest{
			PartyId: uuid.New().String(),
		})

		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})

	t.Run("returns INVALID_ARGUMENT for malformed party ID", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.ListPaymentMethods(ctx, &pb.ListPaymentMethodsRequest{
			PartyId: "not-a-uuid",
		})

		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestGetDefaultPaymentMethod_BranchCoverage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns UNIMPLEMENTED when pmRepo not configured", func(t *testing.T) {
		partyRepo := newMockRepository()
		svc := newTestService(partyRepo)

		_, err := svc.GetDefaultPaymentMethod(ctx, &pb.GetDefaultPaymentMethodRequest{
			PartyId: uuid.New().String(),
		})

		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})

	t.Run("returns INVALID_ARGUMENT for malformed party ID", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.GetDefaultPaymentMethod(ctx, &pb.GetDefaultPaymentMethodRequest{
			PartyId: "not-a-uuid",
		})

		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

// TestPaymentMethodConverters exercises the enum/metadata conversion helpers
// across every case so the round-trip mappings are pinned.
func TestPaymentMethodConverters(t *testing.T) {
	t.Parallel()

	t.Run("provider proto-to-domain rejects unknown values", func(t *testing.T) {
		// Unspecified and an out-of-range value both hit the error branches.
		_, err := protoToPaymentProvider(pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_UNSPECIFIED)
		require.Error(t, err)
		_, err = protoToPaymentProvider(pb.PaymentMethodProvider(999))
		require.Error(t, err)
	})

	t.Run("provider domain-to-proto maps unknown to unspecified", func(t *testing.T) {
		assert.Equal(t,
			pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_UNSPECIFIED,
			paymentProviderToProto(domain.PaymentProvider("PAYPAL")))
	})

	t.Run("method type proto-to-domain covers all variants", func(t *testing.T) {
		cardType, err := protoToPaymentMethodType(pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentMethodTypeCard, cardType)

		bankType, err := protoToPaymentMethodType(pb.PaymentMethodType_PAYMENT_METHOD_TYPE_BANK_ACCOUNT)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentMethodTypeBankAccount, bankType)

		sepaType, err := protoToPaymentMethodType(pb.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentMethodTypeSEPA, sepaType)

		_, err = protoToPaymentMethodType(pb.PaymentMethodType_PAYMENT_METHOD_TYPE_UNSPECIFIED)
		require.Error(t, err)
		_, err = protoToPaymentMethodType(pb.PaymentMethodType(999))
		require.Error(t, err)
	})

	t.Run("method type domain-to-proto covers all variants", func(t *testing.T) {
		assert.Equal(t, pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
			paymentMethodTypeToProto(domain.PaymentMethodTypeCard))
		assert.Equal(t, pb.PaymentMethodType_PAYMENT_METHOD_TYPE_BANK_ACCOUNT,
			paymentMethodTypeToProto(domain.PaymentMethodTypeBankAccount))
		assert.Equal(t, pb.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA,
			paymentMethodTypeToProto(domain.PaymentMethodTypeSEPA))
		assert.Equal(t, pb.PaymentMethodType_PAYMENT_METHOD_TYPE_UNSPECIFIED,
			paymentMethodTypeToProto(domain.PaymentMethodType("CRYPTO")))
	})

	t.Run("status domain-to-proto covers all variants", func(t *testing.T) {
		assert.Equal(t, pb.PaymentMethodStatus_PAYMENT_METHOD_STATUS_ACTIVE,
			paymentMethodStatusToProto(domain.PaymentMethodStatusActive))
		assert.Equal(t, pb.PaymentMethodStatus_PAYMENT_METHOD_STATUS_EXPIRED,
			paymentMethodStatusToProto(domain.PaymentMethodStatusExpired))
		assert.Equal(t, pb.PaymentMethodStatus_PAYMENT_METHOD_STATUS_REMOVED,
			paymentMethodStatusToProto(domain.PaymentMethodStatusRemoved))
		assert.Equal(t, pb.PaymentMethodStatus_PAYMENT_METHOD_STATUS_UNSPECIFIED,
			paymentMethodStatusToProto(domain.PaymentMethodStatus("PENDING")))
	})

	t.Run("metadata proto-to-domain parses card expiry", func(t *testing.T) {
		meta := protoMetadataToDomain(map[string]string{
			"last4":     "1234",
			"brand":     "mastercard",
			"exp_month": "8",
			"exp_year":  "2029",
		})
		assert.Equal(t, "1234", meta.Last4)
		assert.Equal(t, "mastercard", meta.Brand)
		assert.Equal(t, 8, meta.ExpMonth)
		assert.Equal(t, 2029, meta.ExpYear)
	})

	t.Run("metadata proto-to-domain ignores non-numeric expiry", func(t *testing.T) {
		meta := protoMetadataToDomain(map[string]string{
			"exp_month": "notanumber",
			"exp_year":  "alsobad",
		})
		assert.Equal(t, 0, meta.ExpMonth)
		assert.Equal(t, 0, meta.ExpYear)
	})

	t.Run("metadata domain-to-proto round-trips populated fields", func(t *testing.T) {
		result := domainMetadataToProto(&domain.PaymentMethodMetadata{
			Last4:    "9999",
			Brand:    "amex",
			ExpMonth: 1,
			ExpYear:  2031,
		})
		assert.Equal(t, "9999", result["last4"])
		assert.Equal(t, "amex", result["brand"])
		assert.Equal(t, "1", result["exp_month"])
		assert.Equal(t, "2031", result["exp_year"])
	})

	t.Run("metadata domain-to-proto omits empty fields", func(t *testing.T) {
		result := domainMetadataToProto(&domain.PaymentMethodMetadata{})
		assert.Empty(t, result)
	})
}
