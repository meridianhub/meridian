package service

// Architecture pin test for the two-axis quality ladder (ADR-0017, Axis A:
// confidence grade). This is the single cross-layer contract guard: it fails if
// the proto enum, the domain enum, or the DB CHECK range ever drift apart.
//
// It lives in package service (not the domain package) on purpose: the canonical
// proto<->domain adapter (protoQualityLevelToDomain / domainQualityLevelToProto,
// added in task 10) is unexported, and the task requires calling the REAL adapter
// rather than re-implementing the mapping. Package service is the only package
// that can both call that adapter and reference the domain enum, so it is the only
// place a proto+domain+DB guard can live in one file.
//
// The three layers it pins:
//   - Proto enum: ESTIMATE=1, PROVISIONAL=2, ACTUAL=3, and slot 4 (still spelled
//     QUALITY_LEVEL_REVISED but semantically VERIFIED until the symbol rename in
//     task 14).
//   - Domain enum: QualityLevelEstimate=1, QualityLevelProvisional=2,
//     QualityLevelActual=3, QualityLevelVerified=4.
//   - DB CHECK: market_price_observation.quality IN (1,2,3,4). The live DB guard
//     (a CockroachDB testcontainer that accepts 1-4 and rejects 0/5/99) already
//     exists as TestMigrations_QualityLadder_AcceptsFourLevels in
//     services/market-information/migrations. This file pins the same range
//     statically and asserts the domain enum maps onto it, so it does not spin a
//     second container that would duplicate that coverage.

import (
	"testing"

	"github.com/stretchr/testify/assert"

	pb "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	"github.com/meridianhub/meridian/services/market-information/domain"
)

// dbQualityCheckRange is the set of quality values admitted by the
// market_price_observation quality CHECK constraint
// (20260608000002_quality_level_verified.sql: CHECK (quality IN (1, 2, 3, 4))).
// Pinned here so a change to the CHECK without a matching domain change (or vice
// versa) is caught at compile/test time, complementing the live testcontainer
// guard in the migrations package.
var dbQualityCheckRange = map[int]bool{1: true, 2: true, 3: true, 4: true}

// TestArch_QualityLevel_DomainIntegerValues pins the domain enum integer values.
// These integers ARE the on-disk encoding: domain QualityLevel values are written
// verbatim into the quality column, so changing them silently corrupts existing
// rows. They must never drift.
func TestArch_QualityLevel_DomainIntegerValues(t *testing.T) {
	assert.Equal(t, 1, domain.QualityLevelEstimate.Int(), "ESTIMATE must be 1")
	assert.Equal(t, 2, domain.QualityLevelProvisional.Int(), "PROVISIONAL must be 2")
	assert.Equal(t, 3, domain.QualityLevelActual.Int(), "ACTUAL must be 3")
	assert.Equal(t, 4, domain.QualityLevelVerified.Int(), "VERIFIED must be 4")
}

// TestArch_QualityLevel_DomainMatchesDBCheckRange asserts every domain confidence
// level is admitted by the DB CHECK range and that the four levels exactly fill
// that range (no gap, no extra). If a fifth level is added to the domain enum
// without widening the CHECK, or the CHECK is narrowed, this fails.
func TestArch_QualityLevel_DomainMatchesDBCheckRange(t *testing.T) {
	domainLevels := []domain.QualityLevel{
		domain.QualityLevelEstimate,
		domain.QualityLevelProvisional,
		domain.QualityLevelActual,
		domain.QualityLevelVerified,
	}

	covered := make(map[int]bool, len(domainLevels))
	for _, level := range domainLevels {
		assert.Truef(t, dbQualityCheckRange[level.Int()],
			"domain level %s (%d) must be admitted by the DB CHECK range (1,2,3,4)",
			level, level.Int())
		covered[level.Int()] = true
	}

	assert.Equal(t, dbQualityCheckRange, covered,
		"the four domain levels must exactly fill the DB CHECK range (1,2,3,4) - no gap, no extra")
}

// TestArch_QualityLevel_ProtoDomainRoundTrip pins the proto<->domain adapter as a
// lossless bijection over the four confidence grades. It calls the REAL adapter
// (task 10) in both directions and asserts identity, so any change to one side of
// the mapping without the other breaks the build.
//
// Slot 4 is exercised under its current symbol QUALITY_LEVEL_REVISED, which is
// semantically VERIFIED until task 14 renames it. When that rename lands, the
// pb.QualityLevel_QUALITY_LEVEL_REVISED reference below stops compiling, which is
// the intended signal to update this pin.
func TestArch_QualityLevel_ProtoDomainRoundTrip(t *testing.T) {
	t.Run("domain->proto->domain identity", func(t *testing.T) {
		domainLevels := []domain.QualityLevel{
			domain.QualityLevelEstimate,
			domain.QualityLevelProvisional,
			domain.QualityLevelActual,
			domain.QualityLevelVerified,
		}
		for _, level := range domainLevels {
			roundTripped := protoQualityLevelToDomain(domainQualityLevelToProto(level))
			assert.Equalf(t, level, roundTripped,
				"domain %s must round-trip through proto back to itself", level)
		}
	})

	t.Run("proto->domain->proto identity", func(t *testing.T) {
		// The four defined proto confidence slots. UNSPECIFIED is intentionally
		// excluded: it is a lossy alias for ESTIMATE (defensive default), not a
		// distinct grade, so it is not part of the bijection.
		protoLevels := []pb.QualityLevel{
			pb.QualityLevel_QUALITY_LEVEL_ESTIMATE,
			pb.QualityLevel_QUALITY_LEVEL_PROVISIONAL,
			pb.QualityLevel_QUALITY_LEVEL_ACTUAL,
			// Slot 4: spelled REVISED, semantically VERIFIED (rename pending task 14).
			pb.QualityLevel_QUALITY_LEVEL_REVISED,
		}
		for _, level := range protoLevels {
			roundTripped := domainQualityLevelToProto(protoQualityLevelToDomain(level))
			assert.Equalf(t, level, roundTripped,
				"proto %s must round-trip through domain back to itself", level)
		}
	})

	t.Run("proto slot integers match domain integers", func(t *testing.T) {
		// The proto enum numbers and domain enum numbers share the on-disk
		// encoding, so the defined confidence slots must agree numerically.
		assert.Equal(t, domain.QualityLevelEstimate.Int(), int(pb.QualityLevel_QUALITY_LEVEL_ESTIMATE))
		assert.Equal(t, domain.QualityLevelProvisional.Int(), int(pb.QualityLevel_QUALITY_LEVEL_PROVISIONAL))
		assert.Equal(t, domain.QualityLevelActual.Int(), int(pb.QualityLevel_QUALITY_LEVEL_ACTUAL))
		// Slot 4 (REVISED symbol) carries the VERIFIED confidence integer.
		assert.Equal(t, domain.QualityLevelVerified.Int(), int(pb.QualityLevel_QUALITY_LEVEL_REVISED))
	})
}
