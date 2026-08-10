package change

import (
	_ "embed"
	"sync"
)

//go:embed policy_examples.yaml
var examplePolicyYAML []byte

var (
	examplePolicyOnce sync.Once
	examplePolicySet  PolicySet
	examplePolicyErr  error
)

// ExamplePolicyYAML is the shipped example policy document, verbatim — what
// `vnproxctl policy examples` prints and what an operator copies to start
// their own. It is NOT the active policy set: vnprox's default set is empty
// and changes nothing until an operator installs one.
func ExamplePolicyYAML() []byte {
	out := make([]byte, len(examplePolicyYAML))
	copy(out, examplePolicyYAML)
	return out
}

// ExamplePolicySet is ExamplePolicyYAML parsed and validated. It returns an
// error only if the shipped file itself is broken, which policy_examples_test.go
// asserts it is not.
func ExamplePolicySet() (PolicySet, error) {
	examplePolicyOnce.Do(func() {
		examplePolicySet, examplePolicyErr = ParsePolicySet("policy_examples.yaml", examplePolicyYAML)
	})
	return examplePolicySet, examplePolicyErr
}
