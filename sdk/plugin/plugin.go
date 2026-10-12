// SPDX-License-Identifier: Apache-2.0

// Package plugin is the Go SDK for writing source plugins for Panelarr.
//
// A plugin implements [Plugin] and calls [Main] (or [Serve]) from its main
// function. The same binary then works in both run modes:
//
//   - managed: the host starts the binary as a child process and talks to it
//     over gRPC on a local socket (HashiCorp go-plugin handshake);
//   - standalone: the binary is started with --listen and serves gRPC and
//     Connect on that address, and the host connects to it by URL.
//
// Embed [Unimplemented] to get "not supported" answers for the calls a plugin
// does not need.
package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"regexp"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
)

// Protocol is the protocol package plugins built with this SDK speak.
const Protocol = "panelarr.source.v1"

// Plugin is what a source plugin implements. The methods mirror the
// SourcePluginService RPCs; see the proto for their contracts.
//
// Return an [*Error] (see [Errorf] and the Err* helpers) to control the
// status code and error reason the host sees. Any other error is reported as
// an internal error.
type Plugin interface {
	GetInfo(ctx context.Context, req *sourcev1.GetInfoRequest) (*sourcev1.GetInfoResponse, error)
	// GetConfig and SetConfig carry the plugin-level configuration described
	// by PluginInfo.config_schema. [PluginConfig] implements both.
	GetConfig(ctx context.Context, req *sourcev1.GetConfigRequest) (*sourcev1.GetConfigResponse, error)
	SetConfig(ctx context.Context, req *sourcev1.SetConfigRequest) (*sourcev1.SetConfigResponse, error)
	Health(ctx context.Context, req *sourcev1.HealthRequest) (*sourcev1.HealthResponse, error)
	ListSources(ctx context.Context, req *sourcev1.ListSourcesRequest) (*sourcev1.ListSourcesResponse, error)
	GetSource(ctx context.Context, req *sourcev1.GetSourceRequest) (*sourcev1.GetSourceResponse, error)
	GetFilters(ctx context.Context, req *sourcev1.GetFiltersRequest) (*sourcev1.GetFiltersResponse, error)
	Popular(ctx context.Context, req *sourcev1.PopularRequest) (*sourcev1.PopularResponse, error)
	Latest(ctx context.Context, req *sourcev1.LatestRequest) (*sourcev1.LatestResponse, error)
	Search(ctx context.Context, req *sourcev1.SearchRequest) (*sourcev1.SearchResponse, error)
	ResolveUrl(ctx context.Context, req *sourcev1.ResolveUrlRequest) (*sourcev1.ResolveUrlResponse, error) //nolint:revive // named after the RPC
	GetSeries(ctx context.Context, req *sourcev1.GetSeriesRequest) (*sourcev1.GetSeriesResponse, error)
	ListChapters(ctx context.Context, req *sourcev1.ListChaptersRequest) (*sourcev1.ListChaptersResponse, error)
	ListPages(ctx context.Context, req *sourcev1.ListPagesRequest) (*sourcev1.ListPagesResponse, error)
	// FetchImage streams one image through send: the metadata first, then
	// the bytes. [SendImage] does that from an io.Reader.
	FetchImage(ctx context.Context, req *sourcev1.FetchImageRequest, send ImageSendFunc) error
	GetSettingsSchema(ctx context.Context, req *sourcev1.GetSettingsSchemaRequest) (*sourcev1.GetSettingsSchemaResponse, error)
	GetSettings(ctx context.Context, req *sourcev1.GetSettingsRequest) (*sourcev1.GetSettingsResponse, error)
	SetSettings(ctx context.Context, req *sourcev1.SetSettingsRequest) (*sourcev1.SetSettingsResponse, error)
	// The package calls, for plugins with the PACKAGES capability: see the proto.
	// Package IDs must match [PackageIDPattern].
	ListPackages(ctx context.Context, req *sourcev1.ListPackagesRequest) (*sourcev1.ListPackagesResponse, error)
	GetPackage(ctx context.Context, req *sourcev1.GetPackageRequest) (*sourcev1.GetPackageResponse, error)
	InstallPackage(ctx context.Context, req *sourcev1.InstallPackageRequest) (*sourcev1.InstallPackageResponse, error)
	UpdatePackage(ctx context.Context, req *sourcev1.UpdatePackageRequest) (*sourcev1.UpdatePackageResponse, error)
	UninstallPackage(ctx context.Context, req *sourcev1.UninstallPackageRequest) (*sourcev1.UninstallPackageResponse, error)
	SetPackageSettings(ctx context.Context, req *sourcev1.SetPackageSettingsRequest) (*sourcev1.SetPackageSettingsResponse, error)
	// GetNetworkProfile returns the profile a source uses and where it comes
	// from (optional).
	GetNetworkProfile(ctx context.Context, req *sourcev1.GetNetworkProfileRequest) (*sourcev1.GetNetworkProfileResponse, error)
	// SetNetworkProfile receives a source's network profile from the host.
	// Plugins that declare the network profile common service apply it to
	// every request for that source; httpx.Sources does that for them.
	SetNetworkProfile(ctx context.Context, req *sourcev1.SetNetworkProfileRequest) (*sourcev1.SetNetworkProfileResponse, error)
}

