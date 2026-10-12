// SPDX-License-Identifier: Apache-2.0

// Package client connects a host (the Panelarr server, or any program) to
// source plugins: managed binaries it starts itself, remote plugins it
// reaches by URL, and in-process plugins for tests. All three give the same
// [Client].
package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"sync"

	"connectrpc.com/connect"
	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/gen/panelarr/source/v1/sourcev1connect"
	"github.com/panelarr/source-api/sdk/plugin"
)

// Client talks to one source plugin. Errors from the plugin come back as
// *plugin.Error (with the code, the error reason and any retry hint) where
// the plugin gave a status.
type Client interface {
	GetInfo(ctx context.Context) (*sourcev1.PluginInfo, error)
	// GetConfig returns the plugin-level configuration, secrets left empty.
	GetConfig(ctx context.Context) (*structpb.Struct, error)
	// SetConfig replaces the plugin-level configuration and returns it as
	// GetConfig does.
	SetConfig(ctx context.Context, values *structpb.Struct) (*structpb.Struct, error)
	Health(ctx context.Context) (*sourcev1.HealthResponse, error)
	ListSources(ctx context.Context, req *sourcev1.ListSourcesRequest) (*sourcev1.ListSourcesResponse, error)
	GetSource(ctx context.Context, req *sourcev1.GetSourceRequest) (*sourcev1.GetSourceResponse, error)
	GetFilters(ctx context.Context, req *sourcev1.GetFiltersRequest) (*sourcev1.GetFiltersResponse, error)
	Popular(ctx context.Context, req *sourcev1.PopularRequest) (*sourcev1.PopularResponse, error)
	Latest(ctx context.Context, req *sourcev1.LatestRequest) (*sourcev1.LatestResponse, error)
	Search(ctx context.Context, req *sourcev1.SearchRequest) (*sourcev1.SearchResponse, error)
	ResolveUrl(ctx context.Context, req *sourcev1.ResolveUrlRequest) (*sourcev1.ResolveUrlResponse, error)
	GetSeries(ctx context.Context, req *sourcev1.GetSeriesRequest) (*sourcev1.GetSeriesResponse, error)
	ListChapters(ctx context.Context, req *sourcev1.ListChaptersRequest) (*sourcev1.ListChaptersResponse, error)
	ListPages(ctx context.Context, req *sourcev1.ListPagesRequest) (*sourcev1.ListPagesResponse, error)
	GetSettingsSchema(ctx context.Context, req *sourcev1.GetSettingsSchemaRequest) (*sourcev1.GetSettingsSchemaResponse, error)
	GetSettings(ctx context.Context, req *sourcev1.GetSettingsRequest) (*sourcev1.GetSettingsResponse, error)
	SetSettings(ctx context.Context, req *sourcev1.SetSettingsRequest) (*sourcev1.SetSettingsResponse, error)
	// The package calls (plugins with the PACKAGES capability). InstallPackage
	// and UpdatePackage can take minutes: give them a long deadline.
	ListPackages(ctx context.Context, req *sourcev1.ListPackagesRequest) (*sourcev1.ListPackagesResponse, error)
	GetPackage(ctx context.Context, req *sourcev1.GetPackageRequest) (*sourcev1.GetPackageResponse, error)
	InstallPackage(ctx context.Context, req *sourcev1.InstallPackageRequest) (*sourcev1.InstallPackageResponse, error)
	UpdatePackage(ctx context.Context, req *sourcev1.UpdatePackageRequest) (*sourcev1.UpdatePackageResponse, error)
	UninstallPackage(ctx context.Context, req *sourcev1.UninstallPackageRequest) (*sourcev1.UninstallPackageResponse, error)
	SetPackageSettings(ctx context.Context, req *sourcev1.SetPackageSettingsRequest) (*sourcev1.SetPackageSettingsResponse, error)
	// FetchImage starts streaming an image. Read it with Recv, or use
	// [ReadImage].
	FetchImage(ctx context.Context, req *sourcev1.FetchImageRequest) (ImageStream, error)
	// GetNetworkProfile returns the profile a source uses and its origin.
	GetNetworkProfile(ctx context.Context, sourceID string) (*sourcev1.GetNetworkProfileResponse, error)
	// SetNetworkProfile hands the plugin a source's network profile; nil
	// removes the pushed profile.
	SetNetworkProfile(ctx context.Context, sourceID string, profile *sourcev1.NetworkProfile) error
	// Close ends the connection and, for managed plugins, the process.
	Close() error
}

