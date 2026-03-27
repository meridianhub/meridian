package worker

import (
	"context"

	"github.com/meridianhub/meridian/shared/pkg/email"
)

// OutboxFetcher adapts email.OutboxRepository to the dispatch.InstructionFetcher
// interface by wrapping FetchDispatchable and converting results to OutboxInstruction.
type OutboxFetcher struct {
	repo email.OutboxRepository
}

// NewOutboxFetcher creates a fetcher backed by the given OutboxRepository.
func NewOutboxFetcher(repo email.OutboxRepository) *OutboxFetcher {
	return &OutboxFetcher{repo: repo}
}

// FetchDispatchable returns up to limit outbox entries as OutboxInstructions.
func (f *OutboxFetcher) FetchDispatchable(ctx context.Context, limit int) ([]*OutboxInstruction, error) {
	entries, err := f.repo.FetchDispatchable(ctx, limit)
	if err != nil {
		return nil, err
	}

	instructions := make([]*OutboxInstruction, len(entries))
	for i, e := range entries {
		instructions[i] = &OutboxInstruction{Entry: e}
	}
	return instructions, nil
}
