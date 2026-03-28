package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	errUnsupportedProvider   = errors.New("unsupported payment provider")
	errUnsupportedMethodType = errors.New("unsupported payment method type")
)

// PaymentMethodRepository defines the interface for payment method persistence.
type PaymentMethodRepository interface {
	Create(ctx context.Context, pm *domain.PaymentMethod) error
	Update(ctx context.Context, pm *domain.PaymentMethod) error
	FindByID(ctx context.Context, id uuid.UUID) (*domain.PaymentMethod, error)
	ListActiveByParty(ctx context.Context, partyID uuid.UUID) ([]*domain.PaymentMethod, error)
	FindDefaultByParty(ctx context.Context, partyID uuid.UUID) (*domain.PaymentMethod, error)
}

// AddPaymentMethod registers a tokenized payment method for a party.
func (s *Service) AddPaymentMethod(ctx context.Context, req *pb.AddPaymentMethodRequest) (*pb.AddPaymentMethodResponse, error) {
	if s.pmRepo == nil {
		return nil, status.Error(codes.Unimplemented, "payment method operations not configured")
	}

	partyID, provider, methodType, err := s.validatePaymentMethodInput(ctx, req)
	if err != nil {
		return nil, err
	}

	var metadata *domain.PaymentMethodMetadata
	if len(req.Metadata) > 0 {
		metadata = protoMetadataToDomain(req.Metadata)
	}

	pm, err := domain.NewPaymentMethod(
		partyID, provider, req.ProviderCustomerId, req.ProviderMethodId,
		methodType, req.IsDefault, metadata,
	)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment method: %v", err)
	}

	if err := s.pmRepo.Create(ctx, pm); err != nil {
		if errors.Is(err, persistence.ErrPaymentMethodExists) {
			return nil, status.Errorf(codes.AlreadyExists, "payment method already exists for provider method ID")
		}
		s.logger.Error("failed to create payment method",
			"party_id", req.PartyId, "error", err)
		return nil, status.Errorf(codes.Internal, "failed to create payment method: %v", err)
	}

	s.logger.Info("payment method added",
		"payment_method_id", pm.ID(),
		"party_id", req.PartyId,
		"provider", provider,
		"method_type", methodType,
		"is_default", req.IsDefault)

	return &pb.AddPaymentMethodResponse{
		PaymentMethod: paymentMethodToProto(pm),
	}, nil
}

// validatePaymentMethodInput parses and validates the party ID, provider, and method type from the request.
func (s *Service) validatePaymentMethodInput(ctx context.Context, req *pb.AddPaymentMethodRequest) (uuid.UUID, domain.PaymentProvider, domain.PaymentMethodType, error) {
	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return uuid.Nil, "", "", status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	if _, err := s.repo.FindByID(ctx, partyID); err != nil {
		if errors.Is(err, persistence.ErrPartyNotFound) {
			return uuid.Nil, "", "", status.Errorf(codes.NotFound, "party not found: %s", req.PartyId)
		}
		return uuid.Nil, "", "", status.Errorf(codes.Internal, "failed to verify party: %v", err)
	}

	provider, err := protoToPaymentProvider(req.Provider)
	if err != nil {
		return uuid.Nil, "", "", status.Errorf(codes.InvalidArgument, "invalid provider: %v", err)
	}

	methodType, err := protoToPaymentMethodType(req.MethodType)
	if err != nil {
		return uuid.Nil, "", "", status.Errorf(codes.InvalidArgument, "invalid method type: %v", err)
	}

	return partyID, provider, methodType, nil
}

// RemovePaymentMethod soft-deletes a payment method.
func (s *Service) RemovePaymentMethod(ctx context.Context, req *pb.RemovePaymentMethodRequest) (*pb.RemovePaymentMethodResponse, error) {
	if s.pmRepo == nil {
		return nil, status.Error(codes.Unimplemented, "payment method operations not configured")
	}

	pmID, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment method ID format: %v", err)
	}

	pm, err := s.pmRepo.FindByID(ctx, pmID)
	if err != nil {
		if errors.Is(err, persistence.ErrPaymentMethodNotFound) {
			return nil, status.Errorf(codes.NotFound, "payment method not found: %s", req.Id)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve payment method: %v", err)
	}

	// Optimistic locking check
	if pm.Version() != req.Version {
		return nil, status.Errorf(codes.Aborted, "version conflict: expected %d, got %d", pm.Version(), req.Version)
	}

	if err := pm.Remove(); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot remove payment method: %v", err)
	}

	if err := s.pmRepo.Update(ctx, pm); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			return nil, status.Error(codes.Aborted, "version conflict: payment method was modified by another transaction")
		}
		s.logger.Error("failed to remove payment method",
			"payment_method_id", req.Id,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to remove payment method: %v", err)
	}

	s.logger.Info("payment method removed", "payment_method_id", req.Id)

	return &pb.RemovePaymentMethodResponse{}, nil
}