// ImageSendFunc sends one FetchImage message to the host. It returns an
// error when the host has gone away; the plugin should then stop.
type ImageSendFunc func(*sourcev1.FetchImageResponse) error

// Unimplemented answers every call with CodeUnimplemented (reason
// UNSUPPORTED), except Health (always serving), GetFilters (no filters) and
// the settings calls (no settings). Embed it in a plugin and override what the plugin supports.
type Unimplemented struct{}

var _ Plugin = Unimplemented{}

func (Unimplemented) GetInfo(context.Context, *sourcev1.GetInfoRequest) (*sourcev1.GetInfoResponse, error) {
	return nil, ErrUnimplemented("GetInfo")
}

func (Unimplemented) GetConfig(context.Context, *sourcev1.GetConfigRequest) (*sourcev1.GetConfigResponse, error) {
	return nil, ErrUnimplemented("GetConfig")
}

func (Unimplemented) SetConfig(context.Context, *sourcev1.SetConfigRequest) (*sourcev1.SetConfigResponse, error) {
	return nil, ErrUnimplemented("SetConfig")
}

func (Unimplemented) Health(context.Context, *sourcev1.HealthRequest) (*sourcev1.HealthResponse, error) {
	return &sourcev1.HealthResponse{Status: sourcev1.HealthStatus_HEALTH_STATUS_SERVING, InstanceId: InstanceID}, nil
}

func (Unimplemented) GetNetworkProfile(context.Context, *sourcev1.GetNetworkProfileRequest) (*sourcev1.GetNetworkProfileResponse, error) {
	return nil, ErrUnimplemented("GetNetworkProfile")
}

func (Unimplemented) ListSources(context.Context, *sourcev1.ListSourcesRequest) (*sourcev1.ListSourcesResponse, error) {
	return nil, ErrUnimplemented("ListSources")
}

func (Unimplemented) GetSource(context.Context, *sourcev1.GetSourceRequest) (*sourcev1.GetSourceResponse, error) {
	return nil, ErrUnimplemented("GetSource")
}

func (Unimplemented) GetFilters(context.Context, *sourcev1.GetFiltersRequest) (*sourcev1.GetFiltersResponse, error) {
	return &sourcev1.GetFiltersResponse{}, nil
}

func (Unimplemented) Popular(context.Context, *sourcev1.PopularRequest) (*sourcev1.PopularResponse, error) {
	return nil, ErrUnimplemented("Popular")
}

