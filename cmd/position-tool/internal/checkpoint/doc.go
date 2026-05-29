// Package checkpoint provides checkpoint persistence for bulk import operations.
//
// The checkpoint manager enables resume capability for interrupted bulk imports
// by tracking import progress in the import_manifest database table. It handles:
//
//   - File checksum calculation (SHA256) for duplicate import detection
//   - Progress tracking (processed rows, success/failure counts)
//   - Rollback SQL storage for atomic recovery of partial imports
//   - Resume from last checkpoint with file integrity verification
//
// # Architecture
//
// The checkpoint system uses PostgreSQL's import_manifest table to persist state:
//
//	import_manifest
//	├── id (UUID) - Unique manifest identifier
//	├── tenant_id - Tenant identifier for isolation
//	├── source_file - Path to the source CSV file
//	├── file_checksum - SHA256 checksum for integrity
//	├── total_rows - Total rows in file (set after parsing)
//	├── processed_rows - Rows processed so far
//	├── success_count - Successfully imported rows
//	├── failure_count - Failed rows
//	├── status - RUNNING, COMPLETED, FAILED, CANCELLED
//	├── rollback_sql - SQL statements to undo import
//	└── timestamps - created_at, updated_at
//
// # Usage
//
// Basic workflow for a new import:
//
//	manager, err := checkpoint.NewManager(pool)
//	if err != nil {
//	    return err
//	}
//
//	// Start new import
//	cp, err := manager.StartImport(ctx, "tenant_id", "/path/to/file.csv")
//	if err != nil {
//	    if errors.Is(err, checkpoint.ErrDuplicateImport) {
//	        // File already imported successfully
//	    }
//	    return err
//	}
//
//	// Set total rows after file parsing
//	cp.SetTotalRows(totalRows)
//
//	// Process batches and update progress
//	for batch := range batches {
//	    cp.IncrementSuccess(len(batch))
//	    cp.AddRollbackStatement(fmt.Sprintf("DELETE FROM positions WHERE batch_id = '%s'", batchID))
//
//	    if err := manager.UpdateProgress(ctx, cp); err != nil {
//	        return err
//	    }
//	}
//
//	// Complete the import
//	return manager.Complete(ctx, cp)
//
// # Resume Workflow
//
// To resume an interrupted import:
//
//	// Resume by manifest ID (from CLI --resume-from flag)
//	cp, err := manager.ResumeByID(ctx, manifestID)
//	if err != nil {
//	    if errors.Is(err, checkpoint.ErrChecksumMismatch) {
//	        // File was modified - cannot resume safely
//	    }
//	    return err
//	}
//
//	// Skip already-processed lines (header on line 1, so M processed
//	// data rows occupy lines 2..M+1; the next line to process is M+2).
//	startLine := cp.ProcessedRows + 2
//
// # Rollback
//
// To rollback a partial import:
//
//	if err := manager.Rollback(ctx, cp); err != nil {
//	    return err
//	}
//
// The rollback executes stored DELETE statements in a single transaction,
// then marks the checkpoint as CANCELLED.
//
// # Error Handling
//
// The package defines several sentinel errors:
//
//   - ErrNilPool: Database pool is nil
//   - ErrCheckpointNotFound: No checkpoint exists for resume
//   - ErrDuplicateImport: File already successfully imported
//   - ErrImportInProgress: Import already running for this file
//   - ErrFileNotFound: Source file does not exist
//   - ErrChecksumMismatch: File modified since checkpoint
//
// # Thread Safety
//
// The CheckpointManager is safe for concurrent use. However, the Checkpoint
// struct methods (IncrementSuccess, etc.) are NOT thread-safe and should
// only be called from a single goroutine.
package checkpoint