// ImageStream reads the messages of FetchImage: the metadata first, then
// the bytes.
type ImageStream interface {
	// Recv returns the next message, or io.EOF after the last one.
	Recv() (*sourcev1.FetchImageResponse, error)
	// Close stops reading and releases the stream.
	Close() error
}

// ErrImageTooLarge is returned by [ReadImage] when an image is bigger than
// its limit.
var ErrImageTooLarge = errors.New("client: image is larger than the limit")

// ReadImage fetches a whole image: its content type and bytes, at most max
// bytes (0 means no limit).
func ReadImage(ctx context.Context, c Client, req *sourcev1.FetchImageRequest, maxBytes int64) (string, []byte, error) {
	s, err := c.FetchImage(ctx, req)
	if err != nil {
		return "", nil, err
	}
	defer s.Close()
	var contentType string
	var data []byte
	first := true
	for {
		msg, err := s.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", nil, err
		}
		if first {
			first = false
			md := msg.GetMetadata()
			if md == nil {
				return "", nil, plugin.Errorf(plugin.CodeInternal, "FetchImage: the first message has no metadata")
			}
			contentType = md.GetContentType()
			continue
		}
		data = append(data, msg.GetChunk()...)
		if maxBytes > 0 && int64(len(data)) > maxBytes {
			return "", nil, ErrImageTooLarge
		}
	}
	if first {
		return "", nil, plugin.Errorf(plugin.CodeInternal, "FetchImage: the stream was empty")
	}
	return contentType, data, nil
}

// RemoteOptions configure [Remote].
type RemoteOptions struct {
	// APIKey is sent as "Authorization: Bearer <key>".
	APIKey string
	// HTTPClient overrides the HTTP client. The default speaks HTTP/2,
	// cleartext for http:// URLs and TLS for https://, as gRPC needs.
	HTTPClient *http.Client
	// Connect uses the Connect protocol instead of gRPC. gRPC is the
	// protocol every plugin must speak; Connect is an optional extra that
	// also works over HTTP/1.1.
	Connect bool
}

// Remote returns a client for a plugin running as a service at baseURL,
// e.g. "http://source-plugin:8904". It speaks gRPC unless opts.Connect is
// set, so it works with any gRPC plugin, in any language.
func Remote(baseURL string, opts RemoteOptions) Client {
	hc := opts.HTTPClient
	if hc == nil {
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Protocols = new(http.Protocols)
		if opts.Connect {
			tr.Protocols.SetHTTP1(true)
		} else {
			tr.Protocols.SetHTTP2(true)
			tr.Protocols.SetUnencryptedHTTP2(true)
		}
		hc = &http.Client{Transport: tr}
	}
	copts := []connect.ClientOption{
		connect.WithReadMaxBytes(plugin.MaxMessageSize),
		connect.WithSendMaxBytes(plugin.MaxMessageSize),
	}
	if opts.APIKey != "" {
		copts = append(copts, connect.WithInterceptors(bearer(opts.APIKey)))
	}
	if !opts.Connect {
		copts = append(copts, connect.WithGRPC())
	}
	return &remoteClient{c: sourcev1connect.NewSourcePluginServiceClient(hc, baseURL, copts...)}
}

type bearer string

func (b bearer) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		req.Header().Set("Authorization", "Bearer "+string(b))
		return next(ctx, req)
	}
}

func (b bearer) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		conn.RequestHeader().Set("Authorization", "Bearer "+string(b))
		return conn
	}
}

func (b bearer) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}

type remoteClient struct {
	c sourcev1connect.SourcePluginServiceClient
}

func callConnect[Req, Res any](ctx context.Context, req *Req, f func(context.Context, *connect.Request[Req]) (*connect.Response[Res], error)) (*Res, error) {
	res, err := f(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, plugin.FromConnectError(err)
	}
	return res.Msg, nil
}

