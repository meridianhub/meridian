// Package service implements gRPC services for the internal bank account domain.
package service

import (
	"errors"
	"fmt"

	pb "github.com/meridianhub/meridian/api/proto/meridian/internal_bank_account/v1"
	"github.com/meridianhub/meridian/services/internal-bank-account/domain"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ErrUnspecifiedEnum is returned when an enum value is UNSPECIFIED.
var ErrUnspecifiedEnum = errors.New("unspecified enum value")

// ErrUnknownAccountType is returned when an account type value is not recognized.
var ErrUnknownAccountType = errors.New("unknown account type")

// ErrUnknownAccountStatus is returned when an account status value is not recognized.
var ErrUnknownAccountStatus = errors.New("unknown account status")

// ErrUnknownClearingPurpose is returned when a clearing purpose value is not recognized.
var ErrUnknownClearingPurpose = errors.New("unknown clearing purpose")

// toProtoFacility converts a domain InternalBankAccount to a proto InternalBankAccountFacility.
func toProtoFacility(account domain.InternalBankAccount) *pb.InternalBankAccountFacility {
	facility := &pb.InternalBankAccountFacility{
		AccountId:       account.AccountID(),
		AccountCode:     account.AccountCode(),
		Name:            account.Name(),
		AccountType:     accountTypeToProto(account.AccountType()),
		ClearingPurpose: clearingPurposeToProto(account.ClearingPurpose()),
		AccountStatus:   accountStatusToProto(account.Status()),
		InstrumentCode:  account.InstrumentCode(),
		CreatedAt:       timestamppb.New(account.CreatedAt()),
		UpdatedAt:       timestamppb.New(account.UpdatedAt()),
		Version:         int32(account.Version()),
	}

	// Map correspondent details if present
	if correspondent := account.Correspondent(); correspondent != nil {
		facility.CorrespondentDetails = &pb.CorrespondentBankDetails{
			BankId:             correspondent.BankID(),
			BankName:           correspondent.BankName(),
			ExternalAccountRef: correspondent.ExternalAccountRef(),
			SwiftCode:          correspondent.SwiftCode(),
			CorrespondentType:  correspondentTypeFromAccountType(account.AccountType()),
		}
	}

	return facility
}

// protoToAccountType converts a proto InternalAccountType to a domain AccountType.
func protoToAccountType(pt pb.InternalAccountType) (domain.AccountType, error) {
	switch pt {
	case pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING:
		return domain.AccountTypeClearing, nil
	case pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO:
		return domain.AccountTypeNostro, nil
	case pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_VOSTRO:
		return domain.AccountTypeVostro, nil
	case pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_HOLDING:
		return domain.AccountTypeHolding, nil
	case pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE:
		return domain.AccountTypeSuspense, nil
	case pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE:
		return domain.AccountTypeRevenue, nil
	case pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_EXPENSE:
		return domain.AccountTypeExpense, nil
	case pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_INVENTORY:
		// Map INVENTORY to HOLDING as closest equivalent
		return domain.AccountTypeHolding, nil
	case pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_UNSPECIFIED:
		return "", fmt.Errorf("%w: account type", ErrUnspecifiedEnum)
	default:
		return "", fmt.Errorf("%w: %v", ErrUnknownAccountType, pt)
	}
}

// accountTypeToProto converts a domain AccountType to a proto InternalAccountType.
func accountTypeToProto(at domain.AccountType) pb.InternalAccountType {
	switch at {
	case domain.AccountTypeClearing:
		return pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_CLEARING
	case domain.AccountTypeNostro:
		return pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_NOSTRO
	case domain.AccountTypeVostro:
		return pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_VOSTRO
	case domain.AccountTypeHolding:
		return pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_HOLDING
	case domain.AccountTypeSuspense:
		return pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_SUSPENSE
	case domain.AccountTypeRevenue:
		return pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_REVENUE
	case domain.AccountTypeExpense:
		return pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_EXPENSE
	default:
		return pb.InternalAccountType_INTERNAL_ACCOUNT_TYPE_UNSPECIFIED
	}
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

// correspondentTypeFromAccountType returns the correspondent type based on account type.
func correspondentTypeFromAccountType(at domain.AccountType) pb.CorrespondentType {
	switch at {
	case domain.AccountTypeNostro:
		return pb.CorrespondentType_CORRESPONDENT_TYPE_NOSTRO
	case domain.AccountTypeVostro:
		return pb.CorrespondentType_CORRESPONDENT_TYPE_VOSTRO
	case domain.AccountTypeClearing,
		domain.AccountTypeHolding,
		domain.AccountTypeSuspense,
		domain.AccountTypeRevenue,
		domain.AccountTypeExpense:
		return pb.CorrespondentType_CORRESPONDENT_TYPE_UNSPECIFIED
	default:
		return pb.CorrespondentType_CORRESPONDENT_TYPE_UNSPECIFIED
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
	case errors.Is(err, domain.ErrCorrespondentRequired):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrCorrespondentNotAllowed):
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