// SetDefaultPaymentMethod sets a payment method as the party's default.
func (s *Service) SetDefaultPaymentMethod(ctx context.Context, req *pb.SetDefaultPaymentMethodRequest) (*pb.SetDefaultPaymentMethodResponse, error) {
	if s.pmRepo == nil {
		return nil, status.Error(codes.Unimplemented, "payment method operations not configured")
	}

	pmID, err := uuid.Parse(req.Id)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid payment method ID format: %v", err)
	}

	pm, err := s.pmRepo.FindByID(ctx, pmID)
	if err != nil {
		if errors.Is(err, persistence.ErrPaymentMethodNotFound) {
			return nil, status.Errorf(codes.NotFound, "payment method not found: %s", req.Id)
		}
		return nil, status.Errorf(codes.Internal, "failed to retrieve payment method: %v", err)
	}

	if err := pm.SetDefault(true); err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "cannot set payment method as default: %v", err)
	}

	if err := s.pmRepo.Update(ctx, pm); err != nil {
		if errors.Is(err, persistence.ErrVersionConflict) {
			return nil, status.Error(codes.Aborted, "version conflict: payment method was modified by another transaction")
		}
		s.logger.Error("failed to set default payment method",
			"payment_method_id", req.Id,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to set default payment method: %v", err)
	}

	s.logger.Info("default payment method set",
		"payment_method_id", req.Id,
		"party_id", pm.PartyID())

	return &pb.SetDefaultPaymentMethodResponse{
		PaymentMethod: paymentMethodToProto(pm),
	}, nil
}

// ListPaymentMethods returns all active payment methods for a party.
func (s *Service) ListPaymentMethods(ctx context.Context, req *pb.ListPaymentMethodsRequest) (*pb.ListPaymentMethodsResponse, error) {
	if s.pmRepo == nil {
		return nil, status.Error(codes.Unimplemented, "payment method operations not configured")
	}

	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	methods, err := s.pmRepo.ListActiveByParty(ctx, partyID)
	if err != nil {
		s.logger.Error("failed to list payment methods",
			"party_id", req.PartyId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to list payment methods: %v", err)
	}

	pbMethods := make([]*pb.PartyPaymentMethod, len(methods))
	for i, pm := range methods {
		pbMethods[i] = paymentMethodToProto(pm)
	}

	return &pb.ListPaymentMethodsResponse{
		PaymentMethods: pbMethods,
	}, nil
}

// GetDefaultPaymentMethod returns the default payment method for a party.
func (s *Service) GetDefaultPaymentMethod(ctx context.Context, req *pb.GetDefaultPaymentMethodRequest) (*pb.GetDefaultPaymentMethodResponse, error) {
	if s.pmRepo == nil {
		return nil, status.Error(codes.Unimplemented, "payment method operations not configured")
	}

	partyID, err := uuid.Parse(req.PartyId)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid party ID format: %v", err)
	}

	pm, err := s.pmRepo.FindDefaultByParty(ctx, partyID)
	if err != nil {
		s.logger.Error("failed to get default payment method",
			"party_id", req.PartyId,
			"error", err)
		return nil, status.Errorf(codes.Internal, "failed to get default payment method: %v", err)
	}

	if pm == nil {
		return nil, status.Errorf(codes.NotFound, "no default payment method for party: %s", req.PartyId)
	}

	return &pb.GetDefaultPaymentMethodResponse{
		PaymentMethod: paymentMethodToProto(pm),
	}, nil
}

// --- Conversion helpers ---

