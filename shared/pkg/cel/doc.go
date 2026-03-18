// Package cel provides CEL (Common Expression Language) compilation and evaluation
// for instrument validation and bucket key generation.
//
// CEL expressions are bounded — they cannot loop and are guaranteed to terminate
// in sub-millisecond time, making them safe for use in hot paths such as
// validation rules, pricing formulas, and bucket key derivation.
//
// Security constraints are enforced on all compiled expressions:
//   - Maximum expression length: 4 KB
//   - Determinism checks: no non-deterministic functions
//   - Cost limits prevent runaway evaluation
//
// # Usage
//
//	compiler := cel.NewCompiler()
//	prog, err := compiler.Compile(`amount > 0 && amount <= account.limit`)
//	result, err := prog.Eval(map[string]any{"amount": 500.0, "account": ...})
package cel
