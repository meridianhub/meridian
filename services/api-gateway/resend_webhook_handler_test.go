package gateway_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/pkg/email"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
)

// ─── Fake AuditRepository ────────────────────────────────────────────────────

type fakeAuditRepo struct {
	recorded      []recordedCall
	returnErr     error
	notFoundOnIDs map[string]bool
}

type recordedCall struct {
	providerID string
	status     email.AuditStatus
	payload    map[string]any
}

func (f *fakeAuditRepo) Record(_ context.Context, _ *email.AuditEntry) error {
	return f.returnErr
}

func (f *fakeAuditRepo) FindByOutboxID(_ context.Context, _ uuid.UUID) ([]email.AuditEntry, error) {
	return nil, nil
}

func (f *fakeAuditRepo) FindByProviderID(_ context.Context, _ string) ([]email.AuditEntry, error) {
	return nil, nil
}

func (f *fakeAuditRepo) RecordByProviderID(_ context.Context, providerID string, status email.AuditStatus, payload map[string]any) error {
	if f.notFoundOnIDs[providerID] {
		return email.ErrAuditEntryNotFound
	}
	if f.returnErr != nil {
		return f.returnErr
	}
	f.recorded = append(f.recorded, recordedCall{providerID: providerID, status: status, payload: payload})
	return nil
}

// ─── Signature helpers ───────────────────────────────────────────────────────

const testSecret = "whsec_dGVzdHNlY3JldGtleXRlc3RzZWNyZXRrZXk=" // base64("testsecretkeytestsecretkey" repeated)

func signPayload(t *testing.T, body []byte, msgID, secret string) (svixID, svixTimestamp, svixSignature string) {
	t.Helper()

	svixID = msgID
	svixTimestamp = fmt.Sprintf("%d", time.Now().Unix())

	keyB64 := secret[len("whsec_"):]
	key, err := base64.StdEncoding.DecodeString(keyB64)
	if err != nil {
		t.Fatalf("decode test secret: %v", err)
	}

	toSign := fmt.Sprintf("%s.%s.%s", svixID, svixTimestamp, string(body))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(toSign))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

	svixSignature = "v1," + sig
	return
}

// ─── Request builder ─────────────────────────────────────────────────────────

func buildWebhookRequest(t *testing.T, body []byte, secret string, overrideHeaders ...func(h http.Header)) *http.Request {
	t.Helper()
	msgID, ts, sig := signPayload(t, body, "msg_"+uuid.New().String(), secret)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/resend", bytes.NewReader(body))
	r.Header.Set("svix-id", msgID)
	r.Header.Set("svix-timestamp", ts)
	r.Header.Set("svix-signature", sig)
	for _, fn := range overrideHeaders {
		fn(r.Header)
	}
	return r
}

func buildPayload(t *testing.T, eventType, emailID string) []byte {
	t.Helper()
	p := map[string]any{
		"type":       eventType,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data": map[string]any{
			"email_id": emailID,
		},
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}

// ─── Tests ───────────────────────────────────────────────────────────────────

func TestResendWebhookHandler_ValidSignature(t *testing.T) {
	repo := &fakeAuditRepo{}
	handler := gateway.NewResendWebhookHandler(email.NewDeliveryStatusRecorder(repo, nil, nil), testSecret, slog.Default())

	body := buildPayload(t, "email.delivered", "resend-id-001")
	r := buildWebhookRequest(t, body, testSecret)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(repo.recorded) != 1 {
		t.Fatalf("expected 1 recorded call, got %d", len(repo.recorded))
	}
	if repo.recorded[0].status != email.AuditStatusDelivered {
		t.Errorf("expected DELIVERED, got %s", repo.recorded[0].status)
	}
	if repo.recorded[0].providerID != "resend-id-001" {
		t.Errorf("expected providerID 'resend-id-001', got %s", repo.recorded[0].providerID)
	}
}

func TestResendWebhookHandler_InvalidSignature(t *testing.T) {
	repo := &fakeAuditRepo{}
	handler := gateway.NewResendWebhookHandler(email.NewDeliveryStatusRecorder(repo, nil, nil), testSecret, slog.Default())

	body := buildPayload(t, "email.delivered", "resend-id-001")
	r := buildWebhookRequest(t, body, testSecret, func(h http.Header) {
		h.Set("svix-signature", "v1,invalidsignature")
	})
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if len(repo.recorded) != 0 {
		t.Errorf("expected no recorded calls, got %d", len(repo.recorded))
	}
}

