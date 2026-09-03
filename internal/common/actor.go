package common

import "context"

// actorKey addresses the identity of whoever authenticated the request,
// so a database hook running far from the handler can still record who
// asked for the change.
type actorKey struct{}

// WithActor returns a context carrying actor.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey{}, actor)
}

// Actor returns the actor carried by ctx. An unauthenticated request and
// a token with no subject both give an empty string.
func Actor(ctx context.Context) string {
	if ctx == nil {
		return ""
	}

	actor, _ := ctx.Value(actorKey{}).(string)

	return actor
}