func (r *remoteClient) GetInfo(ctx context.Context) (*sourcev1.PluginInfo, error) {
	res, err := callConnect(ctx, &sourcev1.GetInfoRequest{}, r.c.GetInfo)
	return res.GetInfo(), err
}

func (r *remoteClient) GetConfig(ctx context.Context) (*structpb.Struct, error) {
	res, err := callConnect(ctx, &sourcev1.GetConfigRequest{}, r.c.GetConfig)
	return res.GetValues(), err
}

func (r *remoteClient) SetConfig(ctx context.Context, values *structpb.Struct) (*structpb.Struct, error) {
	res, err := callConnect(ctx, &sourcev1.SetConfigRequest{Values: values}, r.c.SetConfig)
	return res.GetValues(), err
}

func (r *remoteClient) Health(ctx context.Context) (*sourcev1.HealthResponse, error) {
	return callConnect(ctx, &sourcev1.HealthRequest{}, r.c.Health)
}

func (r *remoteClient) ListSources(ctx context.Context, req *sourcev1.ListSourcesRequest) (*sourcev1.ListSourcesResponse, error) {
	return callConnect(ctx, req, r.c.ListSources)
}

func (r *remoteClient) GetSource(ctx context.Context, req *sourcev1.GetSourceRequest) (*sourcev1.GetSourceResponse, error) {
	return callConnect(ctx, req, r.c.GetSource)
}

func (r *remoteClient) GetFilters(ctx context.Context, req *sourcev1.GetFiltersRequest) (*sourcev1.GetFiltersResponse, error) {
	return callConnect(ctx, req, r.c.GetFilters)
}

func (r *remoteClient) Popular(ctx context.Context, req *sourcev1.PopularRequest) (*sourcev1.PopularResponse, error) {
	return callConnect(ctx, req, r.c.Popular)
}

func (r *remoteClient) Latest(ctx context.Context, req *sourcev1.LatestRequest) (*sourcev1.LatestResponse, error) {
	return callConnect(ctx, req, r.c.Latest)
}

func (r *remoteClient) Search(ctx context.Context, req *sourcev1.SearchRequest) (*sourcev1.SearchResponse, error) {
	return callConnect(ctx, req, r.c.Search)
}

func (r *remoteClient) ResolveUrl(ctx context.Context, req *sourcev1.ResolveUrlRequest) (*sourcev1.ResolveUrlResponse, error) {
	return callConnect(ctx, req, r.c.ResolveUrl)
}

func (r *remoteClient) GetSeries(ctx context.Context, req *sourcev1.GetSeriesRequest) (*sourcev1.GetSeriesResponse, error) {
	return callConnect(ctx, req, r.c.GetSeries)
}

func (r *remoteClient) ListChapters(ctx context.Context, req *sourcev1.ListChaptersRequest) (*sourcev1.ListChaptersResponse, error) {
	return callConnect(ctx, req, r.c.ListChapters)
}

func (r *remoteClient) ListPages(ctx context.Context, req *sourcev1.ListPagesRequest) (*sourcev1.ListPagesResponse, error) {
	return callConnect(ctx, req, r.c.ListPages)
}

func (r *remoteClient) GetSettingsSchema(ctx context.Context, req *sourcev1.GetSettingsSchemaRequest) (*sourcev1.GetSettingsSchemaResponse, error) {
	return callConnect(ctx, req, r.c.GetSettingsSchema)
}

func (r *remoteClient) GetSettings(ctx context.Context, req *sourcev1.GetSettingsRequest) (*sourcev1.GetSettingsResponse, error) {
	return callConnect(ctx, req, r.c.GetSettings)
}

func (r *remoteClient) SetSettings(ctx context.Context, req *sourcev1.SetSettingsRequest) (*sourcev1.SetSettingsResponse, error) {
	return callConnect(ctx, req, r.c.SetSettings)
}

func (r *remoteClient) ListPackages(ctx context.Context, req *sourcev1.ListPackagesRequest) (*sourcev1.ListPackagesResponse, error) {
	return callConnect(ctx, req, r.c.ListPackages)
}

