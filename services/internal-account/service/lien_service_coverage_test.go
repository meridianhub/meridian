package service

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/internal-account/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// persistLienWithRaceHandling – duplicate payment-order-ref race re-find path.
// ---------------------------------------------------------------------------

func TestPersistLienWithRaceHandling_DuplicateRefReturnsExisting(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-RACE-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("RACE-%s", uuid.New().String()[:6])
	acctUUID := insertTestAccount(t, db, accountID, accountCode)

	const ref = "PAY-RACE-DUP-001"

	// Seed an existing lien with the payment-order-reference directly via the repo.
	existing, err := domain.NewLien(acctUUID, 5000, "GBP", "", ref, nil)
	require.NoError(t, err)
	require.NoError(t, svc.lienRepo.Create(ctx, existing))

	// A second lien with the SAME payment-order-reference simulates a request that
	// passed the idempotency check but lost the create race. Create fails on the
	// unique constraint, so persistLienWithRaceHandling re-finds and returns the
	// existing lien instead of erroring.
	racing, err := domain.NewLien(acctUUID, 7500, "GBP", "", ref, nil)
	require.NoError(t, err)

	resp, opStatus, err := svc.persistLienWithRaceHandling(ctx, racing, ref)
	require.NoError(t, err)
	assert.Empty(t, opStatus)
	require.NotNil(t, resp)
	// Returned lien is the originally-seeded one (5000 minor units = 50.00 GBP).
	assert.Equal(t, existing.ID.String(), resp.Lien.LienId)
	assert.Equal(t, "50", resp.Lien.Amount.Amount)
}

func TestPersistLienWithRaceHandling_FreshCreateSucceeds(t *testing.T) {
	svc, db, ctx, cleanup := setupLienIntegrationDB(t)
	defer cleanup()

	accountID := fmt.Sprintf("IBA-RACEOK-%s", uuid.New().String()[:8])
	accountCode := fmt.Sprintf("RACEOK-%s", uuid.New().String()[:6])
	acctUUID := insertTestAccount(t, db, accountID, accountCode)

	lien, err := domain.NewLien(acctUUID, 1000, "GBP", "", "PAY-RACE-FRESH-001", nil)
	require.NoError(t, err)

	// No existing row → Create succeeds → returns (nil, "", nil).
	resp, opStatus, err := svc.persistLienWithRaceHandling(ctx, lien, "PAY-RACE-FRESH-001")
	require.NoError(t, err)
	assert.Empty(t, opStatus)
	assert.Nil(t, resp)
}
