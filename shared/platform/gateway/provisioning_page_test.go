package gateway

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/stretchr/testify/assert"
)

func TestFormatServiceName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"party", "Party"},
		{"reference_data", "Reference Data"},
		{"current_account", "Current Account"},
		{"account", "Account"},
		{"financial_accounting", "Financial Accounting"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, formatServiceName(tt.input))
		})
	}
}

func TestBuildServiceStatusData(t *testing.T) {
	now := time.Now()
	later := now.Add(2 * time.Second)
	errMsg := "migration failed"

	statuses := []domain.ProvisioningStatus{
		{ServiceName: "party", Status: domain.ServiceStatusCompleted, StartedAt: &now, CompletedAt: &later},
		{ServiceName: "account", Status: domain.ServiceStatusInProgress, StartedAt: &now},
		{ServiceName: "reference_data", Status: domain.ServiceStatusFailed, ErrorMessage: &errMsg},
		{ServiceName: "transaction", Status: domain.ServiceStatusPending},
	}

	result := buildServiceStatusData(statuses)

	assert.Len(t, result, 4)

	assert.Equal(t, "Party", result[0].Name)
	assert.Equal(t, "completed", result[0].Status)
	assert.Equal(t, "2s", result[0].Duration)

	assert.Equal(t, "Account", result[1].Name)
	assert.Equal(t, "in_progress", result[1].Status)
	assert.Empty(t, result[1].Duration)

	assert.Equal(t, "Reference Data", result[2].Name)
	assert.Equal(t, "failed", result[2].Status)
	assert.Equal(t, "migration failed", result[2].Error)

	assert.Equal(t, "Transaction", result[3].Name)
	assert.Equal(t, "pending", result[3].Status)
}

func TestServeProvisioningPage(t *testing.T) {
	rec := httptest.NewRecorder()

	data := provisioningPageData{
		TenantName: "Test Corp",
		TenantSlug: "test-corp",
		TenantID:   "tenant_abc",
		Status:     "provisioning",
		Services: []serviceStatusData{
			{Name: "Party", Status: "completed"},
			{Name: "Account", Status: "in_progress"},
		},
		StatusJSON: marshalServicesJSON([]serviceStatusData{
			{Name: "Party", Status: "completed"},
			{Name: "Account", Status: "in_progress"},
		}),
	}

	serveProvisioningPage(rec, data)

	assert.Equal(t, 503, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, rec.Header().Get("Cache-Control"), "no-cache")

	body := rec.Body.String()
	assert.Contains(t, body, "Test Corp")
	assert.Contains(t, body, "Setting up your workspace")
	assert.Contains(t, body, "provisioning-status")
}

func TestServeProvisioningStatusJSON(t *testing.T) {
	rec := httptest.NewRecorder()

	services := []serviceStatusData{
		{Name: "Party", Status: "completed", Duration: "1.5s"},
		{Name: "Account", Status: "in_progress"},
	}

	serveProvisioningStatusJSON(rec, "provisioning", services)

	assert.Equal(t, 200, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	body := rec.Body.String()
	assert.Contains(t, body, `"status":"provisioning"`)
	assert.Contains(t, body, `"name":"Party"`)
	assert.Contains(t, body, `"status":"completed"`)
}

func TestMarshalServicesJSON(t *testing.T) {
	services := []serviceStatusData{
		{Name: "Party", Status: "completed"},
	}
	result := marshalServicesJSON(services)
	assert.Contains(t, string(result), `"name":"Party"`)

	// Nil input returns empty array
	result = marshalServicesJSON(nil)
	assert.Equal(t, "[]", string(result))
}