func (r *remoteClient) GetPackage(ctx context.Context, req *sourcev1.GetPackageRequest) (*sourcev1.GetPackageResponse, error) {
	return callConnect(ctx, req, r.c.GetPackage)
}

func (r *remoteClient) InstallPackage(ctx context.Context, req *sourcev1.InstallPackageRequest) (*sourcev1.InstallPackageResponse, error) {
	return callConnect(ctx, req, r.c.InstallPackage)
}

func (r *remoteClient) UpdatePackage(ctx context.Context, req *sourcev1.UpdatePackageRequest) (*sourcev1.UpdatePackageResponse, error) {
	return callConnect(ctx, req, r.c.UpdatePackage)
}

func (r *remoteClient) UninstallPackage(ctx context.Context, req *sourcev1.UninstallPackageRequest) (*sourcev1.UninstallPackageResponse, error) {
	return callConnect(ctx, req, r.c.UninstallPackage)
}

func (r *remoteClient) SetPackageSettings(ctx context.Context, req *sourcev1.SetPackageSettingsRequest) (*sourcev1.SetPackageSettingsResponse, error) {
	return callConnect(ctx, req, r.c.SetPackageSettings)
}

func (r *remoteClient) FetchImage(ctx context.Context, req *sourcev1.FetchImageRequest) (ImageStream, error) {
	s, err := r.c.FetchImage(ctx, connect.NewRequest(req))
	if err != nil {
		return nil, plugin.FromConnectError(err)
	}
	return &connectStream{s: s}, nil
}

func (r *remoteClient) GetNetworkProfile(ctx context.Context, sourceID string) (*sourcev1.GetNetworkProfileResponse, error) {
	return callConnect(ctx, &sourcev1.GetNetworkProfileRequest{SourceId: sourceID}, r.c.GetNetworkProfile)
}

func (r *remoteClient) SetNetworkProfile(ctx context.Context, sourceID string, p *sourcev1.NetworkProfile) error {
	_, err := callConnect(ctx, &sourcev1.SetNetworkProfileRequest{SourceId: sourceID, Profile: p}, r.c.SetNetworkProfile)
	return err
}

func (r *remoteClient) Close() error { return nil }

type connectStream struct {
	s *connect.ServerStreamForClient[sourcev1.FetchImageResponse]
}

func (c *connectStream) Recv() (*sourcev1.FetchImageResponse, error) {
	if c.s.Receive() {
		return c.s.Msg(), nil
	}
	if err := c.s.Err(); err != nil {
		return nil, plugin.FromConnectError(err)
	}
	return nil, io.EOF
}

func (c *connectStream) Close() error { return c.s.Close() }

// ManagedOptions configure [Managed].
type ManagedOptions struct {
	// Args are passed to the plugin binary.
	Args []string
	// Env is added to the plugin's environment.
	Env []string
	// Logger receives the plugin's log output. Defaults to no output.
	Logger hclog.Logger
}

// Managed starts the plugin binary at path as a child process (go-plugin
// handshake, gRPC on a local socket) and returns a client for it. Close
// stops the process.
func Managed(path string, opts ManagedOptions) (Client, error) {
	// go-plugin owns the process lifetime (Close kills it), so no context here.
	cmd := exec.Command(path, opts.Args...) //nolint:gosec,noctx // the admin chose which plugin binary to run
	cmd.Env = append(cmd.Environ(), opts.Env...)
	logger := opts.Logger
	if logger == nil {
		logger = hclog.NewNullLogger()
	}
	pc := goplugin.NewClient(&goplugin.ClientConfig{
		HandshakeConfig:  plugin.Handshake,
		Plugins:          plugin.PluginSet(nil),
		Cmd:              cmd,
		AllowedProtocols: []goplugin.Protocol{goplugin.ProtocolGRPC},
		Logger:           logger,
		GRPCDialOptions: []grpc.DialOption{grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(plugin.MaxMessageSize),
			grpc.MaxCallSendMsgSize(plugin.MaxMessageSize),
		)},
	})
	rpc, err := pc.Client()
	if err != nil {
		pc.Kill()
		return nil, err
	}
	raw, err := rpc.Dispense(plugin.PluginName)
	if err != nil {
		pc.Kill()
		return nil, err
	}
	svc, ok := raw.(sourcev1.SourcePluginServiceClient)
	if !ok {
		pc.Kill()
		return nil, errors.New("client: plugin did not return a source plugin client")
	}
	return &grpcClient{c: svc, kill: pc.Kill, exited: pc.Exited}, nil
}