func TestResendWebhookHandler_MissingHeaders(t *testing.T) {
	repo := &fakeAuditRepo{}
	handler := gateway.NewResendWebhookHandler(email.NewDeliveryStatusRecorder(repo, nil, nil), testSecret, slog.Default())

	body := buildPayload(t, "email.delivered", "resend-id-001")
	r := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/resend", bytes.NewReader(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestResendWebhookHandler_EventTypesRouteCorrectly(t *testing.T) {
	tests := []struct {
		eventType      string
		expectedStatus email.AuditStatus
	}{
		{"email.delivered", email.AuditStatusDelivered},
		{"email.bounced", email.AuditStatusBounced},
		{"email.complained", email.AuditStatusComplained},
	}

	for _, tc := range tests {
		t.Run(tc.eventType, func(t *testing.T) {
			repo := &fakeAuditRepo{}
			handler := gateway.NewResendWebhookHandler(email.NewDeliveryStatusRecorder(repo, nil, nil), testSecret, slog.Default())

			body := buildPayload(t, tc.eventType, "resend-id-test")
			r := buildWebhookRequest(t, body, testSecret)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, r)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", w.Code)
			}
			if len(repo.recorded) != 1 {
				t.Fatalf("expected 1 recorded call, got %d", len(repo.recorded))
			}
			if repo.recorded[0].status != tc.expectedStatus {
				t.Errorf("expected %s, got %s", tc.expectedStatus, repo.recorded[0].status)
			}
		})
	}
}

func TestResendWebhookHandler_UnknownEventType_Returns200(t *testing.T) {
	repo := &fakeAuditRepo{}
	handler := gateway.NewResendWebhookHandler(email.NewDeliveryStatusRecorder(repo, nil, nil), testSecret, slog.Default())

	body := buildPayload(t, "email.opened", "resend-id-001") // unknown type
	r := buildWebhookRequest(t, body, testSecret)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for unknown event type, got %d", w.Code)
	}
	if len(repo.recorded) != 0 {
		t.Errorf("expected no recorded calls for unknown event type, got %d", len(repo.recorded))
	}
}

func TestResendWebhookHandler_AuditEntryNotFound_Returns200(t *testing.T) {
	repo := &fakeAuditRepo{notFoundOnIDs: map[string]bool{"missing-id": true}}
	handler := gateway.NewResendWebhookHandler(email.NewDeliveryStatusRecorder(repo, nil, nil), testSecret, slog.Default())

	body := buildPayload(t, "email.delivered", "missing-id")
	r := buildWebhookRequest(t, body, testSecret)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	// Not-found on a webhook should still be acknowledged (200) to avoid Resend retries.
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for not-found provider ID, got %d", w.Code)
	}
}

func TestResendWebhookHandler_RepoError_Returns500(t *testing.T) {
	repo := &fakeAuditRepo{returnErr: errors.New("db connection lost")}
	handler := gateway.NewResendWebhookHandler(email.NewDeliveryStatusRecorder(repo, nil, nil), testSecret, slog.Default())

	body := buildPayload(t, "email.delivered", "resend-id-err")
	r := buildWebhookRequest(t, body, testSecret)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestResendWebhookHandler_WrongMethod(t *testing.T) {
	repo := &fakeAuditRepo{}
	handler := gateway.NewResendWebhookHandler(email.NewDeliveryStatusRecorder(repo, nil, nil), testSecret, slog.Default())

	r := httptest.NewRequest(http.MethodGet, "/api/v1/webhooks/resend", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", w.Code)
	}
}

func TestResendWebhookHandler_InvalidBody_Returns400(t *testing.T) {
	repo := &fakeAuditRepo{}
	handler := gateway.NewResendWebhookHandler(email.NewDeliveryStatusRecorder(repo, nil, nil), testSecret, slog.Default())

	body := []byte("not valid json")
	r := buildWebhookRequest(t, body, testSecret)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid JSON, got %d", w.Code)
	}
}

func TestResendWebhookHandler_MultipleSignatures_FirstInvalid(t *testing.T) {
	// Svix may send multiple signatures (key rotation). Verification should pass
	// if any of them match.
	repo := &fakeAuditRepo{}
	handler := gateway.NewResendWebhookHandler(email.NewDeliveryStatusRecorder(repo, nil, nil), testSecret, slog.Default())

	body := buildPayload(t, "email.delivered", "resend-id-multi")
	msgID, ts, validSig := signPayload(t, body, "msg_multi", testSecret)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/resend", bytes.NewReader(body))
	r.Header.Set("svix-id", msgID)
	r.Header.Set("svix-timestamp", ts)
	// First token is invalid, second is valid.
	r.Header.Set("svix-signature", "v1,invalidsig "+validSig)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with valid signature in multi-sig header, got %d", w.Code)
	}
}