// paymentMethodToProto converts a domain PaymentMethod to a proto PartyPaymentMethod.
func paymentMethodToProto(pm *domain.PaymentMethod) *pb.PartyPaymentMethod {
	result := &pb.PartyPaymentMethod{
		Id:                 pm.ID().String(),
		PartyId:            pm.PartyID().String(),
		Provider:           paymentProviderToProto(pm.Provider()),
		ProviderCustomerId: pm.ProviderCustomerID(),
		ProviderMethodId:   pm.ProviderMethodID(),
		MethodType:         paymentMethodTypeToProto(pm.MethodType()),
		IsDefault:          pm.IsDefault(),
		Status:             paymentMethodStatusToProto(pm.Status()),
		Version:            pm.Version(),
		CreatedAt:          timestamppb.New(pm.CreatedAt()),
		UpdatedAt:          timestamppb.New(pm.UpdatedAt()),
	}

	if pm.Metadata() != nil {
		result.Metadata = domainMetadataToProto(pm.Metadata())
	}

	return result
}

func protoToPaymentProvider(p pb.PaymentMethodProvider) (domain.PaymentProvider, error) {
	switch p {
	case pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE:
		return domain.PaymentProviderStripe, nil
	case pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_UNSPECIFIED:
		return "", fmt.Errorf("%w: %s", errUnsupportedProvider, p.String())
	default:
		return "", fmt.Errorf("%w: %s", errUnsupportedProvider, p.String())
	}
}

func paymentProviderToProto(p domain.PaymentProvider) pb.PaymentMethodProvider {
	switch p {
	case domain.PaymentProviderStripe:
		return pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE
	default:
		return pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_UNSPECIFIED
	}
}

func protoToPaymentMethodType(mt pb.PaymentMethodType) (domain.PaymentMethodType, error) {
	switch mt {
	case pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD:
		return domain.PaymentMethodTypeCard, nil
	case pb.PaymentMethodType_PAYMENT_METHOD_TYPE_BANK_ACCOUNT:
		return domain.PaymentMethodTypeBankAccount, nil
	case pb.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA:
		return domain.PaymentMethodTypeSEPA, nil
	case pb.PaymentMethodType_PAYMENT_METHOD_TYPE_UNSPECIFIED:
		return "", fmt.Errorf("%w: %s", errUnsupportedMethodType, mt.String())
	default:
		return "", fmt.Errorf("%w: %s", errUnsupportedMethodType, mt.String())
	}
}

func paymentMethodTypeToProto(mt domain.PaymentMethodType) pb.PaymentMethodType {
	switch mt {
	case domain.PaymentMethodTypeCard:
		return pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD
	case domain.PaymentMethodTypeBankAccount:
		return pb.PaymentMethodType_PAYMENT_METHOD_TYPE_BANK_ACCOUNT
	case domain.PaymentMethodTypeSEPA:
		return pb.PaymentMethodType_PAYMENT_METHOD_TYPE_SEPA
	default:
		return pb.PaymentMethodType_PAYMENT_METHOD_TYPE_UNSPECIFIED
	}
}

func paymentMethodStatusToProto(s domain.PaymentMethodStatus) pb.PaymentMethodStatus {
	switch s {
	case domain.PaymentMethodStatusActive:
		return pb.PaymentMethodStatus_PAYMENT_METHOD_STATUS_ACTIVE
	case domain.PaymentMethodStatusExpired:
		return pb.PaymentMethodStatus_PAYMENT_METHOD_STATUS_EXPIRED
	case domain.PaymentMethodStatusRemoved:
		return pb.PaymentMethodStatus_PAYMENT_METHOD_STATUS_REMOVED
	default:
		return pb.PaymentMethodStatus_PAYMENT_METHOD_STATUS_UNSPECIFIED
	}
}

func protoMetadataToDomain(m map[string]string) *domain.PaymentMethodMetadata {
	meta := &domain.PaymentMethodMetadata{
		Last4: m["last4"],
		Brand: m["brand"],
	}
	if v, ok := m["exp_month"]; ok {
		if month, err := strconv.Atoi(v); err == nil {
			meta.ExpMonth = month
		}
	}
	if v, ok := m["exp_year"]; ok {
		if year, err := strconv.Atoi(v); err == nil {
			meta.ExpYear = year
		}
	}
	return meta
}

func domainMetadataToProto(m *domain.PaymentMethodMetadata) map[string]string {
	result := make(map[string]string)
	if m.Last4 != "" {
		result["last4"] = m.Last4
	}
	if m.Brand != "" {
		result["brand"] = m.Brand
	}
	if m.ExpMonth > 0 {
		result["exp_month"] = strconv.Itoa(m.ExpMonth)
	}
	if m.ExpYear > 0 {
		result["exp_year"] = strconv.Itoa(m.ExpYear)
	}
	return result
}
