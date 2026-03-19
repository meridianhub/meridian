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

func TestExportCSV_NilTree(t *testing.T) {
	var buf bytes.Buffer
	err := ExportCausationTree(&buf, nil, 0, ExportFormatCSV)
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(buf.String()))
	records, err := reader.ReadAll()
	require.NoError(t, err)

	// Should only contain the header row (flattenTree returns nil for nil node)
	require.Len(t, records, 1)
	assert.Equal(t, "depth", records[0][0])
}

func TestExportCSV_DeepNesting(t *testing.T) {
	now := time.Now()
	grandchild := &saga.CausationTreeNode{
		SagaID:   uuid.MustParse("33333333-3333-3333-3333-333333333333"),
		SagaName: "grandchild_saga",
		Status:   "COMPLETED",
		Steps: []saga.StepNode{
			{
				Index:      0,
				Name:       "gc_step",
				Status:     "COMPLETED",
				ExecutedAt: &now,
			},
		},
	}

	child := &saga.CausationTreeNode{
		SagaID:   uuid.MustParse("22222222-2222-2222-2222-222222222222"),
		SagaName: "child_saga",
		Status:   "COMPLETED",
		Steps: []saga.StepNode{
			{
				Index:      0,
				Name:       "child_step",
				Status:     "COMPLETED",
				ExecutedAt: &now,
				ChildSagas: []*saga.CausationTreeNode{grandchild},
			},
		},
	}

	root := &saga.CausationTreeNode{
		SagaID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SagaName: "root_saga",
		Status:   "COMPLETED",
		Steps: []saga.StepNode{
			{
				Index:      0,
				Name:       "root_step",
				Status:     "COMPLETED",
				ExecutedAt: &now,
				ChildSagas: []*saga.CausationTreeNode{child},
			},
		},
	}

	var buf bytes.Buffer
	err := ExportCausationTree(&buf, root, 3, ExportFormatCSV)
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(buf.String()))
	records, err := reader.ReadAll()
	require.NoError(t, err)

	// Header + root_step(depth 0) + child_step(depth 1) + gc_step(depth 2) = 4 rows
	require.Len(t, records, 4)

	// Verify depths
	assert.Equal(t, "0", records[1][0]) // root
	assert.Equal(t, "1", records[2][0]) // child
	assert.Equal(t, "2", records[3][0]) // grandchild

	// Verify parent saga chain
	assert.Equal(t, "", records[1][11])                                          // root has no parent
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", records[2][11])      // child's parent is root
	assert.Equal(t, "22222222-2222-2222-2222-222222222222", records[3][11])      // grandchild's parent is child
}

func TestFlattenTree_NilKnowledgeAt(t *testing.T) {
	// Node with nil KnowledgeAt and nil FailedStep
	tree := &saga.CausationTreeNode{
		SagaID:      uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SagaName:    "test_saga",
		Status:      "PENDING",
		KnowledgeAt: nil,
		FailedStep:  nil,
		Steps:       []saga.StepNode{},
	}

	var buf bytes.Buffer
	err := ExportCausationTree(&buf, tree, 1, ExportFormatCSV)
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(buf.String()))
	records, err := reader.ReadAll()
	require.NoError(t, err)

	require.Len(t, records, 2)
	// knowledgeAt column should be empty
	assert.Equal(t, "", records[1][10])
	// failedStep column should be empty
	assert.Equal(t, "", records[1][9])
}

func TestFlattenTree_StepWithNilExecutedAtAndError(t *testing.T) {
	tree := &saga.CausationTreeNode{
		SagaID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
		SagaName: "test_saga",
		Status:   "PENDING",
		Steps: []saga.StepNode{
			{
				Index:      0,
				Name:       "pending_step",
				Status:     "PENDING",
				ExecutedAt: nil,
				Error:      nil,
			},
		},
	}

	var buf bytes.Buffer
	err := ExportCausationTree(&buf, tree, 1, ExportFormatCSV)
	require.NoError(t, err)

	reader := csv.NewReader(strings.NewReader(buf.String()))
	records, err := reader.ReadAll()
	require.NoError(t, err)

	require.Len(t, records, 2)
	// executedAt column should be empty
	assert.Equal(t, "", records[1][7])
	// stepError column should be empty
	assert.Equal(t, "", records[1][8])
}

type errorWriter struct {
	failAfter int
	written   int
}

func (e *errorWriter) Write(p []byte) (n int, err error) {
	e.written++
	if e.written > e.failAfter {
		return 0, errors.New("write error")
	}
	return len(p), nil
}

func TestExportJSON_WriteError(t *testing.T) {
	tree := buildTestTree()
	w := &errorWriter{failAfter: 0}

	err := ExportCausationTree(w, tree, 2, ExportFormatJSON)
	require.Error(t, err)
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
