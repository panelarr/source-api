// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"connectrpc.com/connect"
	"github.com/hashicorp/go-hclog"
	goplugin "github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/structpb"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/gen/panelarr/source/v1/sourcev1connect"
)

// connectServer exposes a Plugin as a Connect handler (standalone mode).
type connectServer struct {
	p Plugin
}

var _ sourcev1connect.SourcePluginServiceHandler = (*connectServer)(nil)

func unaryConnect[Req, Res any](ctx context.Context, req *connect.Request[Req], f func(context.Context, *Req) (*Res, error)) (*connect.Response[Res], error) {
	res, err := f(ctx, req.Msg)
	if err != nil {
		return nil, ToConnectError(err)
	}
	return connect.NewResponse(res), nil
}

func (s *connectServer) GetInfo(ctx context.Context, r *connect.Request[sourcev1.GetInfoRequest]) (*connect.Response[sourcev1.GetInfoResponse], error) {
	return unaryConnect(ctx, r, s.p.GetInfo)
}

func (s *connectServer) GetConfig(ctx context.Context, r *connect.Request[sourcev1.GetConfigRequest]) (*connect.Response[sourcev1.GetConfigResponse], error) {
	return unaryConnect(ctx, r, s.p.GetConfig)
}

func (s *connectServer) SetConfig(ctx context.Context, r *connect.Request[sourcev1.SetConfigRequest]) (*connect.Response[sourcev1.SetConfigResponse], error) {
	return unaryConnect(ctx, r, s.p.SetConfig)
}

func (s *connectServer) Health(ctx context.Context, r *connect.Request[sourcev1.HealthRequest]) (*connect.Response[sourcev1.HealthResponse], error) {
	return unaryConnect(ctx, r, s.p.Health)
}

func (s *connectServer) ListSources(ctx context.Context, r *connect.Request[sourcev1.ListSourcesRequest]) (*connect.Response[sourcev1.ListSourcesResponse], error) {
	return unaryConnect(ctx, r, s.p.ListSources)
}

func (s *connectServer) GetSource(ctx context.Context, r *connect.Request[sourcev1.GetSourceRequest]) (*connect.Response[sourcev1.GetSourceResponse], error) {
	return unaryConnect(ctx, r, s.p.GetSource)
}

func (s *connectServer) GetFilters(ctx context.Context, r *connect.Request[sourcev1.GetFiltersRequest]) (*connect.Response[sourcev1.GetFiltersResponse], error) {
	return unaryConnect(ctx, r, s.p.GetFilters)
}

func (s *connectServer) Popular(ctx context.Context, r *connect.Request[sourcev1.PopularRequest]) (*connect.Response[sourcev1.PopularResponse], error) {
	return unaryConnect(ctx, r, s.p.Popular)
}

func (s *connectServer) Latest(ctx context.Context, r *connect.Request[sourcev1.LatestRequest]) (*connect.Response[sourcev1.LatestResponse], error) {
	return unaryConnect(ctx, r, s.p.Latest)
}

func (s *connectServer) Search(ctx context.Context, r *connect.Request[sourcev1.SearchRequest]) (*connect.Response[sourcev1.SearchResponse], error) {
	return unaryConnect(ctx, r, s.p.Search)
}

func (s *connectServer) ResolveUrl(ctx context.Context, r *connect.Request[sourcev1.ResolveUrlRequest]) (*connect.Response[sourcev1.ResolveUrlResponse], error) {
	return unaryConnect(ctx, r, s.p.ResolveUrl)
}

func (s *connectServer) GetSeries(ctx context.Context, r *connect.Request[sourcev1.GetSeriesRequest]) (*connect.Response[sourcev1.GetSeriesResponse], error) {
	return unaryConnect(ctx, r, s.p.GetSeries)
}

func (s *connectServer) ListChapters(ctx context.Context, r *connect.Request[sourcev1.ListChaptersRequest]) (*connect.Response[sourcev1.ListChaptersResponse], error) {
	return unaryConnect(ctx, r, s.p.ListChapters)
}

