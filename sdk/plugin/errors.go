// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/protoadapt"
	"google.golang.org/protobuf/types/known/durationpb"

	jobsv1 "github.com/panelarr/source-api/gen/panelarr/jobs/v1"
	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
)

// ErrorDomain is the google.rpc.ErrorInfo domain of source errors.
const ErrorDomain = "source.panelarr"

// Code is a status code. The values are the gRPC/Connect codes.
type Code = connect.Code

// Codes plugins return most often.
const (
	CodeInvalidArgument    = connect.CodeInvalidArgument
	CodeNotFound           = connect.CodeNotFound
	CodeUnimplemented      = connect.CodeUnimplemented
	CodeUnavailable        = connect.CodeUnavailable
	CodeResourceExhausted  = connect.CodeResourceExhausted
	CodeFailedPrecondition = connect.CodeFailedPrecondition
	CodeUnauthenticated    = connect.CodeUnauthenticated
	CodePermissionDenied   = connect.CodePermissionDenied
	CodeInternal           = connect.CodeInternal
	CodeAlreadyExists      = connect.CodeAlreadyExists
)

// Reason is why a request failed because of the upstream source: an
// ErrorReason name without its prefix, e.g. "SOURCE_DOWN". It travels as the
// reason of a google.rpc.ErrorInfo.
type Reason string

// Reasons of the source API (sourcev1.ErrorReason).
const (
	ReasonChallenge     Reason = "CLOUDFLARE_CHALLENGE"
	ReasonRateLimited   Reason = "RATE_LIMITED"
	ReasonSourceDown    Reason = "SOURCE_DOWN"
	ReasonNotFound      Reason = "NOT_FOUND"
	ReasonLoginRequired Reason = "LOGIN_REQUIRED"
	ReasonUnsupported   Reason = "UNSUPPORTED"
	// Package origins (e.g. extension stores).
	ReasonStoreFetchFailed Reason = "STORE_FETCH_FAILED"
	ReasonStoreInvalid     Reason = "STORE_INVALID"
	// Jobs.
	ReasonAlreadyRunning Reason = "ALREADY_RUNNING"
	ReasonJobDisabled    Reason = "JOB_DISABLED"
)

// ReasonOf turns an ErrorReason value into its wire name.
func ReasonOf(r sourcev1.ErrorReason) Reason {
	if r == sourcev1.ErrorReason_ERROR_REASON_UNSPECIFIED {
		return ""
	}
	return Reason(strings.TrimPrefix(r.String(), "ERROR_REASON_"))
}

// Error is an error with a status code and, for upstream failures, a
// reason, carried across the process or network boundary between plugin and
// host.
type Error struct {
	Code    Code
	Reason  Reason
	Message string
	// RetryAfter tells the caller when to try again, e.g. after the source
	// rate-limited the plugin. Zero means no hint.
	RetryAfter time.Duration
	// CurrentRunID is the running run, when starting a job that is already
	// running (reason ALREADY_RUNNING).
	CurrentRunID string
	// Metadata is extra detail for the ErrorInfo, e.g. the job_id of
	// JOB_DISABLED.
	Metadata map[string]string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.Code.String())
	if e.Reason != "" {
		b.WriteString(" (" + string(e.Reason) + ")")
	}
	b.WriteString(": " + e.Message)
	if e.RetryAfter > 0 {
		fmt.Fprintf(&b, " (retry after %s)", e.RetryAfter)
	}
	return b.String()
}

// Errorf returns an *Error with the code and a formatted message.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

func reasoned(code Code, reason Reason, format string, args ...any) *Error {
	e := Errorf(code, format, args...)
	e.Reason = reason
	return e
}

// ErrInvalidArgument says the request (or the settings) can't be used.
func ErrInvalidArgument(format string, args ...any) *Error {
	return Errorf(CodeInvalidArgument, format, args...)
}

// ErrNotFound says the source, series, chapter or page doesn't exist.
func ErrNotFound(format string, args ...any) *Error {
	return reasoned(CodeNotFound, ReasonNotFound, format, args...)
}

// ErrUnimplemented says the plugin does not support a call.
func ErrUnimplemented(method string) *Error {
	return reasoned(CodeUnimplemented, ReasonUnsupported, "%s is not supported by this plugin", method)
}

// ErrUnsupported says the source does not support a call, e.g. Latest on a
// source without the LATEST capability.
func ErrUnsupported(format string, args ...any) *Error {
	return reasoned(CodeUnimplemented, ReasonUnsupported, format, args...)
}

// ErrSourceDown says the upstream is unreachable, timed out or failing.
func ErrSourceDown(format string, args ...any) *Error {
	return reasoned(CodeUnavailable, ReasonSourceDown, format, args...)
}

// ErrChallenge says an anti-bot challenge blocked the request and no solver
// passed it.
func ErrChallenge(format string, args ...any) *Error {
	return reasoned(CodeUnavailable, ReasonChallenge, format, args...)
}

// ErrRateLimited says the upstream is rate-limiting the plugin.
func ErrRateLimited(retryAfter time.Duration, format string, args ...any) *Error {
	e := reasoned(CodeResourceExhausted, ReasonRateLimited, format, args...)
	e.RetryAfter = retryAfter
	return e
}

