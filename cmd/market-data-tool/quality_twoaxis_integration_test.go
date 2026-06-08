//go:build integration

// Two-axis quality ladder integration test for the market-data-tool CSV import
// path. It imports a CSV carrying the four-level confidence grades plus the
// legacy REVISED label, maps each row through the same parsing the importer
// uses, persists the observations into a CockroachDB testcontainer, and asserts
// the quality (Axis A) and revision (Axis B) columns land correctly per ADR-0017.
//
// Run with: go test -tags=integration -v ./cmd/market-data-tool/...
package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	csvadapter "github.com/meridianhub/meridian/cmd/market-data-tool/internal/adapters/csv"
	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/infra"
	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/validation"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// twoAxisObservation is a minimal projection of the market_price_observation
// table covering the two axes of the quality ladder. The quality CHECK mirrors
// production (four-level ladder IN (1,2,3,4)) so the test also proves the
// four-level codes are accepted; revision defaults to 0 like the real column.
type twoAxisObservation struct {
	ID            string `gorm:"column:id;primaryKey"`
	ResolutionKey string `gorm:"column:resolution_key;not null"`
	Quality       int    `gorm:"column:quality;not null;check:quality IN (1,2,3,4)"`
	Revision      int    `gorm:"column:revision;not null;default:0"`
}

func (twoAxisObservation) TableName() string {
	return "market_price_observation_twoaxis_test"
}

func TestTwoAxisQualityImport_Integration(t *testing.T) {
	db, cleanup := testdb.SetupCockroachDB(t, []interface{}{&twoAxisObservation{}})
	defer cleanup()

	// CSV carries a PROVISIONAL and a VERIFIED row (the required four-level grades)
	// plus a legacy REVISED row to exercise the Axis-B revision mapping.
	csvData := `observed_at,quality_level,value
2024-01-15T10:30:00Z,PROVISIONAL,1.10
2024-01-15T11:30:00Z,VERIFIED,1.20
2024-01-15T12:30:00Z,REVISED,1.30`

	dataset := &infra.DataSetDefinition{Code: "TWO_AXIS_TEST"}
	parser := csvadapter.NewParser(dataset)

	var rows []csvadapter.ObservationRow
	_, err := parser.Parse(context.Background(), strings.NewReader(csvData), csvadapter.DefaultParseConfig(),
		func(batch csvadapter.RowBatch) error {
			require.Empty(t, batch.Errors, "CSV rows should parse cleanly")
			rows = append(rows, batch.Rows...)
			return nil
		})
	require.NoError(t, err)
	require.Len(t, rows, 3)

	// Persist each parsed row using the same quality/revision mapping the importer
	// applies (ParseQualityString -> Axis A grade + Axis B revision).
	for i, row := range rows {
		level, revision, parseErr := validation.ParseQualityString(row.QualityLevel)
		require.NoError(t, parseErr, "row %d quality %q", i, row.QualityLevel)

		record := twoAxisObservation{
			ID:            fmt.Sprintf("obs-%d", row.LineNumber),
			ResolutionKey: "TWO_AXIS_TEST",
			Quality:       level.Int(),
			Revision:      revision,
		}
		require.NoError(t, db.Create(&record).Error, "insert row %d", i)
	}

	// All three rows should be durably present before we assert their columns.
	err = await.Until(func() bool {
		var count int64
		if countErr := db.Model(&twoAxisObservation{}).Count(&count).Error; countErr != nil {
			return false
		}
		return count == 3
	})
	require.NoError(t, err)

	assertObservation(t, db, "obs-2", 2, 0) // PROVISIONAL -> quality 2, revision 0
	assertObservation(t, db, "obs-3", 4, 0) // VERIFIED    -> quality 4, revision 0
	assertObservation(t, db, "obs-4", 4, 1) // REVISED     -> quality 4, revision 1
}

// assertObservation reads back a persisted observation and asserts its Axis-A
// quality grade and Axis-B revision counter landed in the database.
func assertObservation(t *testing.T, db *gorm.DB, id string, wantQuality, wantRevision int) {
	t.Helper()
	var got twoAxisObservation
	require.NoError(t, db.First(&got, "id = ?", id).Error, "load %s", id)
	assert.Equal(t, wantQuality, got.Quality, "%s quality (Axis A)", id)
	assert.Equal(t, wantRevision, got.Revision, "%s revision (Axis B)", id)
}