func (Unimplemented) Latest(context.Context, *sourcev1.LatestRequest) (*sourcev1.LatestResponse, error) {
	return nil, ErrUnimplemented("Latest")
}

func (Unimplemented) Search(context.Context, *sourcev1.SearchRequest) (*sourcev1.SearchResponse, error) {
	return nil, ErrUnimplemented("Search")
}

func (Unimplemented) ResolveUrl(context.Context, *sourcev1.ResolveUrlRequest) (*sourcev1.ResolveUrlResponse, error) { //nolint:revive // named after the RPC
	return nil, ErrUnimplemented("ResolveUrl")
}

func (Unimplemented) GetSeries(context.Context, *sourcev1.GetSeriesRequest) (*sourcev1.GetSeriesResponse, error) {
	return nil, ErrUnimplemented("GetSeries")
}

func (Unimplemented) ListChapters(context.Context, *sourcev1.ListChaptersRequest) (*sourcev1.ListChaptersResponse, error) {
	return nil, ErrUnimplemented("ListChapters")
}

func (Unimplemented) ListPages(context.Context, *sourcev1.ListPagesRequest) (*sourcev1.ListPagesResponse, error) {
	return nil, ErrUnimplemented("ListPages")
}

func (Unimplemented) FetchImage(context.Context, *sourcev1.FetchImageRequest, ImageSendFunc) error {
	return ErrUnimplemented("FetchImage")
}

func (Unimplemented) GetSettingsSchema(context.Context, *sourcev1.GetSettingsSchemaRequest) (*sourcev1.GetSettingsSchemaResponse, error) {
	return &sourcev1.GetSettingsSchemaResponse{}, nil
}

func (Unimplemented) GetSettings(context.Context, *sourcev1.GetSettingsRequest) (*sourcev1.GetSettingsResponse, error) {
	return &sourcev1.GetSettingsResponse{}, nil
}

func (Unimplemented) SetSettings(context.Context, *sourcev1.SetSettingsRequest) (*sourcev1.SetSettingsResponse, error) {
	return &sourcev1.SetSettingsResponse{}, nil
}

func (Unimplemented) ListPackages(context.Context, *sourcev1.ListPackagesRequest) (*sourcev1.ListPackagesResponse, error) {
	return nil, ErrUnimplemented("ListPackages")
}

func (Unimplemented) GetPackage(context.Context, *sourcev1.GetPackageRequest) (*sourcev1.GetPackageResponse, error) {
	return nil, ErrUnimplemented("GetPackage")
}

func (Unimplemented) InstallPackage(context.Context, *sourcev1.InstallPackageRequest) (*sourcev1.InstallPackageResponse, error) {
	return nil, ErrUnimplemented("InstallPackage")
}

func (Unimplemented) UpdatePackage(context.Context, *sourcev1.UpdatePackageRequest) (*sourcev1.UpdatePackageResponse, error) {
	return nil, ErrUnimplemented("UpdatePackage")
}

func (Unimplemented) UninstallPackage(context.Context, *sourcev1.UninstallPackageRequest) (*sourcev1.UninstallPackageResponse, error) {
	return nil, ErrUnimplemented("UninstallPackage")
}

func (Unimplemented) SetPackageSettings(context.Context, *sourcev1.SetPackageSettingsRequest) (*sourcev1.SetPackageSettingsResponse, error) {
	return nil, ErrUnimplemented("SetPackageSettings")
}

func (Unimplemented) SetNetworkProfile(context.Context, *sourcev1.SetNetworkProfileRequest) (*sourcev1.SetNetworkProfileResponse, error) {
	return nil, ErrUnimplemented("SetNetworkProfile")
}

// PackageIDPattern is what package IDs look like: no ':', which HTTP paths
// read as a custom verb (as in ":install").
var PackageIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// InstanceID identifies this run of the process: random, set at start. The
// SDK puts it in Health answers that don't carry their own, so hosts notice a
// restart and push configuration and profiles again.
var InstanceID = func() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}()