// ErrLoginRequired says the upstream wants a login the plugin doesn't have.
func ErrLoginRequired(format string, args ...any) *Error {
	return reasoned(CodeUnauthenticated, ReasonLoginRequired, format, args...)
}

// ErrStoreFetchFailed says a package's origin, or a file it lists, couldn't
// be downloaded.
func ErrStoreFetchFailed(format string, args ...any) *Error {
	return reasoned(CodeUnavailable, ReasonStoreFetchFailed, format, args...)
}

// ErrStoreInvalid says a package's origin, or a file it lists, can't be used:
// unreadable, or signed with a key that doesn't match.
func ErrStoreInvalid(format string, args ...any) *Error {
	return reasoned(CodeFailedPrecondition, ReasonStoreInvalid, format, args...)
}

// ErrAlreadyRunning says a job can't start because it is running already, as
// the run runID.
func ErrAlreadyRunning(runID, format string, args ...any) *Error {
	e := reasoned(CodeAlreadyExists, ReasonAlreadyRunning, format, args...)
	e.CurrentRunID = runID
	return e
}

// ErrJobDisabled says a job can't run now, for the reason given.
func ErrJobDisabled(jobID, reason string) *Error {
	e := reasoned(CodeFailedPrecondition, ReasonJobDisabled, "job %s can't run now: %s", jobID, reason)
	e.Metadata = map[string]string{"job_id": jobID}
	return e
}

// ErrorCode returns the code of err: the code of an *Error, CodeCanceled or
// CodeDeadlineExceeded for context errors, CodeInternal otherwise.
func ErrorCode(err error) Code {
	if err == nil {
		return 0
	}
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	if errors.Is(err, context.Canceled) {
		return connect.CodeCanceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return connect.CodeDeadlineExceeded
	}
	return CodeInternal
}

// ErrorReason returns the reason of err, or "" when it has none.
func ErrorReason(err error) Reason {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Reason
	}
	return ""
}

func asError(err error) *Error {
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	return &Error{Code: ErrorCode(err), Message: err.Error()}
}

// ToConnectError converts err for a Connect handler.
func ToConnectError(err error) error {
	if err == nil {
		return nil
	}
	pe := asError(err)
	ce := connect.NewError(pe.Code, errors.New(pe.Message))
	for _, m := range details(pe) {
		if d, derr := connect.NewErrorDetail(m); derr == nil {
			ce.AddDetail(d)
		}
	}
	return ce
}

// ToGRPCError converts err for a gRPC handler.
func ToGRPCError(err error) error {
	if err == nil {
		return nil
	}
	pe := asError(err)
	st := status.New(codes.Code(pe.Code), pe.Message)
	for _, m := range details(pe) {
		if withDetails, derr := st.WithDetails(protoadapt.MessageV1Of(m)); derr == nil {
			st = withDetails
		}
	}
	return st.Err()
}

// details are the status details that carry pe's reason, retry hint and
// running run.
func details(pe *Error) []proto.Message {
	var out []proto.Message
	if pe.Reason != "" {
		info := &errdetails.ErrorInfo{Reason: string(pe.Reason), Domain: ErrorDomain}
		for k, v := range pe.Metadata {
			if info.Metadata == nil {
				info.Metadata = map[string]string{}
			}
			info.Metadata[k] = v
		}
		if pe.CurrentRunID != "" {
			if info.Metadata == nil {
				info.Metadata = map[string]string{}
			}
			info.Metadata["current_run_id"] = pe.CurrentRunID
		}
		out = append(out, info)
	}
	if pe.RetryAfter > 0 {
		out = append(out, &errdetails.RetryInfo{RetryDelay: durationpb.New(pe.RetryAfter)})
	}
	if pe.CurrentRunID != "" {
		out = append(out, &jobsv1.JobAlreadyRunning{CurrentRunId: pe.CurrentRunID})
	}
	return out
}

func fromDetail(pe *Error, v any) {
	switch d := v.(type) {
	case *errdetails.ErrorInfo:
		pe.Reason = Reason(d.GetReason())
		if id := d.GetMetadata()["current_run_id"]; id != "" {
			pe.CurrentRunID = id
		}
		for k, v := range d.GetMetadata() {
			if k == "current_run_id" {
				continue
			}
			if pe.Metadata == nil {
				pe.Metadata = map[string]string{}
			}
			pe.Metadata[k] = v
		}
	case *jobsv1.JobAlreadyRunning:
		pe.CurrentRunID = d.GetCurrentRunId()
	case *errdetails.RetryInfo:
		pe.RetryAfter = d.GetRetryDelay().AsDuration()
	}
}

// FromConnectError converts an error returned by a Connect client into an
// *Error. Context errors are returned unchanged.
func FromConnectError(err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var ce *connect.Error
	if !errors.As(err, &ce) {
		return err
	}
	pe := &Error{Code: ce.Code(), Message: ce.Message()}
	for _, d := range ce.Details() {
		if v, derr := d.Value(); derr == nil {
			fromDetail(pe, v)
		}
	}
	return pe
}

// FromGRPCError converts an error returned by a gRPC client into an *Error.
func FromGRPCError(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	pe := &Error{Code: Code(st.Code()), Message: st.Message()}
	for _, d := range st.Details() {
		fromDetail(pe, d)
	}
	return pe
}
