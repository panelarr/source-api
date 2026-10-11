// SPDX-License-Identifier: Apache-2.0

package httpx

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"sync"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/plugin"
)

// Sources keeps one [Client] per source, each following the network profile
// the host pushed for that source, or the default profile when none was
// pushed. Sources with the same shared_cookie_jar name share cookies.
type Sources struct {
	opts Options

	mu       sync.Mutex
	clients  map[string]*Client
	pushed   map[string]bool
	fallback *sourcev1.NetworkProfile
	jars     map[string]http.CookieJar
}

// NewSources returns an empty set; clients are made on first use.
func NewSources(opts Options) *Sources {
	s := &Sources{opts: opts, clients: map[string]*Client{}, pushed: map[string]bool{}, jars: map[string]http.CookieJar{}}
	s.opts.SharedJar = s.sharedJar
	return s
}

func (s *Sources) sharedJar(name string) http.CookieJar {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok := s.jars[name]; ok {
		return j
	}
	j, _ := cookiejar.New(nil)
	s.jars[name] = j
	return j
}

// Client returns the client for a source.
func (s *Sources) Client(sourceID string) *Client {
	s.mu.Lock()
	c, ok := s.clients[sourceID]
	fallback := s.fallback
	s.mu.Unlock()
	if ok {
		return c
	}
	c = New(s.opts)
	if fallback != nil {
		// A default profile was valid when it was set.
		_ = c.SetProfile(fallback)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.clients[sourceID]; ok {
		return existing
	}
	s.clients[sourceID] = c
	return c
}

// HTTP returns the *http.Client for a source.
func (s *Sources) HTTP(sourceID string) *http.Client { return s.Client(sourceID).HTTP() }

// SetDefault sets the profile of sources the host pushed none for, e.g. a
// plugin's own configuration. nil means a direct connection.
func (s *Sources) SetDefault(p *sourcev1.NetworkProfile) error {
	if err := New(s.opts).SetProfile(p); err != nil {
		return err
	}
	s.mu.Lock()
	s.fallback = p
	var update []*Client
	for id, c := range s.clients {
		if !s.pushed[id] {
			update = append(update, c)
		}
	}
	s.mu.Unlock()
	for _, c := range update {
		_ = c.SetProfile(p)
	}
	return nil
}

// SetProfile applies a pushed profile to a source; nil removes it, so the
// default applies again.
func (s *Sources) SetProfile(sourceID string, p *sourcev1.NetworkProfile) error {
	c := s.Client(sourceID)
	s.mu.Lock()
	fallback := s.fallback
	s.mu.Unlock()
	effective := p
	if p == nil {
		effective = fallback
	}
	if err := c.SetProfile(effective); err != nil {
		return err
	}
	s.mu.Lock()
	s.pushed[sourceID] = p != nil
	s.mu.Unlock()
	return nil
}

// SetNetworkProfile implements the plugin RPC of the same name, so a plugin
// can pass it straight through:
//
//	func (p *Plugin) SetNetworkProfile(ctx context.Context, r *sourcev1.SetNetworkProfileRequest) (*sourcev1.SetNetworkProfileResponse, error) {
//		return p.net.SetNetworkProfile(ctx, r)
//	}
//
// Check that the source exists before, if the plugin cares.
func (s *Sources) SetNetworkProfile(_ context.Context, req *sourcev1.SetNetworkProfileRequest) (*sourcev1.SetNetworkProfileResponse, error) {
	if req.GetSourceId() == "" {
		return nil, plugin.ErrInvalidArgument("source_id is required")
	}
	if err := s.SetProfile(req.GetSourceId(), req.GetProfile()); err != nil {
		return nil, plugin.ErrInvalidArgument("%v", err)
	}
	p := req.GetProfile()
	s.opts.logger().Info("network profile applied", "source", req.GetSourceId(), "profile", p.GetName(),
		"proxy", p.GetProxy().GetUrl() != "", "solver", p.GetSolver().GetUrl() != "")
	return &sourcev1.SetNetworkProfileResponse{}, nil
}
