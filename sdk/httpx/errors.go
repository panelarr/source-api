// SPDX-License-Identifier: Apache-2.0

package httpx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/panelarr/source-api/sdk/plugin"
)

func (o Options) logger() *slog.Logger {
	if o.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return o.Log
}

// StatusError turns an unsuccessful response into the error the source API
// expects: 429 → RATE_LIMITED (with Retry-After), 404 → NOT_FOUND, 401 and
// 403 → LOGIN_REQUIRED, others → SOURCE_DOWN. A 2xx or 3xx response gives
// nil. The body is left to the caller.
func StatusError(resp *http.Response) error {
	code := resp.StatusCode
	host := resp.Request.URL.Hostname()
	switch {
	case code < 400:
		return nil
	case code == http.StatusTooManyRequests:
		return plugin.ErrRateLimited(retryAfter(resp.Header.Get("Retry-After")), "%s rate-limited the request", host)
	case code == http.StatusNotFound || code == http.StatusGone:
		return plugin.ErrNotFound("%s answered HTTP %d", host, code)
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return plugin.ErrLoginRequired("%s answered HTTP %d", host, code)
	default:
		return plugin.ErrSourceDown("%s answered HTTP %d", host, code)
	}
}

// retryAfter reads a Retry-After header: seconds or an HTTP date.
func retryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0)
	}
	return 0
}

// Wrap turns an error from a request made with a [Client] into the error
// the source API expects: a challenge that couldn't be passed →
// CLOUDFLARE_CHALLENGE, network failures and timeouts → SOURCE_DOWN. Plugin
// errors and context cancellation pass through.
func Wrap(err error) error {
	if err == nil {
		return nil
	}
	var pe *plugin.Error
	if errors.As(err, &pe) || errors.Is(err, context.Canceled) {
		return err
	}
	var ce *ChallengeError
	if errors.As(err, &ce) {
		return plugin.ErrChallenge("%s", ce.Error())
	}
	var ne net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &ne) {
		return plugin.ErrSourceDown("%v", err)
	}
	return plugin.ErrSourceDown("%s", fmt.Sprint(err))
}
