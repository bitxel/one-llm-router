package oauth

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPKCE(t *testing.T) {
	t.Run("generate state length and uniqueness", func(t *testing.T) {
		seen := make(map[string]struct{}, 1000)
		for i := range 1000 {
			state, err := GenerateState()
			if !assert.NoError(t, err) {
				return
			}
			assert.Len(t, state, 43)
			if _, exists := seen[state]; exists {
				t.Fatalf("duplicate state at iteration %d: %q", i, state)
			}
			seen[state] = struct{}{}
		}
	})

	t.Run("generate code verifier", func(t *testing.T) {
		verifier, err := GenerateCodeVerifier()
		if !assert.NoError(t, err) {
			return
		}
		assert.Len(t, verifier, 43)
	})

	t.Run("generate state surfaces rand errors", func(t *testing.T) {
		old := pkceRandRead
		pkceRandRead = func(_ []byte) (int, error) { return 0, errors.New("rand failed") }
		defer func() { pkceRandRead = old }()

		_, err := GenerateState()
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "GenerateState")
			assert.Contains(t, err.Error(), "rand failed")
		}
	})

	t.Run("generate verifier surfaces short reads", func(t *testing.T) {
		old := pkceRandRead
		pkceRandRead = func(_ []byte) (int, error) { return 31, nil }
		defer func() { pkceRandRead = old }()

		_, err := GenerateCodeVerifier()
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "GenerateCodeVerifier")
		}
	})

	t.Run("code challenge", func(t *testing.T) {
		assert.Equal(t, codeChallengeForV, CodeChallenge("v"))
	})

	t.Run("constant time compare equal", func(t *testing.T) {
		assert.True(t, CompareStatesConstantTime("same-state", "same-state"))
	})

	t.Run("constant time compare different lengths", func(t *testing.T) {
		assert.NotPanics(t, func() {
			assert.False(t, CompareStatesConstantTime("short", "longer"))
		})
	})

	t.Run("constant time compare different values", func(t *testing.T) {
		assert.False(t, CompareStatesConstantTime("state-a", "state-b"))
	})
}
