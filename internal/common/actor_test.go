package common

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ctx      context.Context //nolint:containedctx // the table is the input
		expected string
	}{
		{
			name:     "a nil context has no actor",
			ctx:      nil,
			expected: "",
		},
		{
			name:     "a context nobody wrote to has no actor",
			ctx:      context.Background(),
			expected: "",
		},
		{
			name:     "the actor comes back as it was stored",
			ctx:      WithActor(context.Background(), "crawler"),
			expected: "crawler",
		},
		{
			name:     "an empty actor stays empty",
			ctx:      WithActor(context.Background(), ""),
			expected: "",
		},
		{
			name:     "the last actor wins",
			ctx:      WithActor(WithActor(context.Background(), "crawler"), "editor"),
			expected: "editor",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.expected, Actor(test.ctx))
		})
	}
}

func TestActorIgnoresAValueStoredUnderAnotherKey(t *testing.T) {
	t.Parallel()

	type otherKey struct{}

	ctx := context.WithValue(context.Background(), otherKey{}, "crawler")

	assert.Empty(t, Actor(ctx))
}
