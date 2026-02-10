package admin

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

func buildTestTree() *saga.CausationTreeNode {
	now := time.Now()
	errMsg := "connection timeout"
	return &saga.CausationTreeNode{
		SagaID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SagaName:    "payment_orchestrator",
		Status:      "COMPLETED",
		KnowledgeAt: &now,
		Steps: []saga.StepNode{
			{
				Index:      0,
				Name:       "validate",
				Status:     "COMPLETED",
				ExecutedAt: &now,
			},
			{
				Index:      1,
				Name:       "charge_card",
				Status:     "COMPLETED",
				ExecutedAt: &now,
				ChildSagas: []*saga.CausationTreeNode{
					{
						SagaID:   uuid.MustParse("22222222-2222-2222-2222-222222222222"),
						SagaName: "card_processor",
						Status:   "FAILED",
						FailedStep: &saga.FailedStep{
							Index:         0,
							Error:         "connection timeout",
							ErrorCategory: "TRANSIENT",
						},
						Steps: []saga.StepNode{
							{
								Index:      0,
								Name:       "process",
								Status:     "FAILED",
								ExecutedAt: &now,
								Error:      &errMsg,
							},
						},
					},
				},
			},
		},
	}
}

func TestExportJSON(t *testing.T) {
	tree := buildTestTree()

	var buf bytes.Buffer
	err := ExportCausationTree(&buf, tree, 2, ExportFormatJSON)
	require.NoError(t, err)

	// Verify valid JSON
	var export jsonExport
	err = json.Unmarshal(buf.Bytes(), &export)
	require.NoError(t, err)

	assert.Equal(t, 2, export.Depth)
	assert.NotEmpty(t, export.ExportedAt)
	assert.Equal(t, "payment_orchestrator", export.Tree.SagaName)
	assert.Len(t, export.Tree.Steps, 2)
	assert.Equal(t, "COMPLETED", export.Tree.Status)

	// Verify child saga is present
	require.Len(t, export.Tree.Steps[1].ChildSagas, 1)
	assert.Equal(t, "card_processor", export.Tree.Steps[1].ChildSagas[0].SagaName)
}

func TestExportCSV(t *testing.T) {
	tree := buildTestTree()

	var buf bytes.Buffer
	err := ExportCausationTree(&buf, tree, 2, ExportFormatCSV)
	require.NoError(t, err)

	// Parse CSV
	reader := csv.NewReader(strings.NewReader(buf.String()))
	records, err := reader.ReadAll()
	require.NoError(t, err)

	// Header + 2 parent steps + 1 child step = 4 rows
	require.Len(t, records, 4)

	// Verify header
	assert.Equal(t, "depth", records[0][0])
	assert.Equal(t, "saga_id", records[0][1])
	assert.Equal(t, "step_index", records[0][4])

	// Verify parent row depth=0
	assert.Equal(t, "0", records[1][0])
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", records[1][1])
	assert.Equal(t, "payment_orchestrator", records[1][2])
	assert.Equal(t, "0", records[1][4])
	assert.Equal(t, "validate", records[1][5])

	// Verify child row depth=1
	assert.Equal(t, "1", records[3][0])
	assert.Equal(t, "22222222-2222-2222-2222-222222222222", records[3][1])
	assert.Equal(t, "card_processor", records[3][2])
	assert.Contains(t, records[3][9], "connection timeout")
	// Parent saga ID is set
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", records[3][11])
}

func TestExportCSV_SagaWithNoSteps(t *testing.T) {
	tree := &saga.CausationTreeNode{
		SagaID:   uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		SagaName: "empty_saga",
		Status:   "PENDING",
		Steps:    []saga.StepNode{},
	}

	var buf bytes.Buffer
	err := ExportCausationTree(&buf, tree, 1, ExportFormatCSV)
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(buf.String()))
	records, err := reader.ReadAll()
	require.NoError(t, err)

	// Header + 1 row for the saga itself
	require.Len(t, records, 2)
	assert.Equal(t, "empty_saga", records[1][2])
	assert.Equal(t, "PENDING", records[1][3])
}

func TestExportUnsupportedFormat(t *testing.T) {
	var buf bytes.Buffer
	err := ExportCausationTree(&buf, nil, 0, "xml")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrUnsupportedExportFormat))
	assert.Contains(t, err.Error(), "xml")
}

func TestExportJSON_NilTree(t *testing.T) {
	var buf bytes.Buffer
	err := ExportCausationTree(&buf, nil, 0, ExportFormatJSON)
	require.NoError(t, err)

	var export jsonExport
	err = json.Unmarshal(buf.Bytes(), &export)
	require.NoError(t, err)
	assert.Nil(t, export.Tree)
}