func (s *connectServer) ListPages(ctx context.Context, r *connect.Request[sourcev1.ListPagesRequest]) (*connect.Response[sourcev1.ListPagesResponse], error) {
	return unaryConnect(ctx, r, s.p.ListPages)
}

func (s *connectServer) GetSettingsSchema(ctx context.Context, r *connect.Request[sourcev1.GetSettingsSchemaRequest]) (*connect.Response[sourcev1.GetSettingsSchemaResponse], error) {
	return unaryConnect(ctx, r, s.p.GetSettingsSchema)
}

func (s *connectServer) GetSettings(ctx context.Context, r *connect.Request[sourcev1.GetSettingsRequest]) (*connect.Response[sourcev1.GetSettingsResponse], error) {
	return unaryConnect(ctx, r, s.p.GetSettings)
}

func (s *connectServer) SetSettings(ctx context.Context, r *connect.Request[sourcev1.SetSettingsRequest]) (*connect.Response[sourcev1.SetSettingsResponse], error) {
	return unaryConnect(ctx, r, s.p.SetSettings)
}

func (s *connectServer) SetNetworkProfile(ctx context.Context, r *connect.Request[sourcev1.SetNetworkProfileRequest]) (*connect.Response[sourcev1.SetNetworkProfileResponse], error) {
	return unaryConnect(ctx, r, s.p.SetNetworkProfile)
}

func (s *connectServer) ListPackages(ctx context.Context, r *connect.Request[sourcev1.ListPackagesRequest]) (*connect.Response[sourcev1.ListPackagesResponse], error) {
	return unaryConnect(ctx, r, s.p.ListPackages)
}

func (s *connectServer) GetPackage(ctx context.Context, r *connect.Request[sourcev1.GetPackageRequest]) (*connect.Response[sourcev1.GetPackageResponse], error) {
	return unaryConnect(ctx, r, s.p.GetPackage)
}

func (s *connectServer) InstallPackage(ctx context.Context, r *connect.Request[sourcev1.InstallPackageRequest]) (*connect.Response[sourcev1.InstallPackageResponse], error) {
	return unaryConnect(ctx, r, s.p.InstallPackage)
}

func (s *connectServer) UpdatePackage(ctx context.Context, r *connect.Request[sourcev1.UpdatePackageRequest]) (*connect.Response[sourcev1.UpdatePackageResponse], error) {
	return unaryConnect(ctx, r, s.p.UpdatePackage)
}

func (s *connectServer) UninstallPackage(ctx context.Context, r *connect.Request[sourcev1.UninstallPackageRequest]) (*connect.Response[sourcev1.UninstallPackageResponse], error) {
	return unaryConnect(ctx, r, s.p.UninstallPackage)
}

func (s *connectServer) SetPackageSettings(ctx context.Context, r *connect.Request[sourcev1.SetPackageSettingsRequest]) (*connect.Response[sourcev1.SetPackageSettingsResponse], error) {
	return unaryConnect(ctx, r, s.p.SetPackageSettings)
}

func (s *connectServer) FetchImage(ctx context.Context, r *connect.Request[sourcev1.FetchImageRequest], stream *connect.ServerStream[sourcev1.FetchImageResponse]) error {
	return ToConnectError(s.p.FetchImage(ctx, r.Msg, stream.Send))
}

// grpcServer exposes a Plugin as a gRPC service (managed mode).
type grpcServer struct {
	sourcev1.UnimplementedSourcePluginServiceServer
	p Plugin
}

func unaryGRPC[Req, Res any](ctx context.Context, req *Req, f func(context.Context, *Req) (*Res, error)) (*Res, error) {
	res, err := f(ctx, req)
	if err != nil {
		return nil, ToGRPCError(err)
	}
	return res, nil
}

func (s *grpcServer) GetInfo(ctx context.Context, r *sourcev1.GetInfoRequest) (*sourcev1.GetInfoResponse, error) {
	return unaryGRPC(ctx, r, s.p.GetInfo)
}

func (s *grpcServer) GetConfig(ctx context.Context, r *sourcev1.GetConfigRequest) (*sourcev1.GetConfigResponse, error) {
	return unaryGRPC(ctx, r, s.p.GetConfig)
}

