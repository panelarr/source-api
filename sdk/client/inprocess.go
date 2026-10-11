// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"io"
	"sync"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/plugin"
)

// InProcess returns a client that calls p directly, without a process or a
// network in between. Messages are cloned on the way, as a real transport
// would copy them. Meant for tests.
func InProcess(p plugin.Plugin) Client {
	return &inProcess{p: p}
}

type inProcess struct {
	p plugin.Plugin
}

func clone[T proto.Message](m T) T {
	return proto.Clone(m).(T)
}

func call[Req, Res proto.Message](ctx context.Context, req Req, f func(context.Context, Req) (Res, error)) (Res, error) {
	res, err := f(ctx, clone(req))
	if err != nil {
		var zero Res
		return zero, err
	}
	return clone(res), nil
}

func (c *inProcess) GetInfo(ctx context.Context) (*sourcev1.PluginInfo, error) {
	res, err := call(ctx, &sourcev1.GetInfoRequest{}, c.p.GetInfo)
	return res.GetInfo(), err
}

func (c *inProcess) GetConfig(ctx context.Context) (*structpb.Struct, error) {
	res, err := call(ctx, &sourcev1.GetConfigRequest{}, c.p.GetConfig)
	return res.GetValues(), err
}

func (c *inProcess) SetConfig(ctx context.Context, values *structpb.Struct) (*structpb.Struct, error) {
	res, err := call(ctx, &sourcev1.SetConfigRequest{Values: values}, c.p.SetConfig)
	return res.GetValues(), err
}

func (c *inProcess) Health(ctx context.Context) (*sourcev1.HealthResponse, error) {
	return call(ctx, &sourcev1.HealthRequest{}, c.p.Health)
}

func (c *inProcess) ListSources(ctx context.Context, req *sourcev1.ListSourcesRequest) (*sourcev1.ListSourcesResponse, error) {
	return call(ctx, req, c.p.ListSources)
}

func (c *inProcess) GetSource(ctx context.Context, req *sourcev1.GetSourceRequest) (*sourcev1.GetSourceResponse, error) {
	return call(ctx, req, c.p.GetSource)
}

func (c *inProcess) GetFilters(ctx context.Context, req *sourcev1.GetFiltersRequest) (*sourcev1.GetFiltersResponse, error) {
	return call(ctx, req, c.p.GetFilters)
}

func (c *inProcess) Popular(ctx context.Context, req *sourcev1.PopularRequest) (*sourcev1.PopularResponse, error) {
	return call(ctx, req, c.p.Popular)
}

func (c *inProcess) Latest(ctx context.Context, req *sourcev1.LatestRequest) (*sourcev1.LatestResponse, error) {
	return call(ctx, req, c.p.Latest)
}

func (c *inProcess) Search(ctx context.Context, req *sourcev1.SearchRequest) (*sourcev1.SearchResponse, error) {
	return call(ctx, req, c.p.Search)
}

func (c *inProcess) ResolveUrl(ctx context.Context, req *sourcev1.ResolveUrlRequest) (*sourcev1.ResolveUrlResponse, error) {
	return call(ctx, req, c.p.ResolveUrl)
}

func (c *inProcess) GetSeries(ctx context.Context, req *sourcev1.GetSeriesRequest) (*sourcev1.GetSeriesResponse, error) {
	return call(ctx, req, c.p.GetSeries)
}

func (c *inProcess) ListChapters(ctx context.Context, req *sourcev1.ListChaptersRequest) (*sourcev1.ListChaptersResponse, error) {
	return call(ctx, req, c.p.ListChapters)
}

func (c *inProcess) ListPages(ctx context.Context, req *sourcev1.ListPagesRequest) (*sourcev1.ListPagesResponse, error) {
	return call(ctx, req, c.p.ListPages)
}

func (c *inProcess) GetSettingsSchema(ctx context.Context, req *sourcev1.GetSettingsSchemaRequest) (*sourcev1.GetSettingsSchemaResponse, error) {
	return call(ctx, req, c.p.GetSettingsSchema)
}

func (c *inProcess) GetSettings(ctx context.Context, req *sourcev1.GetSettingsRequest) (*sourcev1.GetSettingsResponse, error) {
	return call(ctx, req, c.p.GetSettings)
}

func (c *inProcess) SetSettings(ctx context.Context, req *sourcev1.SetSettingsRequest) (*sourcev1.SetSettingsResponse, error) {
	return call(ctx, req, c.p.SetSettings)
}

func (c *inProcess) FetchImage(ctx context.Context, req *sourcev1.FetchImageRequest) (ImageStream, error) {
	ctx, cancel := context.WithCancel(ctx)
	s := &inProcessStream{msgs: make(chan *sourcev1.FetchImageResponse, 16), cancel: cancel}
	req = clone(req)
	go func() {
		defer close(s.msgs)
		err := c.p.FetchImage(ctx, req, func(m *sourcev1.FetchImageResponse) error {
			select {
			case s.msgs <- clone(m):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		s.mu.Lock()
		s.err = err
		s.mu.Unlock()
	}()
	return s, nil
}

func (c *inProcess) SetNetworkProfile(ctx context.Context, sourceID string, p *sourcev1.NetworkProfile) error {
	var cp *sourcev1.NetworkProfile
	if p != nil {
		cp = clone(p)
	}
	_, err := c.p.SetNetworkProfile(ctx, &sourcev1.SetNetworkProfileRequest{SourceId: sourceID, Profile: cp})
	return err
}

func (c *inProcess) Close() error { return nil }

type inProcessStream struct {
	msgs   chan *sourcev1.FetchImageResponse
	cancel context.CancelFunc
	mu     sync.Mutex
	err    error
}

func (s *inProcessStream) Recv() (*sourcev1.FetchImageResponse, error) {
	m, ok := <-s.msgs
	if ok {
		return m, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	return nil, io.EOF
}

func (s *inProcessStream) Close() error {
	s.cancel()
	// Drain so the plugin goroutine can finish.
	for range s.msgs { //nolint:revive // draining
	}
	return nil
}
