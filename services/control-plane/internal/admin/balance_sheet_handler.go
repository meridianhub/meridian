package admin

import (
	"context"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
)

// BalanceSheetHandler implements the BalanceSheetService gRPC service.
type BalanceSheetHandler struct {
	controlplanev1.UnimplementedBalanceSheetServiceServer
	service *BalanceSheetService
	logger  *slog.Logger
}

// NewBalanceSheetHandler creates a new BalanceSheetService handler.
func NewBalanceSheetHandler(service *BalanceSheetService, logger *slog.Logger) *BalanceSheetHandler {
	if logger == nil {
		logger = slog.Default()
	}
	return &BalanceSheetHandler{
		service: service,
		logger:  logger,
	}
}

// GetBalanceSheet returns a multi-asset balance sheet for a tenant.
func (h *BalanceSheetHandler) GetBalanceSheet(
	ctx context.Context,
	req *controlplanev1.GetBalanceSheetRequest,
) (*controlplanev1.GetBalanceSheetResponse, error) {
	tenantID := req.GetTenantId()
	if tenantID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "tenant_id is required")
	}

	asOf := req.GetAsOf().AsTime()

	bs, err := h.service.GetBalanceSheet(ctx, tenantID, asOf)
	if err != nil {
		h.logger.Error("failed to generate balance sheet",
			"tenant_id", tenantID,
			"error", err,
		)
		return nil, status.Errorf(codes.Internal, "failed to generate balance sheet: %v", err)
	}

	return h.balanceSheetToProto(bs), nil
}

// GetPositionDetails returns drill-down details for a specific account type and instrument.
func (h *BalanceSheetHandler) GetPositionDetails(
	ctx context.Context,
	req *controlplanev1.GetPositionDetailsRequest,
) (*controlplanev1.GetPositionDetailsResponse, error) {
	tenantID := req.GetTenantId()
	if tenantID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "tenant_id is required")
	}
	if req.GetAccountType() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "account_type is required")
	}
	if req.GetInstrument() == "" {
		return nil, status.Errorf(codes.InvalidArgument, "instrument is required")
	}

	result, err := h.service.GetPositionDetails(ctx, tenantID, req.GetAccountType(), req.GetInstrument())
	if err != nil {
		h.logger.Error("failed to get position details",
			"tenant_id", tenantID,
			"account_type", req.GetAccountType(),
			"instrument", req.GetInstrument(),
			"error", err,
		)
		return nil, status.Errorf(codes.Internal, "failed to get position details: %v", err)
	}

	return h.positionDetailsToProto(result), nil
}

// ExportBalanceSheetCSV exports the balance sheet as CSV.
func (h *BalanceSheetHandler) ExportBalanceSheetCSV(
	ctx context.Context,
	req *controlplanev1.ExportBalanceSheetCSVRequest,
) (*controlplanev1.ExportBalanceSheetCSVResponse, error) {
	tenantID := req.GetTenantId()
	if tenantID == "" {
		return nil, status.Errorf(codes.InvalidArgument, "tenant_id is required")
	}

	asOf := req.GetAsOf().AsTime()

	csvContent, err := h.service.ExportBalanceSheetCSV(ctx, tenantID, asOf)
	if err != nil {
		h.logger.Error("failed to export balance sheet CSV",
			"tenant_id", tenantID,
			"error", err,
		)
		return nil, status.Errorf(codes.Internal, "failed to export balance sheet CSV: %v", err)
	}

	return &controlplanev1.ExportBalanceSheetCSVResponse{
		CsvContent:  csvContent,
		GeneratedAt: timestamppb.Now(),
		TenantId:    tenantID,
	}, nil
}

// balanceSheetToProto converts a domain BalanceSheet to proto.
func (h *BalanceSheetHandler) balanceSheetToProto(bs *BalanceSheet) *controlplanev1.GetBalanceSheetResponse {
	resp := &controlplanev1.GetBalanceSheetResponse{
		TenantId: bs.TenantID,
		AsOf:     timestamppb.New(bs.AsOf),
		Sections: make([]*controlplanev1.BalanceSheetSection, 0, len(bs.Sections)),
	}

	for _, section := range bs.Sections {
		protoSection := &controlplanev1.BalanceSheetSection{
			Classification: classificationToProto(section.Classification),
			LineItems:      make([]*controlplanev1.BalanceSheetLineItem, 0, len(section.LineItems)),
			Totals:         make(map[string]string, len(section.Totals)),
		}

		for _, item := range section.LineItems {
			protoSection.LineItems = append(protoSection.LineItems, &controlplanev1.BalanceSheetLineItem{
				AccountType:   item.AccountType,
				Instrument:    item.Instrument,
				Quantity:      item.Quantity.String(),
				NormalBalance: normalBalanceToProto(item.NormalBalance),
				AccountCount:  item.AccountCount,
			})
		}

		for instrument, total := range section.Totals {
			protoSection.Totals[instrument] = total.String()
		}

		resp.Sections = append(resp.Sections, protoSection)
	}

	return resp
}

// positionDetailsToProto converts a PositionDetailsResult to proto.
func (h *BalanceSheetHandler) positionDetailsToProto(result *PositionDetailsResult) *controlplanev1.GetPositionDetailsResponse {
	resp := &controlplanev1.GetPositionDetailsResponse{
		TenantId:    result.TenantID,
		AccountType: result.AccountType,
		Instrument:  result.Instrument,
		Total:       result.Total.String(),
		Positions:   make([]*controlplanev1.PositionDetail, 0, len(result.Positions)),
	}

	for _, pos := range result.Positions {
		resp.Positions = append(resp.Positions, &controlplanev1.PositionDetail{
			AccountId:   pos.AccountID,
			Quantity:    pos.Quantity.String(),
			LogId:       pos.LogID,
			LastUpdated: timestamppb.New(pos.LastUpdated),
		})
	}

	return resp
}

func classificationToProto(c BalanceSheetClassification) controlplanev1.BalanceSheetClassification {
	switch c {
	case ClassificationAssets:
		return controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_ASSETS
	case ClassificationLiabilities:
		return controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_LIABILITIES
	case ClassificationEquity:
		return controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_EQUITY
	default:
		return controlplanev1.BalanceSheetClassification_BALANCE_SHEET_CLASSIFICATION_UNSPECIFIED
	}
}

func normalBalanceToProto(nb NormalBalance) controlplanev1.NormalBalanceDirection {
	switch nb {
	case NormalBalanceDebit:
		return controlplanev1.NormalBalanceDirection_NORMAL_BALANCE_DIRECTION_DEBIT
	case NormalBalanceCredit:
		return controlplanev1.NormalBalanceDirection_NORMAL_BALANCE_DIRECTION_CREDIT
	default:
		return controlplanev1.NormalBalanceDirection_NORMAL_BALANCE_DIRECTION_UNSPECIFIED
	}
}
