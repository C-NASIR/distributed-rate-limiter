// Package grpctransport provides gRPC error helpers.
package grpctransport

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"ratelimit/internal/ratelimit/core"
)

func grpcError(err error) error {
	if err == nil {
		return nil
	}
	code := core.CodeOf(err)
	grpcCode := codes.Internal
	switch code {
	case core.CodeInvalidInput, core.CodeInvalidCost:
		grpcCode = codes.InvalidArgument
	case core.CodeConflict:
		grpcCode = codes.FailedPrecondition
	case core.CodeNotFound:
		grpcCode = codes.NotFound
	case core.CodeUnauthorized:
		grpcCode = codes.Unauthenticated
	case core.CodeForbidden:
		grpcCode = codes.PermissionDenied
	}
	return status.Error(grpcCode, err.Error())
}
