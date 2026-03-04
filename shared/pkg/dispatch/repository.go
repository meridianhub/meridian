package dispatch

import "context"

// InstructionFetcher abstracts the operation of fetching a batch of dispatchable
// instructions from a persistence store. Implementations should use row-level locking
// (e.g., SELECT FOR UPDATE SKIP LOCKED) to support concurrent workers.
type InstructionFetcher[I DispatchableInstruction] interface {
	// FetchDispatchable returns up to limit instructions that are ready for dispatch.
	FetchDispatchable(ctx context.Context, limit int) ([]I, error)
}

// InstructionPersister abstracts the operation of saving an instruction after
// processing (marking delivered, retrying, or failed).
type InstructionPersister[I DispatchableInstruction] interface {
	// SaveInstruction persists the current state of an instruction.
	SaveInstruction(ctx context.Context, instruction I) error
}
