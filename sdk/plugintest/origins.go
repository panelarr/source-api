// SPDX-License-Identifier: Apache-2.0

package plugintest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/plugin"
)

// fakeEpoch is when the fake's origins were first fetched and its packages
// installed.
var fakeEpoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type originState struct {
	refreshed time.Time
	broken    bool
	err       string
}

// BreakOrigin makes refreshing an origin fail with STORE_FETCH_FAILED, or
// work again.
func (f *Fake) BreakOrigin(id string, broken bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.origin(id).broken = broken
}

// origin is an origin's state. Call with f.mu held.
func (f *Fake) origin(id string) *originState {
	if f.origins == nil {
		f.origins = map[string]*originState{}
	}
	st := f.origins[id]
	if st == nil {
		st = &originState{refreshed: fakeEpoch}
		f.origins[id] = st
	}
	return st
}

// originsView lists the origins the packages' offers name. Call with f.mu
// held.
func (f *Fake) originsView() []*sourcev1.Origin {
	var out []*sourcev1.Origin
	for _, p := range f.Packages {
		for _, o := range p.Package.GetOffers() {
			i := slices.IndexFunc(out, func(v *sourcev1.Origin) bool { return v.GetId() == o.GetOriginId() })
			if i < 0 {
				st := f.origin(o.GetOriginId())
				out = append(out, &sourcev1.Origin{
					Id: o.GetOriginId(), Name: o.GetOriginName(), Url: "https://origins.example/" + o.GetOriginId(),
					RefreshedAt: timestamppb.New(st.refreshed), Error: st.err,
					SigningKeyFingerprint: digest("key", o.GetOriginId()),
				})
				i = len(out) - 1
			}
			out[i].PackageCount++
		}
	}
	return out
}

func digest(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ListOrigins lists the origins the packages' offers name.
func (f *Fake) ListOrigins(_ context.Context, _ *sourcev1.ListOriginsRequest) (*sourcev1.ListOriginsResponse, error) {
	f.count("ListOrigins")
	if f.Packages == nil {
		return nil, plugin.ErrUnimplemented("ListOrigins")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return &sourcev1.ListOriginsResponse{Origins: f.originsView()}, nil
}

// RefreshOrigin pretends to fetch an origin; see [Fake.BreakOrigin].
func (f *Fake) RefreshOrigin(_ context.Context, r *sourcev1.RefreshOriginRequest) (*sourcev1.RefreshOriginResponse, error) {
	f.count("RefreshOrigin")
	if f.Packages == nil {
		return nil, plugin.ErrUnimplemented("RefreshOrigin")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.originsView() {
		if o.GetId() != r.GetOriginId() {
			continue
		}
		st := f.origin(o.GetId())
		if st.broken {
			st.err = "origin unreachable"
			return nil, plugin.ErrStoreFetchFailed("refreshing %s: origin unreachable", o.GetName())
		}
		st.err = ""
		st.refreshed = time.Now().UTC()
		o.RefreshedAt, o.Error = timestamppb.New(st.refreshed), ""
		return &sourcev1.RefreshOriginResponse{Origin: o}, nil
	}
	return nil, plugin.ErrNotFound("origin %q not found", r.GetOriginId())
}

// ReloadPackage answers with the package as it is: there is nothing to load.
func (f *Fake) ReloadPackage(_ context.Context, r *sourcev1.ReloadPackageRequest) (*sourcev1.ReloadPackageResponse, error) {
	f.count("ReloadPackage")
	p, err := f.findPackage(r.GetPackageId())
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.state(p).installed == "" {
		return nil, plugin.Errorf(plugin.CodeFailedPrecondition, "package %q isn't installed", r.GetPackageId())
	}
	return &sourcev1.ReloadPackageResponse{Package: f.view(p)}, nil
}

// SetSourceHealthCheck turns a source's (pretend) scheduled checks on or off.
func (f *Fake) SetSourceHealthCheck(_ context.Context, r *sourcev1.SetSourceHealthCheckRequest) (*sourcev1.SetSourceHealthCheckResponse, error) {
	f.count("SetSourceHealthCheck")
	s, err := f.source(r.GetSourceId())
	if err != nil {
		return nil, err
	}
	f.mu.Lock()
	if f.checksOff == nil {
		f.checksOff = map[string]bool{}
	}
	f.checksOff[s.Source.GetId()] = !r.GetEnabled()
	f.mu.Unlock()
	return &sourcev1.SetSourceHealthCheckResponse{Source: f.sourceView(s)}, nil
}

// sourceView is a source as the API shows it now.
func (f *Fake) sourceView(s *Source) *sourcev1.Source {
	src := proto.CloneOf(s.Source)
	if p := f.packageOf(s.Source.GetId()); p != nil {
		src.PackageId = p.Package.GetId()
	}
	f.mu.Lock()
	off := f.checksOff[s.Source.GetId()]
	f.mu.Unlock()
	if off {
		if src.Health == nil {
			src.Health = &sourcev1.SourceHealth{}
		}
		src.Health.ChecksDisabled = true
	}
	return src
}
