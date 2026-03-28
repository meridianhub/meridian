// Package service provides the AuditService gRPC implementation for querying
// audit trail entries stored in tenant-scoped audit_log tables.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	auditv1 "github.com/meridianhub/meridian/api/proto/meridian/audit/v1"
	"github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/gorm"
)

// Static errors.
var (
	ErrNilDatabase    = errors.New("database connection cannot be nil")
	ErrZeroCursorTime = errors.New("cursor contains zero timestamp")
)

// AuditService implements the AuditService gRPC service.
type AuditService struct {
	auditv1.UnimplementedAuditServiceServer
	db     *gorm.DB
	logger *slog.Logger
}

// NewAuditService creates a new audit service.
func NewAuditService(gormDB *gorm.DB, logger *slog.Logger) (*AuditService, error) {
	if gormDB == nil {
		return nil, ErrNilDatabase
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &AuditService{db: gormDB, logger: logger}, nil
}

// defaultPageSize is the default number of entries returned per page.
const defaultPageSize = 25

// maxPageSize is the maximum number of entries returned per page.
const maxPageSize = 100

// auditLogRow maps to a row in the audit_log table.
type auditLogRow struct {
	ID            string    `gorm:"column:id"`
	TableName     string    `gorm:"column:table_name"`
	Operation     string    `gorm:"column:operation"`
	RecordID      string    `gorm:"column:record_id"`
	OldValues     *string   `gorm:"column:old_values"`
	NewValues     *string   `gorm:"column:new_values"`
	CreatedAt     time.Time `gorm:"column:created_at"`
	ChangedBy     *string   `gorm:"column:changed_by"`
	TransactionID *string   `gorm:"column:transaction_id"`
}

// ListAuditEntries returns paginated audit entries with optional filtering.
func (s *AuditService) ListAuditEntries(
	ctx context.Context,
	req *auditv1.ListAuditEntriesRequest,
) (*auditv1.ListAuditEntriesResponse, error) {
	// Extract tenant from context (injected by gateway)
	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing tenant context")
	}
	_ = tenantID // used implicitly by WithGormTenantScope in the transaction

	pageSize := clampPageSize(int(req.GetPageSize()))

	var rows []auditLogRow

	// Execute query within tenant-scoped transaction (sets search_path)
	err := db.WithGormTenantTransaction(ctx, s.db, func(tx *gorm.DB) error {
		query := buildAuditQuery(tx, req)

		// Handle cursor pagination
		if req.GetPageToken() != "" {
			cursor, err := decodeCursor(req.GetPageToken())
			if err != nil {
				return status.Errorf(codes.InvalidArgument, "invalid page token: %v", err)
			}
			query = query.Where("created_at < ?", cursor)
		}

		// Fetch one extra to determine if there's a next page
		return query.Limit(pageSize + 1).Find(&rows).Error
	})
	if err != nil {
		// If the error is already a gRPC status, return it directly
		if _, ok := status.FromError(err); ok {
			return nil, err
		}
		s.logger.ErrorContext(ctx, "failed to query audit log", "error", err)
		return nil, status.Errorf(codes.Internal, "failed to query audit log")
	}

	return s.buildListResponse(ctx, rows, pageSize), nil
}

// clampPageSize applies pagination defaults and limits.
func clampPageSize(pageSize int) int {
	if pageSize == 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}
	return pageSize
}

// buildAuditQuery constructs the base audit log query with optional filters applied.
func buildAuditQuery(tx *gorm.DB, req *auditv1.ListAuditEntriesRequest) *gorm.DB {
	query := tx.Table("audit_log").
		Select("id, table_name, operation, record_id, old_values, new_values, created_at, changed_by").
		Order("created_at DESC")

	if req.GetTableName() != "" {
		query = query.Where("table_name = ?", req.GetTableName())
	}
	if req.GetOperation() != auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED {
		opStr := domain.ProtoToOperation(req.GetOperation())
		if opStr != "" {
			query = query.Where("operation = ?", opStr)
		}
	}
	if req.GetChangedBy() != "" {
		query = query.Where("changed_by = ?", req.GetChangedBy())
	}
	if req.GetRecordId() != "" {
		query = query.Where("record_id = ?", req.GetRecordId())
	}

	return query
}

