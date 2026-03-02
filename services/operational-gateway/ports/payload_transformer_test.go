package ports_test

import (
	"context"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/services/operational-gateway/ports"
)

// stubTransformer is a minimal in-test implementation used to verify the interface contract.
type stubTransformer struct {
	outboundBody    []byte
	outboundHeaders map[string]string
	outboundErr     error
	inboundOutcome  *ports.InstructionOutcome
	inboundErr      error
}

func (s *stubTransformer) TransformOutbound(_ context.Context, _ *domain.Instruction, _ *ports.InstructionRoute) ([]byte, map[string]string, error) {
	return s.outboundBody, s.outboundHeaders, s.outboundErr
}

func (s *stubTransformer) TransformInbound(_ context.Context, _ int, _ []byte, _ *ports.InstructionRoute) (*ports.InstructionOutcome, error) {
	return s.inboundOutcome, s.inboundErr
}

// TestPayloadTransformer_Interface is a compile-time assertion that stubTransformer
// satisfies the PayloadTransformer interface.
func TestPayloadTransformer_Interface(_ *testing.T) {
	var _ ports.PayloadTransformer = &stubTransformer{}
}

// TestErrMappingNotFound_IsSentinel verifies that ErrMappingNotFound can be identified
// via errors.Is for use in error handling chains.
func TestErrMappingNotFound_IsSentinel(t *testing.T) {
	err := ports.ErrMappingNotFound
	if !errors.Is(err, ports.ErrMappingNotFound) {
		t.Fatalf("expected ErrMappingNotFound to be detectable via errors.Is")
	}
}

// TestErrTransformFailed_IsSentinel verifies that ErrTransformFailed can be identified
// via errors.Is for use in error handling chains.
func TestErrTransformFailed_IsSentinel(t *testing.T) {
	err := ports.ErrTransformFailed
	if !errors.Is(err, ports.ErrTransformFailed) {
		t.Fatalf("expected ErrTransformFailed to be detectable via errors.Is")
	}
}
