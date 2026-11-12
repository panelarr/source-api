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
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/structpb"

	jobsv1 "github.com/panelarr/source-api/gen/panelarr/jobs/v1"
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

func TestPackagesConformance(t *testing.T) {
	f := plugintest.New()
	rep := conformance.Run(t, remote(t, f, "k"), conformance.Options{PackageID: "multi.comics"})
	if rep.Packages != 3 || len(rep.PackageSteps) != 7 {
		t.Fatalf("packages %d, steps %+v", rep.Packages, rep.PackageSteps)
	}
	for _, st := range rep.PackageSteps {
		if !st.OK {
			t.Errorf("step %s: %s", st.Name, st.Detail)
		}
	}
	// It was not installed before, so the walk removed it again.
	if f.Calls()["UninstallPackage"] != 2 {
		t.Fatalf("calls: %v", f.Calls())
	}
}

func TestPackageLanguagesDecideTheSources(t *testing.T) {
	f := plugintest.New()
	f.Langs = []string{"en", "de", "all"} // the plugin's own setting: no French
	c := remote(t, f, "")
	ctx := context.Background()
	res, err := c.InstallPackage(ctx, &sourcev1.InstallPackageRequest{PackageId: "multi.comics", OriginId: "store-a"})
	if err != nil || res.GetPackage().GetInstalledVersion() != "3.0" {
		t.Fatalf("install: %v %v", res, err)
	}
	listed := func() map[string]string {
		out := map[string]string{}
		l, _ := c.ListSources(ctx, &sourcev1.ListSourcesRequest{})
		for _, s := range l.GetSources() {
			out[s.GetId()] = s.GetPackageId()
		}
		return out
	}
	got := listed()
	if got["3001"] != "multi.comics" || got["3003"] != "multi.comics" || got["3002"] != "" {
		t.Fatalf("plugin languages: %v", got)
	}
	// The package's own languages narrow it further; "all" must be named to stay.
	if _, err := c.SetPackageSettings(ctx, &sourcev1.SetPackageSettingsRequest{PackageId: "multi.comics", Settings: &sourcev1.PackageSettings{EnabledLangs: []string{"en"}}}); err != nil {
		t.Fatal(err)
	}
	if got := listed(); got["3001"] == "" || got["3003"] != "" {
		t.Fatalf("package languages: %v", got)
	}
	if _, err := c.GetSource(ctx, &sourcev1.GetSourceRequest{SourceId: "3003"}); plugin.ErrorCode(err) != plugin.CodeNotFound {
		t.Fatalf("a source outside the languages: %v", err)
	}
	p, _ := c.GetPackage(ctx, &sourcev1.GetPackageRequest{PackageId: "multi.comics"})
	avail := map[string]bool{}
	for _, s := range p.GetPackage().GetSources() {
		avail[s.GetId()] = s.GetAvailable()
	}
	if !avail["3001"] || avail["3002"] || avail["3003"] || !p.GetPackage().GetUpdateAvailable() {
		t.Fatalf("package view: %v", p.GetPackage())
	}
	if _, err := c.SetPackageSettings(ctx, &sourcev1.SetPackageSettingsRequest{PackageId: "multi.comics", Settings: &sourcev1.PackageSettings{EnabledLangs: []string{"xx"}}}); plugin.ErrorCode(err) != plugin.CodeInvalidArgument {
		t.Fatalf("unknown language: %v", err)
	}
	// Update to the newest offer; again is OK.
	for range 2 {
		u, err := c.UpdatePackage(ctx, &sourcev1.UpdatePackageRequest{PackageId: "multi.comics"})
		if err != nil || u.GetPackage().GetInstalledVersion() != "3.1" || u.GetPackage().GetUpdateAvailable() {
			t.Fatalf("update: %v %v", u, err)
		}
	}
	// Without the capability, the package calls are unimplemented.
	plain := plugintest.New()
	plain.Packages = nil
	if _, err := remote(t, plain, "").ListPackages(ctx, &sourcev1.ListPackagesRequest{}); plugin.ErrorCode(err) != plugin.CodeUnimplemented {
		t.Fatalf("no packages: %v", err)
	}
}

func TestStoreErrorReasonsTravel(t *testing.T) {
	for name, back := range map[string]error{
		"grpc":    plugin.FromGRPCError(plugin.ToGRPCError(plugin.ErrStoreFetchFailed("GET x failed"))),
		"connect": plugin.FromConnectError(plugin.ToConnectError(plugin.ErrStoreInvalid("bad key"))),
	} {
		if r := plugin.ErrorReason(back); r != plugin.ReasonStoreFetchFailed && r != plugin.ReasonStoreInvalid {
			t.Errorf("%s: %v", name, back)
		}
	}
}

