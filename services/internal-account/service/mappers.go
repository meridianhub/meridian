// Package service implements gRPC services for the internal account domain.
package service

import (
	"errors"
	"fmt"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrUnspecifiedEnum is returned when an enum value is UNSPECIFIED.
var ErrUnspecifiedEnum = errors.New("unspecified enum value")

// ErrUnknownAccountStatus is returned when an account status value is not recognized.
var ErrUnknownAccountStatus = errors.New("unknown account status")

// ErrUnknownClearingPurpose is returned when a clearing purpose value is not recognized.
var ErrUnknownClearingPurpose = errors.New("unknown clearing purpose")

// toProtoFacility converts a domain InternalAccount to a proto InternalAccountFacility.
func toProtoFacility(account domain.InternalAccount) *pb.InternalAccountFacility {
	facility := &pb.InternalAccountFacility{
		AccountId:          account.AccountID(),
		AccountCode:        account.AccountCode(),
		Name:               account.Name(),
		BehaviorClass:      string(account.AccountType()),
		ClearingPurpose:    clearingPurposeToProto(account.ClearingPurpose()),
		AccountStatus:      accountStatusToProto(account.Status()),
		InstrumentCode:     account.InstrumentCode(),
		CreatedAt:          timestamppb.New(account.CreatedAt()),
		UpdatedAt:          timestamppb.New(account.UpdatedAt()),
		Version:            int32(account.Version()),
		ProductTypeCode:    account.ProductTypeCode(),
		ProductTypeVersion: int32(account.ProductTypeVersion()),
	}

	// Map counterparty details if present
	if counterparty := account.Counterparty(); counterparty != nil {
		facility.CounterpartyDetails = &pb.CounterpartyDetails{
			CounterpartyId:          counterparty.CounterpartyID(),
			CounterpartyName:        counterparty.CounterpartyName(),
			CounterpartyExternalRef: counterparty.ExternalRef(),
			Attributes:              counterparty.Attributes(),
			CounterpartyType:        counterpartyTypeFromAccountType(account.AccountType()),
		}
	}

	return facility
}

// protoToAccountStatus converts a proto InternalAccountStatus to a domain AccountStatus.
func protoToAccountStatus(ps pb.InternalAccountStatus) (domain.AccountStatus, error) {
	switch ps {
	case pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE:
		return domain.AccountStatusActive, nil
	case pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED:
		return domain.AccountStatusSuspended, nil
	case pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_CLOSED:
		return domain.AccountStatusClosed, nil
	case pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_UNSPECIFIED:
		return "", fmt.Errorf("%w: account status", ErrUnspecifiedEnum)
	default:
		return "", fmt.Errorf("%w: %v", ErrUnknownAccountStatus, ps)
	}
}

// accountStatusToProto converts a domain AccountStatus to a proto InternalAccountStatus.
func accountStatusToProto(as domain.AccountStatus) pb.InternalAccountStatus {
	switch as {
	case domain.AccountStatusActive:
		return pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_ACTIVE
	case domain.AccountStatusSuspended:
		return pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_SUSPENDED
	case domain.AccountStatusClosed:
		return pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_CLOSED
	default:
		return pb.InternalAccountStatus_INTERNAL_ACCOUNT_STATUS_UNSPECIFIED
	}
}

// protoToClearingPurpose converts a proto ClearingPurpose to a domain ClearingPurpose.
func protoToClearingPurpose(pc pb.ClearingPurpose) (domain.ClearingPurpose, error) {
	switch pc {
	case pb.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED:
		return domain.ClearingPurposeUnspecified, nil
	case pb.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT:
		return domain.ClearingPurposeDeposit, nil
	case pb.ClearingPurpose_CLEARING_PURPOSE_WITHDRAWAL:
		return domain.ClearingPurposeWithdrawal, nil
	case pb.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT:
		return domain.ClearingPurposeSettlement, nil
	case pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL:
		return domain.ClearingPurposeGeneral, nil
	default:
		return "", fmt.Errorf("%w: %v", ErrUnknownClearingPurpose, pc)
	}
}

// clearingPurposeToProto converts a domain ClearingPurpose to a proto ClearingPurpose.
func clearingPurposeToProto(cp domain.ClearingPurpose) pb.ClearingPurpose {
	switch cp {
	case domain.ClearingPurposeDeposit:
		return pb.ClearingPurpose_CLEARING_PURPOSE_DEPOSIT
	case domain.ClearingPurposeWithdrawal:
		return pb.ClearingPurpose_CLEARING_PURPOSE_WITHDRAWAL
	case domain.ClearingPurposeSettlement:
		return pb.ClearingPurpose_CLEARING_PURPOSE_SETTLEMENT
	case domain.ClearingPurposeGeneral:
		return pb.ClearingPurpose_CLEARING_PURPOSE_GENERAL
	case domain.ClearingPurposeUnspecified:
		return pb.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED
	default:
		return pb.ClearingPurpose_CLEARING_PURPOSE_UNSPECIFIED
	}
}

// counterpartyTypeFromAccountType returns the counterparty type based on account type.
func counterpartyTypeFromAccountType(at domain.AccountType) pb.CounterpartyType {
	switch at {
	case domain.AccountTypeNostro:
		return pb.CounterpartyType_COUNTERPARTY_TYPE_NOSTRO
	case domain.AccountTypeVostro:
		return pb.CounterpartyType_COUNTERPARTY_TYPE_VOSTRO
	case domain.AccountTypeClearing,
		domain.AccountTypeHolding,
		domain.AccountTypeSuspense,
		domain.AccountTypeRevenue,
		domain.AccountTypeExpense:
		return pb.CounterpartyType_COUNTERPARTY_TYPE_UNSPECIFIED
	default:
		return pb.CounterpartyType_COUNTERPARTY_TYPE_UNSPECIFIED
	}
}

// mapDomainErrorToGRPC converts domain errors to appropriate gRPC status errors.
func mapDomainErrorToGRPC(err error) error {
	switch {
	case errors.Is(err, domain.ErrAccountNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, domain.ErrAccountClosed):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrAccountSuspended):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrInvalidAccountType):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrInvalidStatusTransition):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrCounterpartyRequired):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrCounterpartyNotAllowed):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrDuplicateAccountCode):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, domain.ErrVersionMismatch):
		return status.Error(codes.Aborted, err.Error())
	case errors.Is(err, domain.ErrAccountIDRequired),
		errors.Is(err, domain.ErrAccountCodeRequired),
		errors.Is(err, domain.ErrNameRequired),
		errors.Is(err, domain.ErrInvalidClearingPurpose),
		errors.Is(err, domain.ErrClearingPurposeNotAllowed),
		errors.Is(err, domain.ErrClearingPurposeRequired):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Errorf(codes.Internal, "internal error: %v", err)
	}
}