// buildListResponse converts query rows into the paginated gRPC response.
func (s *AuditService) buildListResponse(ctx context.Context, rows []auditLogRow, pageSize int) *auditv1.ListAuditEntriesResponse {
	hasMore := len(rows) > pageSize
	if hasMore {
		rows = rows[:pageSize]
	}

	entries := make([]*auditv1.AuditLogEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := rowToProto(row)
		if err != nil {
			s.logger.WarnContext(ctx, "skipping malformed audit entry",
				"id", row.ID, "error", err)
			continue
		}
		entries = append(entries, entry)
	}

	resp := &auditv1.ListAuditEntriesResponse{Entries: entries}
	if hasMore && len(rows) > 0 {
		resp.NextPageToken = encodeCursor(rows[len(rows)-1].CreatedAt)
	}

	return resp
}

// rowToProto converts a database row to a protobuf AuditLogEntry.
func rowToProto(row auditLogRow) (*auditv1.AuditLogEntry, error) {
	entry := &auditv1.AuditLogEntry{
		EntryId:   row.ID,
		Timestamp: timestamppb.New(row.CreatedAt),
		TableName: row.TableName,
		Operation: stringToOperation(row.Operation),
		RecordId:  row.RecordID,
	}

	if row.ChangedBy != nil {
		entry.ChangedBy = *row.ChangedBy
	}

	if row.OldValues != nil && *row.OldValues != "" {
		s, err := jsonToStruct(*row.OldValues)
		if err != nil {
			return nil, fmt.Errorf("old_values: %w", err)
		}
		entry.OldValues = s
	}

	if row.NewValues != nil && *row.NewValues != "" {
		s, err := jsonToStruct(*row.NewValues)
		if err != nil {
			return nil, fmt.Errorf("new_values: %w", err)
		}
		entry.NewValues = s
	}

	return entry, nil
}

// jsonToStruct converts a JSON string to a protobuf Struct.
func jsonToStruct(jsonStr string) (*structpb.Struct, error) {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &m); err != nil {
		return nil, fmt.Errorf("unmarshal json: %w", err)
	}
	return structpb.NewStruct(m)
}

// stringToOperation converts a string operation to the protobuf enum.
func stringToOperation(op string) auditv1.AuditOperation {
	switch op {
	case "INSERT":
		return auditv1.AuditOperation_AUDIT_OPERATION_INSERT
	case "UPDATE":
		return auditv1.AuditOperation_AUDIT_OPERATION_UPDATE
	case "DELETE":
		return auditv1.AuditOperation_AUDIT_OPERATION_DELETE
	case "INITIAL_IMPORT":
		return auditv1.AuditOperation_AUDIT_OPERATION_INITIAL_IMPORT
	default:
		return auditv1.AuditOperation_AUDIT_OPERATION_UNSPECIFIED
	}
}

// cursorPayload is the JSON structure encoded in the page token.
type cursorPayload struct {
	CreatedAt time.Time `json:"c"`
}

// encodeCursor encodes a timestamp into a base64 page token.
func encodeCursor(t time.Time) string {
	payload := cursorPayload{CreatedAt: t}
	b, _ := json.Marshal(payload)
	return base64.URLEncoding.EncodeToString(b)
}

// decodeCursor decodes a base64 page token into a timestamp.
func decodeCursor(token string) (time.Time, error) {
	b, err := base64.URLEncoding.DecodeString(token)
	if err != nil {
		return time.Time{}, fmt.Errorf("decode base64: %w", err)
	}
	var payload cursorPayload
	if err := json.Unmarshal(b, &payload); err != nil {
		return time.Time{}, fmt.Errorf("unmarshal cursor: %w", err)
	}
	if payload.CreatedAt.IsZero() {
		return time.Time{}, ErrZeroCursorTime
	}
	return payload.CreatedAt, nil
}