func TestInstanceID(t *testing.T) {
	ctx := context.Background()
	h, err := remote(t, plugintest.New(), "").Health(ctx)
	if err != nil || h.GetInstanceId() != plugin.InstanceID || len(plugin.InstanceID) != 16 {
		t.Fatalf("remote: %v %v", h, err)
	}
	// A plugin's own Health without an ID gets the process's.
	h, err = remote(t, ownHealth{plugintest.New()}, "").Health(ctx)
	if err != nil || h.GetInstanceId() != plugin.InstanceID || h.GetMessage() != "mine" {
		t.Fatalf("filled in: %v %v", h, err)
	}
	// A managed plugin is another process with another ID.
	if testing.Short() {
		return
	}
	c, err := client.Managed(buildExample(t), client.ManagedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	a, _ := c.Health(ctx)
	b, _ := c.Health(ctx)
	if a.GetInstanceId() == "" || a.GetInstanceId() == plugin.InstanceID || a.GetInstanceId() != b.GetInstanceId() {
		t.Fatalf("managed: %q %q (ours %q)", a.GetInstanceId(), b.GetInstanceId(), plugin.InstanceID)
	}
}

type ownHealth struct{ *plugintest.Fake }

func (ownHealth) Health(context.Context, *sourcev1.HealthRequest) (*sourcev1.HealthResponse, error) {
	return &sourcev1.HealthResponse{Status: sourcev1.HealthStatus_HEALTH_STATUS_SERVING, Message: "mine"}, nil
}

func TestGetNetworkProfile(t *testing.T) {
	c := remote(t, plugintest.New(), "")
	ctx := context.Background()
	got, err := c.GetNetworkProfile(ctx, "1001")
	if err != nil || got.GetOrigin() != sourcev1.NetworkProfileOrigin_NETWORK_PROFILE_ORIGIN_NONE || got.GetProfile() != nil {
		t.Fatalf("none: %v %v", got, err)
	}
	p := &sourcev1.NetworkProfile{Name: "vpn", Proxy: &sourcev1.Proxy{Url: "socks5://proxy.example:1080", Username: "u", Password: "secret"}}
	if err := c.SetNetworkProfile(ctx, "1001", p); err != nil {
		t.Fatal(err)
	}
	got, err = c.GetNetworkProfile(ctx, "1001")
	if err != nil || got.GetOrigin() != sourcev1.NetworkProfileOrigin_NETWORK_PROFILE_ORIGIN_PUSHED ||
		got.GetProfile().GetProxy().GetUsername() != "u" || got.GetProfile().GetProxy().GetPassword() != "" {
		t.Fatalf("pushed: %v %v", got, err)
	}
	if _, err := c.GetNetworkProfile(ctx, "nope"); plugin.ErrorCode(err) != plugin.CodeNotFound {
		t.Fatalf("unknown source: %v", err)
	}
}

func TestJobsOverEveryTransport(t *testing.T) {
	f := plugintest.New()
	clients := map[string]client.Client{
		"in-process": client.InProcess(f),
		"grpc":       remote(t, f, ""),
		"connect":    client.Remote(serve(t, f, ""), client.RemoteOptions{Connect: true}),
	}
	ctx := context.Background()
	for name, c := range clients {
		held, err := c.RunJob(ctx, &sourcev1.RunJobRequest{JobId: "hold", OnBehalfOf: "tester"})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		_, err = c.RunJob(ctx, &sourcev1.RunJobRequest{JobId: "hold"})
		var pe *plugin.Error
		if !errors.As(err, &pe) || pe.Code != plugin.CodeAlreadyExists || pe.Reason != plugin.ReasonAlreadyRunning || pe.CurrentRunID != held.GetRun().GetId() {
			t.Errorf("%s: second run: %#v", name, err)
		}
		if _, err := c.CancelJobRun(ctx, &sourcev1.CancelJobRunRequest{RunId: held.GetRun().GetId()}); err != nil {
			t.Errorf("%s: cancel: %v", name, err)
		}
		if _, err := f.Jobs.Wait(ctx, held.GetRun().GetId()); err != nil {
			t.Fatal(err)
		}
		run, err := c.GetJobRun(ctx, &sourcev1.GetJobRunRequest{RunId: held.GetRun().GetId()})
		if err != nil || run.GetRun().GetSummary().GetStatus() != jobsv1.JobRunStatus_JOB_RUN_STATUS_CANCELLED || run.GetRun().GetSummary().GetTriggeredBy() != "tester" {
			t.Errorf("%s: run: %v, %v", name, run, err)
		}
	}
}

func TestOriginsDetailsReloadAndHealthChecks(t *testing.T) {
	f := plugintest.New()
	clients := map[string]client.Client{
		"in-process": client.InProcess(f),
		"grpc":       remote(t, f, ""),
		"connect":    client.Remote(serve(t, f, ""), client.RemoteOptions{Connect: true}),
	}
	ctx := context.Background()
	for name, c := range clients {
		origins, err := c.ListOrigins(ctx, &sourcev1.ListOriginsRequest{})
		if err != nil || len(origins.GetOrigins()) != 2 || origins.GetOrigins()[0].GetPackageCount() != 3 {
			t.Fatalf("%s: origins %v, %v", name, origins, err)
		}
		f.BreakOrigin("store-b", true)
		_, err = c.RefreshOrigin(ctx, &sourcev1.RefreshOriginRequest{OriginId: "store-b"})
		if plugin.ErrorCode(err) != plugin.CodeUnavailable || plugin.ErrorReason(err) != plugin.ReasonStoreFetchFailed {
			t.Errorf("%s: broken refresh: %v", name, err)
		}
		f.BreakOrigin("store-b", false)
		if o, err := c.RefreshOrigin(ctx, &sourcev1.RefreshOriginRequest{OriginId: "store-b"}); err != nil || o.GetOrigin().GetError() != "" {
			t.Errorf("%s: refresh: %v, %v", name, o, err)
		}
		pkg, err := c.ReloadPackage(ctx, &sourcev1.ReloadPackageRequest{PackageId: "example.comics"})
		if err != nil || pkg.GetPackage().GetDetails().GetOriginId() != "store-a" || pkg.GetPackage().GetDetails().GetSigningFingerprint() == "" {
			t.Errorf("%s: reload: %v, %v", name, pkg, err)
		}
		if _, err := c.ReloadPackage(ctx, &sourcev1.ReloadPackageRequest{PackageId: "multi.comics"}); plugin.ErrorCode(err) != plugin.CodeFailedPrecondition {
			t.Errorf("%s: reload a package that isn't installed: %v", name, err)
		}
		off, err := c.SetSourceHealthCheck(ctx, &sourcev1.SetSourceHealthCheckRequest{SourceId: "1001"})
		if err != nil || !off.GetSource().GetHealth().GetChecksDisabled() {
			t.Errorf("%s: checks off: %v, %v", name, off, err)
		}
		on, err := c.SetSourceHealthCheck(ctx, &sourcev1.SetSourceHealthCheckRequest{SourceId: "1001", Enabled: true})
		if err != nil || on.GetSource().GetHealth().GetChecksDisabled() {
			t.Errorf("%s: checks on: %v, %v", name, on, err)
		}
	}
}

func TestCompatIDsOverEveryTransport(t *testing.T) {
	f := plugintest.New()
	clients := map[string]client.Client{
		"in-process": client.InProcess(f),
		"grpc":       remote(t, f, ""),
		"connect":    client.Remote(serve(t, f, ""), client.RemoteOptions{Connect: true}),
	}
	ctx := context.Background()
	want := []string{"example-app:1001"}
	for name, c := range clients {
		list, err := c.ListSources(ctx, &sourcev1.ListSourcesRequest{Lang: "en"})
		if err != nil || len(list.GetSources()) == 0 || !slices.Equal(list.GetSources()[0].GetCompatIds(), want) {
			t.Errorf("%s: ListSources: %v, %v", name, list, err)
		}
		src, err := c.GetSource(ctx, &sourcev1.GetSourceRequest{SourceId: "1001"})
		if err != nil || !slices.Equal(src.GetSource().GetCompatIds(), want) {
			t.Errorf("%s: GetSource: %v, %v", name, src, err)
		}
		pkg, err := c.GetPackage(ctx, &sourcev1.GetPackageRequest{PackageId: "example.comics"})
		if err != nil || len(pkg.GetPackage().GetSources()) != 1 || !slices.Equal(pkg.GetPackage().GetSources()[0].GetCompatIds(), want) {
			t.Errorf("%s: GetPackage: %v, %v", name, pkg, err)
		}
	}
}