// GRPC returns a client over an existing gRPC connection, e.g. one a host
// dialled itself.
func GRPC(conn grpc.ClientConnInterface) Client {
	return &grpcClient{c: sourcev1.NewSourcePluginServiceClient(conn)}
}

type grpcClient struct {
	c      sourcev1.SourcePluginServiceClient
	kill   func()
	exited func() bool
	once   sync.Once
}

func callGRPC[Req, Res any](ctx context.Context, req *Req, f func(context.Context, *Req, ...grpc.CallOption) (*Res, error)) (*Res, error) {
	res, err := f(ctx, req)
	if err != nil {
		return nil, plugin.FromGRPCError(err)
	}
	return res, nil
}

func (g *grpcClient) GetInfo(ctx context.Context) (*sourcev1.PluginInfo, error) {
	res, err := callGRPC(ctx, &sourcev1.GetInfoRequest{}, g.c.GetInfo)
	return res.GetInfo(), err
}

func (g *grpcClient) GetConfig(ctx context.Context) (*structpb.Struct, error) {
	res, err := callGRPC(ctx, &sourcev1.GetConfigRequest{}, g.c.GetConfig)
	return res.GetValues(), err
}

func (g *grpcClient) SetConfig(ctx context.Context, values *structpb.Struct) (*structpb.Struct, error) {
	res, err := callGRPC(ctx, &sourcev1.SetConfigRequest{Values: values}, g.c.SetConfig)
	return res.GetValues(), err
}

func (g *grpcClient) Health(ctx context.Context) (*sourcev1.HealthResponse, error) {
	if g.exited != nil && g.exited() {
		return &sourcev1.HealthResponse{Status: sourcev1.HealthStatus_HEALTH_STATUS_NOT_SERVING, Message: "plugin process exited"}, nil
	}
	return callGRPC(ctx, &sourcev1.HealthRequest{}, g.c.Health)
}

func (g *grpcClient) ListSources(ctx context.Context, req *sourcev1.ListSourcesRequest) (*sourcev1.ListSourcesResponse, error) {
	return callGRPC(ctx, req, g.c.ListSources)
}

func (g *grpcClient) GetSource(ctx context.Context, req *sourcev1.GetSourceRequest) (*sourcev1.GetSourceResponse, error) {
	return callGRPC(ctx, req, g.c.GetSource)
}

func (g *grpcClient) GetFilters(ctx context.Context, req *sourcev1.GetFiltersRequest) (*sourcev1.GetFiltersResponse, error) {
	return callGRPC(ctx, req, g.c.GetFilters)
}

func (g *grpcClient) Popular(ctx context.Context, req *sourcev1.PopularRequest) (*sourcev1.PopularResponse, error) {
	return callGRPC(ctx, req, g.c.Popular)
}

func (g *grpcClient) Latest(ctx context.Context, req *sourcev1.LatestRequest) (*sourcev1.LatestResponse, error) {
	return callGRPC(ctx, req, g.c.Latest)
}

func (g *grpcClient) Search(ctx context.Context, req *sourcev1.SearchRequest) (*sourcev1.SearchResponse, error) {
	return callGRPC(ctx, req, g.c.Search)
}

func (g *grpcClient) ResolveUrl(ctx context.Context, req *sourcev1.ResolveUrlRequest) (*sourcev1.ResolveUrlResponse, error) {
	return callGRPC(ctx, req, g.c.ResolveUrl)
}

func (g *grpcClient) GetSeries(ctx context.Context, req *sourcev1.GetSeriesRequest) (*sourcev1.GetSeriesResponse, error) {
	return callGRPC(ctx, req, g.c.GetSeries)
}

