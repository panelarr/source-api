// SPDX-License-Identifier: Apache-2.0

package client_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/client"
	"github.com/panelarr/source-api/sdk/conformance"
	"github.com/panelarr/source-api/sdk/plugin"
	"github.com/panelarr/source-api/sdk/plugintest"
	"github.com/panelarr/source-api/sdk/settings"
)

// serve runs p like a standalone plugin: HTTP/1.1 and cleartext HTTP/2.
func serve(t *testing.T, p plugin.Plugin, key string) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(plugin.NewHandler(p, key))
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)
	return srv.URL
}

// remote connects over gRPC, the canonical protocol.
func remote(t *testing.T, p plugin.Plugin, key string) client.Client {
	t.Helper()
	c := client.Remote(serve(t, p, key), client.RemoteOptions{APIKey: key})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// buildExample builds the example plugin binary for managed-mode tests.
func buildExample(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds a binary")
	}
	bin := filepath.Join(t.TempDir(), "static-source")
	cmd := exec.Command("go", "build", "-o", bin, "../examples/static-source")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build example plugin: %v\n%s", err, out)
	}
	return bin
}

func checkReport(t *testing.T, rep *conformance.Report) {
	t.Helper()
	if len(rep.Sources) != 2 {
		t.Fatalf("walked %d sources", len(rep.Sources))
	}
	for _, s := range rep.Sources {
		if !s.OK() {
			t.Errorf("source %s failed: %+v", s.ID, s.Steps)
		}
	}
	if len(rep.Warnings) > 0 {
		t.Errorf("warnings: %v", rep.Warnings)
	}
}

func TestConformanceInProcess(t *testing.T) {
	checkReport(t, conformance.Run(t, client.InProcess(plugintest.New()), conformance.Options{}))
}

func TestConformanceRemote(t *testing.T) {
	checkReport(t, conformance.Run(t, remote(t, plugintest.New(), "secret"), conformance.Options{}))
}

func TestConformanceRemoteConnectProtocol(t *testing.T) {
	c := client.Remote(serve(t, plugintest.New(), ""), client.RemoteOptions{Connect: true})
	checkReport(t, conformance.Run(t, c, conformance.Options{}))
}

func TestConformanceRemoteGRPCOverTLS(t *testing.T) {
	srv := httptest.NewUnstartedServer(plugin.NewHandler(plugintest.New(), ""))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	c := client.Remote(srv.URL, client.RemoteOptions{HTTPClient: srv.Client()})
	checkReport(t, conformance.Run(t, c, conformance.Options{}))
}

// A host with its own grpc-go server and connection, e.g. a non-Go plugin.
func TestConformanceOverGRPCGo(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	plugin.RegisterGRPC(s, plugintest.New())
	go func() { _ = s.Serve(ln) }()
	t.Cleanup(s.Stop)
	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	checkReport(t, conformance.Run(t, client.GRPC(conn), conformance.Options{}))
}

func TestConformanceManaged(t *testing.T) {
	bin := buildExample(t)
	c, err := client.Managed(bin, client.ManagedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	rep := conformance.Run(t, c, conformance.Options{Config: map[string]any{"greeting": "hi", "token": "s3cret"}})
	checkReport(t, rep)
	if rep.Info.GetId() != "static-source" {
		t.Fatalf("unexpected id %q", rep.Info.GetId())
	}
}

func TestStandaloneBinary(t *testing.T) {
	bin := buildExample(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Pick a free port up front.
	ln := httptest.NewServer(nil)
	addr := ln.Listener.Addr().String()
	ln.Close()
	cmd := exec.CommandContext(ctx, bin, "--listen", addr, "--api-key", "k")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	c := client.Remote("http://"+addr, client.RemoteOptions{APIKey: "k"})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := c.GetInfo(ctx); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the standalone plugin didn't come up")
		}
		time.Sleep(50 * time.Millisecond)
	}
	checkReport(t, conformance.Run(t, c, conformance.Options{}))

	// The key is required, except for Health.
	wrong := client.Remote("http://"+addr, client.RemoteOptions{APIKey: "nope"})
	if _, err := wrong.ListSources(ctx, &sourcev1.ListSourcesRequest{}); plugin.ErrorCode(err) != plugin.CodeUnauthenticated {
		t.Fatalf("wrong key: %v", err)
	}
	if h, err := wrong.Health(ctx); err != nil || h.GetStatus() != sourcev1.HealthStatus_HEALTH_STATUS_SERVING {
		t.Fatalf("health without a key: %v %v", h, err)
	}
	res, err := http.Get("http://" + addr + "/healthz") //nolint:noctx // test
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("healthz: %v %v", res, err)
	}
	_ = res.Body.Close()
}

// rateLimited fails every Popular call with a reason and a retry hint.
type rateLimited struct{ *plugintest.Fake }

func (rateLimited) Popular(context.Context, *sourcev1.PopularRequest) (*sourcev1.PopularResponse, error) {
	return nil, plugin.ErrRateLimited(42*time.Second, "slow down")
}

