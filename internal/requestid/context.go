// Package requestid stores the router-generated request correlation id in context.
package requestid

import "context"

type contextKey struct{}

func FromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	v, _ := ctx.Value(contextKey{}).(string)
	return v
}

func WithContext(ctx context.Context, id string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey{}, id)
}
