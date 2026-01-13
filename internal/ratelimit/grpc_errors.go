// Package ratelimit provides gRPC error helpers.
package ratelimit

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func grpcError(err error) error {
	if err == nil {
		return nil
	}
	code := CodeOf(err)
	grpcCode := codes.Internal
	switch code {
	case CodeInvalidInput, CodeInvalidCost:
		grpcCode = codes.InvalidArgument
	case CodeConflict:
		grpcCode = codes.FailedPrecondition
	case CodeNotFound:
		grpcCode = codes.NotFound
	case CodeUnauthorized:
		grpcCode = codes.Unauthenticated
	case CodeForbidden:
		grpcCode = codes.PermissionDenied
	}
	return status.Error(grpcCode, err.Error())
}