func TestErrorsKeepCodeReasonAndRetryHint(t *testing.T) {
	p := rateLimited{plugintest.New()}
	clients := map[string]client.Client{
		"in-process": client.InProcess(p),
		"grpc":       remote(t, p, ""),
		"connect":    client.Remote(serve(t, p, ""), client.RemoteOptions{Connect: true}),
	}
	for name, c := range clients {
		_, err := c.Popular(context.Background(), &sourcev1.PopularRequest{SourceId: "1001", Page: 1})
		var pe *plugin.Error
		if !errors.As(err, &pe) || pe.Code != plugin.CodeResourceExhausted || pe.Reason != plugin.ReasonRateLimited || pe.RetryAfter != 42*time.Second {
			t.Errorf("%s: %#v", name, err)
		}
		_, err = c.GetSource(context.Background(), &sourcev1.GetSourceRequest{SourceId: "nope"})
		if plugin.ErrorCode(err) != plugin.CodeNotFound || plugin.ErrorReason(err) != plugin.ReasonNotFound {
			t.Errorf("%s: not found: %v", name, err)
		}
	}
}

func TestImages(t *testing.T) {
	c := remote(t, plugintest.New(), "")
	ct, data, err := client.ReadImage(context.Background(), c, &sourcev1.FetchImageRequest{SourceId: "1001", Ref: "page:/series/1/3/0"}, 0)
	if err != nil || ct != "image/png" || !bytes.Equal(data, plugintest.Image("page:/series/1/3/0")) {
		t.Fatalf("image: %s %d %v", ct, len(data), err)
	}
	if _, _, err := client.ReadImage(context.Background(), c, &sourcev1.FetchImageRequest{SourceId: "1001", Ref: "page:/series/1/3/0"}, 10); !errors.Is(err, client.ErrImageTooLarge) {
		t.Fatalf("limit: %v", err)
	}
	if _, _, err := client.ReadImage(context.Background(), c, &sourcev1.FetchImageRequest{SourceId: "1001", Ref: "page:/nope/1"}, 0); plugin.ErrorCode(err) != plugin.CodeNotFound {
		t.Fatalf("missing image: %v", err)
	}
}

func TestConfig(t *testing.T) {
	schema, err := settings.Parse(`{"type": "object", "properties": {
	  "url": {"type": "string", "format": "uri", "default": "http://default.example"},
	  "key": {"type": "string", "writeOnly": true}}, "required": ["key"], "additionalProperties": false}`)
	if err != nil {
		t.Fatal(err)
	}
	var applied map[string]any
	f := plugintest.New()
	f.Config = plugin.NewPluginConfig(schema, func(_ context.Context, v map[string]any) error {
		applied = v
		return nil
	})
	c := remote(t, f, "")
	ctx := context.Background()
	set := func(v map[string]any) (*structpb.Struct, error) {
		st, _ := structpb.NewStruct(v)
		return c.SetConfig(ctx, st)
	}
	got, err := set(map[string]any{"key": "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if got.GetFields()["key"].GetStringValue() != "" || got.GetFields()["url"].GetStringValue() != "http://default.example" {
		t.Fatalf("returned: %v", got)
	}
	if applied["key"] != "s3cret" {
		t.Fatalf("applied: %v", applied)
	}
	// Sending back what GetConfig returned keeps the secret.
	read, _ := c.GetConfig(ctx)
	read.Fields["url"] = structpb.NewStringValue("http://other.example")
	if _, err := c.SetConfig(ctx, read); err != nil {
		t.Fatal(err)
	}
	if applied["key"] != "s3cret" || applied["url"] != "http://other.example" {
		t.Fatalf("round trip: %v", applied)
	}
	if _, err := set(map[string]any{"key": "x", "extra": true}); plugin.ErrorCode(err) != plugin.CodeInvalidArgument {
		t.Fatalf("invalid config: %v", err)
	}
	// Without a config schema the calls are unimplemented.
	if _, err := remote(t, plugintest.New(), "").GetConfig(ctx); plugin.ErrorCode(err) != plugin.CodeUnimplemented {
		t.Fatalf("no config: %v", err)
	}
}

func TestNetworkProfileReachesThePlugin(t *testing.T) {
	f := plugintest.New()
	c := remote(t, f, "")
	p := &sourcev1.NetworkProfile{Name: "vpn", Proxy: &sourcev1.Proxy{Url: "http://proxy.example:8888"}}
	if err := c.SetNetworkProfile(context.Background(), "1001", p); err != nil {
		t.Fatal(err)
	}
	if f.Profile("1001").GetName() != "vpn" {
		t.Fatalf("profile: %v", f.Profile("1001"))
	}
	if err := c.SetNetworkProfile(context.Background(), "1001", nil); err != nil || f.Profile("1001") != nil {
		t.Fatalf("remove: %v %v", err, f.Profile("1001"))
	}
}

func TestExtraRoutes(t *testing.T) {
	routes := []plugin.Route{
		{Pattern: "GET /metrics", Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("m 1")) })},
		{Pattern: "GET /public", Public: true, Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("hi")) })},
	}
	srv := httptest.NewServer(plugin.NewHandler(plugintest.New(), "k", routes...))
	defer srv.Close()
	status := func(path, key string) int {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+path, nil)
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if status("/metrics", "") != http.StatusUnauthorized || status("/metrics", "k") != http.StatusOK || status("/public", "") != http.StatusOK {
		t.Fatal("route auth")
	}
}