func (s *grpcServer) SetConfig(ctx context.Context, r *sourcev1.SetConfigRequest) (*sourcev1.SetConfigResponse, error) {
	return unaryGRPC(ctx, r, s.p.SetConfig)
}

func (s *grpcServer) Health(ctx context.Context, r *sourcev1.HealthRequest) (*sourcev1.HealthResponse, error) {
	return unaryGRPC(ctx, r, s.p.Health)
}

func (s *grpcServer) ListSources(ctx context.Context, r *sourcev1.ListSourcesRequest) (*sourcev1.ListSourcesResponse, error) {
	return unaryGRPC(ctx, r, s.p.ListSources)
}

func (s *grpcServer) GetSource(ctx context.Context, r *sourcev1.GetSourceRequest) (*sourcev1.GetSourceResponse, error) {
	return unaryGRPC(ctx, r, s.p.GetSource)
}

func (s *grpcServer) GetFilters(ctx context.Context, r *sourcev1.GetFiltersRequest) (*sourcev1.GetFiltersResponse, error) {
	return unaryGRPC(ctx, r, s.p.GetFilters)
}

func (s *grpcServer) Popular(ctx context.Context, r *sourcev1.PopularRequest) (*sourcev1.PopularResponse, error) {
	return unaryGRPC(ctx, r, s.p.Popular)
}

func (s *grpcServer) Latest(ctx context.Context, r *sourcev1.LatestRequest) (*sourcev1.LatestResponse, error) {
	return unaryGRPC(ctx, r, s.p.Latest)
}

func (s *grpcServer) Search(ctx context.Context, r *sourcev1.SearchRequest) (*sourcev1.SearchResponse, error) {
	return unaryGRPC(ctx, r, s.p.Search)
}

func (s *grpcServer) ResolveUrl(ctx context.Context, r *sourcev1.ResolveUrlRequest) (*sourcev1.ResolveUrlResponse, error) {
	return unaryGRPC(ctx, r, s.p.ResolveUrl)
}

func (s *grpcServer) GetSeries(ctx context.Context, r *sourcev1.GetSeriesRequest) (*sourcev1.GetSeriesResponse, error) {
	return unaryGRPC(ctx, r, s.p.GetSeries)
}

func (s *grpcServer) ListChapters(ctx context.Context, r *sourcev1.ListChaptersRequest) (*sourcev1.ListChaptersResponse, error) {
	return unaryGRPC(ctx, r, s.p.ListChapters)
}

func (s *grpcServer) ListPages(ctx context.Context, r *sourcev1.ListPagesRequest) (*sourcev1.ListPagesResponse, error) {
	return unaryGRPC(ctx, r, s.p.ListPages)
}

func (s *grpcServer) GetSettingsSchema(ctx context.Context, r *sourcev1.GetSettingsSchemaRequest) (*sourcev1.GetSettingsSchemaResponse, error) {
	return unaryGRPC(ctx, r, s.p.GetSettingsSchema)
}

func (s *grpcServer) GetSettings(ctx context.Context, r *sourcev1.GetSettingsRequest) (*sourcev1.GetSettingsResponse, error) {
	return unaryGRPC(ctx, r, s.p.GetSettings)
}

func (s *grpcServer) SetSettings(ctx context.Context, r *sourcev1.SetSettingsRequest) (*sourcev1.SetSettingsResponse, error) {
	return unaryGRPC(ctx, r, s.p.SetSettings)
}

func (s *grpcServer) SetNetworkProfile(ctx context.Context, r *sourcev1.SetNetworkProfileRequest) (*sourcev1.SetNetworkProfileResponse, error) {
	return unaryGRPC(ctx, r, s.p.SetNetworkProfile)
}

func (s *grpcServer) ListPackages(ctx context.Context, r *sourcev1.ListPackagesRequest) (*sourcev1.ListPackagesResponse, error) {
	return unaryGRPC(ctx, r, s.p.ListPackages)
}

