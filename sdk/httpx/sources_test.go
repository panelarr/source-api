// SPDX-License-Identifier: Apache-2.0

package httpx_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/httpx"
	"github.com/panelarr/source-api/sdk/plugin"
)

func TestSourcesFollowPushedAndDefaultProfiles(t *testing.T) {
	srv := echo()
	defer srv.Close()
	s := httpx.NewSources(httpx.Options{UserAgent: "plugin/1"})
	fixed := func(ua string) *sourcev1.NetworkProfile {
		return &sourcev1.NetworkProfile{Name: ua, UserAgent: &sourcev1.UserAgent{Mode: &sourcev1.UserAgent_Fixed{Fixed: ua}}}
	}
	if _, body, _ := get(t, s.Client("a"), srv.URL); !strings.Contains(body, "ua=plugin/1") {
		t.Fatalf("before any profile: %s", body)
	}
	if err := s.SetDefault(fixed("default-ua")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNetworkProfile(context.Background(), &sourcev1.SetNetworkProfileRequest{SourceId: "b", Profile: fixed("pushed-ua")}); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"a": "default-ua", "b": "pushed-ua", "c": "default-ua"} {
		if _, body, _ := get(t, s.Client(id), srv.URL); !strings.Contains(body, "ua="+want) {
			t.Errorf("source %s: %s", id, body)
		}
	}
	// Removing the pushed profile brings the default back.
	if _, err := s.SetNetworkProfile(context.Background(), &sourcev1.SetNetworkProfileRequest{SourceId: "b"}); err != nil {
		t.Fatal(err)
	}
	if _, body, _ := get(t, s.Client("b"), srv.URL); !strings.Contains(body, "ua=default-ua") {
		t.Fatalf("after removing: %s", body)
	}
	// A bad profile is refused as an invalid argument.
	_, err := s.SetNetworkProfile(context.Background(), &sourcev1.SetNetworkProfileRequest{SourceId: "b",
		Profile: &sourcev1.NetworkProfile{Proxy: &sourcev1.Proxy{Url: "ftp://nope"}}})
	if plugin.ErrorCode(err) != plugin.CodeInvalidArgument {
		t.Fatalf("bad profile: %v", err)
	}
}

func TestSharedCookieJar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/set" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/"})
			return
		}
		if c, err := r.Cookie("session"); err == nil {
			_, _ = w.Write([]byte(c.Value))
		}
	}))
	defer srv.Close()
	s := httpx.NewSources(httpx.Options{})
	shared := &sourcev1.NetworkProfile{SharedCookieJar: "site"}
	_ = s.SetProfile("a", shared)
	_ = s.SetProfile("b", shared)
	_, _, _ = get(t, s.Client("a"), srv.URL+"/set")
	if _, body, _ := get(t, s.Client("b"), srv.URL+"/get"); body != "abc" {
		t.Fatalf("shared jar: %q", body)
	}
	if _, body, _ := get(t, s.Client("c"), srv.URL+"/get"); body != "" {
		t.Fatalf("own jar leaked: %q", body)
	}
}

func TestRateLimitPerHost(t *testing.T) {
	a, b := echo(), echo()
	defer a.Close()
	defer b.Close()
	c := httpx.New(httpx.Options{})
	_ = c.SetProfile(&sourcev1.NetworkProfile{RateLimit: &sourcev1.RateLimit{
		Requests: 1, Period: durationpb.New(400 * time.Millisecond), Scope: sourcev1.RateLimitScope_RATE_LIMIT_SCOPE_HOST,
	}})
	// One request to each host fits in one period: they have separate budgets.
	start := time.Now()
	_, _, _ = get(t, c, a.URL)
	_, _, _ = get(t, c, strings.Replace(b.URL, "127.0.0.1", "localhost", 1))
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Fatalf("per-host budgets were shared: %s", d)
	}
	_, _, _ = get(t, c, a.URL)
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Fatalf("second request to the same host wasn't limited: %s", d)
	}
}

func TestStatusErrorAndWrap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/429":
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
		case "/404":
			w.WriteHeader(http.StatusNotFound)
		case "/403":
			w.WriteHeader(http.StatusForbidden)
		case "/500":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()
	c := httpx.New(httpx.Options{})
	want := map[string]plugin.Reason{"/429": plugin.ReasonRateLimited, "/404": plugin.ReasonNotFound,
		"/403": plugin.ReasonLoginRequired, "/500": plugin.ReasonSourceDown, "/ok": ""}
	for path, reason := range want {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
		res, err := c.HTTP().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		err = httpx.StatusError(res)
		if plugin.ErrorReason(err) != reason {
			t.Errorf("%s: %v", path, err)
		}
		if path == "/429" {
			var pe *plugin.Error
			if !errors.As(err, &pe) || pe.RetryAfter != 7*time.Second {
				t.Errorf("retry after: %v", err)
			}
		}
	}
	if got := plugin.ErrorReason(httpx.Wrap(&httpx.ChallengeError{Host: "h", Reason: "r"})); got != plugin.ReasonChallenge {
		t.Errorf("challenge: %s", got)
	}
	_, _, err := get(t, c, "http://127.0.0.1:1/")
	if plugin.ErrorReason(httpx.Wrap(err)) != plugin.ReasonSourceDown {
		t.Errorf("unreachable: %v", httpx.Wrap(err))
	}
	if !errors.Is(httpx.Wrap(context.Canceled), context.Canceled) {
		t.Error("cancellation must pass through")
	}
}