func (g *grpcClient) ListChapters(ctx context.Context, req *sourcev1.ListChaptersRequest) (*sourcev1.ListChaptersResponse, error) {
	return callGRPC(ctx, req, g.c.ListChapters)
}

func (g *grpcClient) ListPages(ctx context.Context, req *sourcev1.ListPagesRequest) (*sourcev1.ListPagesResponse, error) {
	return callGRPC(ctx, req, g.c.ListPages)
}

func (g *grpcClient) GetSettingsSchema(ctx context.Context, req *sourcev1.GetSettingsSchemaRequest) (*sourcev1.GetSettingsSchemaResponse, error) {
	return callGRPC(ctx, req, g.c.GetSettingsSchema)
}

func (g *grpcClient) GetSettings(ctx context.Context, req *sourcev1.GetSettingsRequest) (*sourcev1.GetSettingsResponse, error) {
	return callGRPC(ctx, req, g.c.GetSettings)
}

func (g *grpcClient) SetSettings(ctx context.Context, req *sourcev1.SetSettingsRequest) (*sourcev1.SetSettingsResponse, error) {
	return callGRPC(ctx, req, g.c.SetSettings)
}

func (g *grpcClient) ListPackages(ctx context.Context, req *sourcev1.ListPackagesRequest) (*sourcev1.ListPackagesResponse, error) {
	return callGRPC(ctx, req, g.c.ListPackages)
}

func (g *grpcClient) GetPackage(ctx context.Context, req *sourcev1.GetPackageRequest) (*sourcev1.GetPackageResponse, error) {
	return callGRPC(ctx, req, g.c.GetPackage)
}

func (g *grpcClient) InstallPackage(ctx context.Context, req *sourcev1.InstallPackageRequest) (*sourcev1.InstallPackageResponse, error) {
	return callGRPC(ctx, req, g.c.InstallPackage)
}

func (g *grpcClient) UpdatePackage(ctx context.Context, req *sourcev1.UpdatePackageRequest) (*sourcev1.UpdatePackageResponse, error) {
	return callGRPC(ctx, req, g.c.UpdatePackage)
}

func (g *grpcClient) UninstallPackage(ctx context.Context, req *sourcev1.UninstallPackageRequest) (*sourcev1.UninstallPackageResponse, error) {
	return callGRPC(ctx, req, g.c.UninstallPackage)
}

func (g *grpcClient) SetPackageSettings(ctx context.Context, req *sourcev1.SetPackageSettingsRequest) (*sourcev1.SetPackageSettingsResponse, error) {
	return callGRPC(ctx, req, g.c.SetPackageSettings)
}

func (g *grpcClient) FetchImage(ctx context.Context, req *sourcev1.FetchImageRequest) (ImageStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	s, err := g.c.FetchImage(ctx, req)
	if err != nil {
		cancel()
		return nil, plugin.FromGRPCError(err)
	}
	return &grpcStream{s: s, cancel: cancel}, nil
}

func (g *grpcClient) GetNetworkProfile(ctx context.Context, sourceID string) (*sourcev1.GetNetworkProfileResponse, error) {
	return callGRPC(ctx, &sourcev1.GetNetworkProfileRequest{SourceId: sourceID}, g.c.GetNetworkProfile)
}

func (g *grpcClient) SetNetworkProfile(ctx context.Context, sourceID string, p *sourcev1.NetworkProfile) error {
	_, err := callGRPC(ctx, &sourcev1.SetNetworkProfileRequest{SourceId: sourceID, Profile: p}, g.c.SetNetworkProfile)
	return err
}

func (g *grpcClient) Close() error {
	g.once.Do(func() {
		if g.kill != nil {
			g.kill()
		}
	})
	return nil
}

type grpcStream struct {
	s      grpc.ServerStreamingClient[sourcev1.FetchImageResponse]
	cancel context.CancelFunc
}

func (g *grpcStream) Recv() (*sourcev1.FetchImageResponse, error) {
	msg, err := g.s.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, io.EOF
		}
		return nil, plugin.FromGRPCError(err)
	}
	return msg, nil
}

func (g *grpcStream) Close() error {
	g.cancel()
	return nil
}