func (s *grpcServer) GetPackage(ctx context.Context, r *sourcev1.GetPackageRequest) (*sourcev1.GetPackageResponse, error) {
	return unaryGRPC(ctx, r, s.p.GetPackage)
}

func (s *grpcServer) InstallPackage(ctx context.Context, r *sourcev1.InstallPackageRequest) (*sourcev1.InstallPackageResponse, error) {
	return unaryGRPC(ctx, r, s.p.InstallPackage)
}

func (s *grpcServer) UpdatePackage(ctx context.Context, r *sourcev1.UpdatePackageRequest) (*sourcev1.UpdatePackageResponse, error) {
	return unaryGRPC(ctx, r, s.p.UpdatePackage)
}

func (s *grpcServer) UninstallPackage(ctx context.Context, r *sourcev1.UninstallPackageRequest) (*sourcev1.UninstallPackageResponse, error) {
	return unaryGRPC(ctx, r, s.p.UninstallPackage)
}

func (s *grpcServer) SetPackageSettings(ctx context.Context, r *sourcev1.SetPackageSettingsRequest) (*sourcev1.SetPackageSettingsResponse, error) {
	return unaryGRPC(ctx, r, s.p.SetPackageSettings)
}

func (s *grpcServer) FetchImage(r *sourcev1.FetchImageRequest, stream grpc.ServerStreamingServer[sourcev1.FetchImageResponse]) error {
	return ToGRPCError(s.p.FetchImage(stream.Context(), r, stream.Send))
}

// RegisterGRPC registers p on a gRPC server, for hosts and tests that run
// their own grpc-go server.
func RegisterGRPC(s *grpc.Server, p Plugin) {
	sourcev1.RegisterSourcePluginServiceServer(s, &grpcServer{p: p})
}

// GRPCPlugin is the go-plugin glue for source plugins. Hosts use it with a
// nil Impl to dispense a client; plugins serve with Impl set.
type GRPCPlugin struct {
	goplugin.NetRPCUnsupportedPlugin
	Impl Plugin
}

// GRPCServer registers the plugin on the go-plugin gRPC server.
func (g *GRPCPlugin) GRPCServer(_ *goplugin.GRPCBroker, s *grpc.Server) error {
	RegisterGRPC(s, g.Impl)
	return nil
}

// GRPCClient returns a gRPC client for the plugin service.
func (g *GRPCPlugin) GRPCClient(_ context.Context, _ *goplugin.GRPCBroker, c *grpc.ClientConn) (any, error) {
	return sourcev1.NewSourcePluginServiceClient(c), nil
}

// PluginSet is the go-plugin plugin set for source plugins.
func PluginSet(impl Plugin) goplugin.PluginSet {
	return goplugin.PluginSet{PluginName: &GRPCPlugin{Impl: impl}}
}

func serveManaged(p Plugin) {
	goplugin.Serve(&goplugin.ServeConfig{
		HandshakeConfig: Handshake,
		Plugins:         PluginSet(p),
		GRPCServer: func(opts []grpc.ServerOption) *grpc.Server {
			opts = append(opts, grpc.MaxRecvMsgSize(MaxMessageSize), grpc.MaxSendMsgSize(MaxMessageSize))
			return grpc.NewServer(opts...)
		},
		Logger: hclog.New(&hclog.LoggerOptions{Name: "plugin", JSONFormat: true, Level: hclog.Info}),
	})
}

// applyConfigFile sends the configuration in a JSON file to the plugin.
func applyConfigFile(ctx context.Context, p Plugin, path string) error {
	raw, err := os.ReadFile(path) //nolint:gosec // the operator chose the file
	if err != nil {
		return fmt.Errorf("plugin: config: %w", err)
	}
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return fmt.Errorf("plugin: config %s is not a JSON object: %w", path, err)
	}
	st, err := structpb.NewStruct(values)
	if err != nil {
		return fmt.Errorf("plugin: config: %w", err)
	}
	if _, err := p.SetConfig(ctx, &sourcev1.SetConfigRequest{Values: st}); err != nil {
		return fmt.Errorf("plugin: config %s: %w", path, err)
	}
	return nil
}
