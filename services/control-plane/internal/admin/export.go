package admin

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/saga"
)

// ErrUnsupportedExportFormat is returned when an unsupported export format is requested.
var ErrUnsupportedExportFormat = errors.New("unsupported export format")

// ExportFormat defines the output format for causation tree exports.
type ExportFormat string

const (
	// ExportFormatJSON exports the causation tree as JSON.
	ExportFormatJSON ExportFormat = "json"
	// ExportFormatCSV exports the causation tree as a flattened CSV.
	ExportFormatCSV ExportFormat = "csv"
)

// ExportCausationTree writes the causation tree to the given writer in the
// specified format. JSON preserves the tree structure; CSV flattens it into
// rows suitable for spreadsheet analysis by auditors.
func ExportCausationTree(w io.Writer, tree *saga.CausationTreeNode, depth int, format ExportFormat) error {
	switch format {
	case ExportFormatJSON:
		return exportJSON(w, tree, depth)
	case ExportFormatCSV:
		return exportCSV(w, tree)
	default:
		return fmt.Errorf("%w: %s", ErrUnsupportedExportFormat, format)
	}
}

// jsonExport wraps the tree with metadata for JSON export.
type jsonExport struct {
	ExportedAt string                  `json:"exported_at"`
	Depth      int                     `json:"depth"`
	Tree       *saga.CausationTreeNode `json:"tree"`
}

func exportJSON(w io.Writer, tree *saga.CausationTreeNode, depth int) error {
	export := jsonExport{
		ExportedAt: time.Now().UTC().Format(time.RFC3339),
		Depth:      depth,
		Tree:       tree,
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(export)
}

func exportCSV(w io.Writer, tree *saga.CausationTreeNode) error {
	writer := csv.NewWriter(w)

	// Write header
	header := []string{
		"depth", "saga_id", "saga_name", "saga_status",
		"step_index", "step_name", "step_status", "executed_at",
		"step_error", "failed_step", "knowledge_at", "parent_saga",
	}
	if err := writer.Write(header); err != nil {
		return fmt.Errorf("write CSV header: %w", err)
	}

	// Flatten tree into rows
	if err := flattenTree(writer, tree, "", 0); err != nil {
		return err
	}

	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("flush CSV: %w", err)
	}

	return nil
}

func flattenTree(w *csv.Writer, node *saga.CausationTreeNode, parentSagaID string, depth int) error {
	if node == nil {
		return nil
	}

	knowledgeAt := formatOptionalTime(node.KnowledgeAt)
	failedStep := formatFailedStep(node.FailedStep)

	if len(node.Steps) == 0 {
		// Saga with no steps - write a single row
		if err := w.Write([]string{
			fmt.Sprintf("%d", depth),
			node.SagaID.String(),
			node.SagaName,
			node.Status,
			"", "", "", "",
			"",
			failedStep,
			knowledgeAt,
			parentSagaID,
		}); err != nil {
			return fmt.Errorf("write CSV row: %w", err)
		}
		return nil
	}

	for _, step := range node.Steps {
		if err := writeStepRow(w, node, step, failedStep, knowledgeAt, parentSagaID, depth); err != nil {
			return err
		}

		for _, child := range step.ChildSagas {
			if err := flattenTree(w, child, node.SagaID.String(), depth+1); err != nil {
				return err
			}
		}
	}

	return nil
}

// writeStepRow writes a single step row to the CSV writer.
func writeStepRow(w *csv.Writer, node *saga.CausationTreeNode, step saga.CausationStepInfo, failedStep, knowledgeAt, parentSagaID string, depth int) error {
	executedAt := formatOptionalTime(step.ExecutedAt)
	stepError := ""
	if step.Error != nil {
		stepError = *step.Error
	}

	if err := w.Write([]string{
		fmt.Sprintf("%d", depth),
		node.SagaID.String(),
		node.SagaName,
		node.Status,
		fmt.Sprintf("%d", step.Index),
		step.Name,
		step.Status,
		executedAt,
		stepError,
		failedStep,
		knowledgeAt,
		parentSagaID,
	}); err != nil {
		return fmt.Errorf("write CSV row: %w", err)
	}
	return nil
}

// formatOptionalTime formats a *time.Time as RFC3339 or returns empty string if nil.
func formatOptionalTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

// formatFailedStep formats a failed step summary or returns empty string if nil.
func formatFailedStep(fs *saga.FailedStepInfo) string {
	if fs == nil {
		return ""
	}
	return fmt.Sprintf("step %d: %s (%s)", fs.Index, fs.Error, fs.ErrorCategory)
}
