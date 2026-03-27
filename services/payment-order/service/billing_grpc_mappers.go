package service

import (
	billingpb "github.com/meridianhub/meridian/api/proto/meridian/billing/v1"
	"github.com/meridianhub/meridian/services/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/services/payment-order/domain"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func billingRunToProto(run *domain.BillingRun) *billingpb.BillingRun {
	pb := &billingpb.BillingRun{
		Id:            run.ID.String(),
		TenantId:      run.TenantID,
		PeriodStart:   timestamppb.New(run.CycleStart),
		PeriodEnd:     timestamppb.New(run.CycleEnd),
		Status:        mapBillingRunStatusToProto(run.Status),
		DunningLevel:  int32(run.DunningLevel),
		FailureReason: run.FailureReason,
		CreatedAt:     timestamppb.New(run.CreatedAt),
		UpdatedAt:     timestamppb.New(run.UpdatedAt),
	}
	return pb
}

func invoiceToProto(inv *domain.Invoice) *billingpb.Invoice {
	pb := &billingpb.Invoice{
		Id:            inv.ID.String(),
		BillingRunId:  inv.BillingRunID.String(),
		PartyId:       inv.PartyID,
		AccountId:     inv.AccountID,
		InvoiceNumber: inv.InvoiceNumber,
		PeriodStart:   timestamppb.New(inv.PeriodStart),
		PeriodEnd:     timestamppb.New(inv.PeriodEnd),
		SubtotalCents: inv.SubtotalCents,
		Currency:      inv.Currency,
		Status:        mapInvoiceStatusToProto(inv.Status),
		CreatedAt:     timestamppb.New(inv.CreatedAt),
	}

	for _, li := range inv.LineItems {
		pbItem := &billingpb.InvoiceLineItem{
			Description:    li.Description,
			Quantity:       li.Quantity.String(),
			UnitPriceCents: li.UnitPriceCents,
			TotalCents:     li.TotalCents,
		}
		if len(li.ValuationAnalysis) > 0 {
			pbItem.ValuationAnalysis, _ = structpb.NewStruct(li.ValuationAnalysis)
		}
		pb.LineItems = append(pb.LineItems, pbItem)
	}

	return pb
}

func emailAuditToProto(entry *persistence.EmailAuditEntry) *billingpb.InvoiceEmail {
	pb := &billingpb.InvoiceEmail{
		IdempotencyKey: entry.IdempotencyKey,
		TemplateName:   entry.TemplateName,
		ToAddresses:    entry.ToAddresses,
		Status:         mapEmailStatusToProto(entry.Status),
	}
	if entry.SentAt != nil {
		pb.SentAt = timestamppb.New(*entry.SentAt)
	}
	if entry.DeliveredAt != nil {
		pb.DeliveredAt = timestamppb.New(*entry.DeliveredAt)
	}
	if entry.BounceReason != nil {
		pb.BounceReason = *entry.BounceReason
	}
	return pb
}

func mapBillingRunStatusToProto(s domain.BillingRunStatus) billingpb.BillingRunStatus {
	switch s {
	case domain.BillingRunStatusInitiated:
		return billingpb.BillingRunStatus_BILLING_RUN_STATUS_INITIATED
	case domain.BillingRunStatusProcessing:
		return billingpb.BillingRunStatus_BILLING_RUN_STATUS_PROCESSING
	case domain.BillingRunStatusCompleted:
		return billingpb.BillingRunStatus_BILLING_RUN_STATUS_COMPLETED
	case domain.BillingRunStatusFailed:
		return billingpb.BillingRunStatus_BILLING_RUN_STATUS_FAILED
	default:
		return billingpb.BillingRunStatus_BILLING_RUN_STATUS_UNSPECIFIED
	}
}

func mapInvoiceStatusToProto(s domain.InvoiceStatus) billingpb.InvoiceStatus {
	switch s {
	case domain.InvoiceStatusDraft:
		return billingpb.InvoiceStatus_INVOICE_STATUS_DRAFT
	case domain.InvoiceStatusIssued:
		return billingpb.InvoiceStatus_INVOICE_STATUS_ISSUED
	case domain.InvoiceStatusPaid:
		return billingpb.InvoiceStatus_INVOICE_STATUS_PAID
	case domain.InvoiceStatusVoid:
		return billingpb.InvoiceStatus_INVOICE_STATUS_VOID
	case domain.InvoiceStatusOverdue:
		return billingpb.InvoiceStatus_INVOICE_STATUS_OVERDUE
	default:
		return billingpb.InvoiceStatus_INVOICE_STATUS_UNSPECIFIED
	}
}

func mapEmailStatusToProto(s string) billingpb.EmailStatus {
	switch s {
	case "PENDING":
		return billingpb.EmailStatus_EMAIL_STATUS_PENDING
	case "SENT", "SENDING":
		return billingpb.EmailStatus_EMAIL_STATUS_SENT
	case "DELIVERED":
		return billingpb.EmailStatus_EMAIL_STATUS_DELIVERED
	case "BOUNCED":
		return billingpb.EmailStatus_EMAIL_STATUS_BOUNCED
	case "DEAD_LETTER", "FAILED":
		return billingpb.EmailStatus_EMAIL_STATUS_DEAD_LETTER
	case "CANCELLED":
		return billingpb.EmailStatus_EMAIL_STATUS_CANCELLED
	default:
		return billingpb.EmailStatus_EMAIL_STATUS_UNSPECIFIED
	}
}

// mapProtoBillingRunStatuses converts proto billing run status enums to domain status strings.
func mapProtoBillingRunStatuses(statuses []billingpb.BillingRunStatus) []string {
	if len(statuses) == 0 {
		return nil
	}
	result := make([]string, 0, len(statuses))
	for _, s := range statuses {
		switch s {
		case billingpb.BillingRunStatus_BILLING_RUN_STATUS_INITIATED:
			result = append(result, string(domain.BillingRunStatusInitiated))
		case billingpb.BillingRunStatus_BILLING_RUN_STATUS_PROCESSING:
			result = append(result, string(domain.BillingRunStatusProcessing))
		case billingpb.BillingRunStatus_BILLING_RUN_STATUS_COMPLETED:
			result = append(result, string(domain.BillingRunStatusCompleted))
		case billingpb.BillingRunStatus_BILLING_RUN_STATUS_FAILED:
			result = append(result, string(domain.BillingRunStatusFailed))
		case billingpb.BillingRunStatus_BILLING_RUN_STATUS_UNSPECIFIED:
			// Skip unspecified status.
		}
	}
	return result
}

// mapProtoInvoiceStatuses converts proto invoice status enums to domain status strings.
func mapProtoInvoiceStatuses(statuses []billingpb.InvoiceStatus) []string {
	if len(statuses) == 0 {
		return nil
	}
	result := make([]string, 0, len(statuses))
	for _, s := range statuses {
		switch s {
		case billingpb.InvoiceStatus_INVOICE_STATUS_DRAFT:
			result = append(result, string(domain.InvoiceStatusDraft))
		case billingpb.InvoiceStatus_INVOICE_STATUS_ISSUED:
			result = append(result, string(domain.InvoiceStatusIssued))
		case billingpb.InvoiceStatus_INVOICE_STATUS_PAID:
			result = append(result, string(domain.InvoiceStatusPaid))
		case billingpb.InvoiceStatus_INVOICE_STATUS_VOID:
			result = append(result, string(domain.InvoiceStatusVoid))
		case billingpb.InvoiceStatus_INVOICE_STATUS_OVERDUE:
			result = append(result, string(domain.InvoiceStatusOverdue))
		case billingpb.InvoiceStatus_INVOICE_STATUS_UNSPECIFIED:
			// Skip unspecified status.
		}
	}
	return result
}
