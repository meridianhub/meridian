package sandbox

import (
	"errors"
	"fmt"
)

// ErrScriptTooLarge is returned when a script exceeds the configured MaxScriptSize.
var ErrScriptTooLarge = errors.New("script exceeds maximum size")

// ValidateScript checks that the script does not exceed the configured size limit.
func ValidateScript(script string, cfg Config) error {
	if len(script) > cfg.MaxScriptSize {
		return fmt.Errorf("%w: %d bytes exceeds %d", ErrScriptTooLarge, len(script), cfg.MaxScriptSize)
	}
	return nil
}
