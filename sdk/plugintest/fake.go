// SPDX-License-Identifier: Apache-2.0

// Package plugintest provides a made-up source plugin for tests: hosts can
// run against it, and the SDK's own tests and the example plugin use it.
package plugintest

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/plugin"
)

// Fake is an in-memory source plugin. Build one with [New] or fill in
// Sources yourself. It is safe for concurrent use once serving.
type Fake struct {
	plugin.Unimplemented

	// ID of the plugin, "fake" by default.
	ID string
	// Sources it serves.
	Sources []*Source
	// PageSize is the number of series per Popular/Latest/Search page, 2 by
	// default.
	PageSize int
	// Config, when set, is the plugin-level configuration: GetInfo declares
	// its schema and GetConfig/SetConfig use it.
	Config *plugin.PluginConfig
	// Packages, when set, makes the fake a plugin with the PACKAGES capability:
	// a source belongs to the package that lists it, and is listed only while
	// that package is installed and its language is enabled.
	Packages []*Package
	// Langs are the languages the plugin offers sources in; empty means all.
	Langs []string

	mu       sync.Mutex
	pkgState map[string]*packageState
	profiles map[string]*sourcev1.NetworkProfile
	settings map[string]map[string]any
	calls    map[string]int
}

// Source is a fake source and its catalogue.
type Source struct {
	Source *sourcev1.Source
	Series []*Series
}

// Series is a fake series with its chapters.
type Series struct {
	Series   *sourcev1.Series
	Chapters []*Chapter
}

// Chapter is a fake chapter with a number of pages.
type Chapter struct {
	Chapter *sourcev1.Chapter
	Pages   int
}

// New returns a fake with two sources: "Example Comics" (en: latest, search,
// filters, URL resolving, settings) and "Beispiel Comics" (de: search only),
// each with a few series, chapters and pages. Everything is made up.
func New() *Fake {
	f := &Fake{ID: "fake"}
	f.Sources = []*Source{
		NewSource("1001", "Example Comics", "en", []string{
			"The Lantern Keeper", "Salt and Iron", "A Quiet Orbit", "Paper Cranes",
		}, sourcev1.SourceCapability_SOURCE_CAPABILITY_LATEST,
			sourcev1.SourceCapability_SOURCE_CAPABILITY_SEARCH,
			sourcev1.SourceCapability_SOURCE_CAPABILITY_FILTERS,
			sourcev1.SourceCapability_SOURCE_CAPABILITY_RESOLVE_URL,
			sourcev1.SourceCapability_SOURCE_CAPABILITY_SETTINGS),
		NewSource("2001", "Beispiel Comics", "de", []string{
			"Der Leuchtturm", "Salz und Eisen", "Stille Bahn",
		}, sourcev1.SourceCapability_SOURCE_CAPABILITY_SEARCH),
	}
	// Alternative titles, so title matching has something to find.
	f.Sources[0].Series[0].Series.AltTitles = []string{"Toudai Mori", "Der Leuchtturm"}

	// Packages: the two sources come from installed packages (the first with
	// an update waiting), and a multi-language package can be installed.
	multi := []*Source{
		NewSource("3001", "Multi Comics", "en", []string{"First Light", "Second Wind"}, sourcev1.SourceCapability_SOURCE_CAPABILITY_SEARCH),
		NewSource("3002", "Multi Comics", "fr", []string{"Premier Jour"}, sourcev1.SourceCapability_SOURCE_CAPABILITY_SEARCH),
		NewSource("3003", "Multi Comics", "all", []string{"Every Tongue"}, sourcev1.SourceCapability_SOURCE_CAPABILITY_SEARCH),
	}
	f.Sources = append(f.Sources, multi...)
	f.Packages = []*Package{
		NewPackage("example.comics", "Example Comics", "1.1", []*Source{f.Sources[0]}, Offer{"store-a", "Store A", "1.1"}).Installed("1.0"),
		NewPackage("beispiel.comics", "Beispiel Comics", "2.0", []*Source{f.Sources[1]}, Offer{"store-a", "Store A", "2.0"}).Installed("2.0"),
		NewPackage("multi.comics", "Multi Comics", "3.1", multi, Offer{"store-a", "Store A", "3.0"}, Offer{"store-b", "Store B", "3.1"}),
	}
	return f
}

// NewSource builds a source with one series per title, three chapters per
// series and four pages per chapter.
func NewSource(id, name, lang string, titles []string, caps ...sourcev1.SourceCapability) *Source {
	s := &Source{Source: &sourcev1.Source{
		Id:            id,
		Name:          name,
		Lang:          lang,
		ContentRating: sourcev1.ContentRating_CONTENT_RATING_SAFE,
		Capabilities:  caps,
		HomeUrl:       "https://" + slug(name) + ".example",
	}}
	for i, title := range titles {
		key := fmt.Sprintf("/series/%d", i+1)
		se := &Series{Series: &sourcev1.Series{
			Key:         key,
			Title:       title,
			Authors:     []string{"A. Author"},
			Description: "A made-up series for tests.",
			Genres:      []string{"Adventure"},
			Status:      sourcev1.SeriesStatus_SERIES_STATUS_ONGOING,
			CoverUrl:    "fake://cover" + key,
			Url:         s.Source.GetHomeUrl() + key,
		}}
		for c := 3; c >= 1; c-- {
			num := float64(c)
			se.Chapters = append(se.Chapters, &Chapter{Pages: 4, Chapter: &sourcev1.Chapter{
				Key:        fmt.Sprintf("%s/%d", key, c),
				Name:       fmt.Sprintf("Chapter %d", c),
				Number:     &num,
				UploadedAt: timestamppb.New(timestamppb.Now().AsTime().AddDate(0, 0, -7*(4-c))),
				Url:        fmt.Sprintf("%s%s/%d", s.Source.GetHomeUrl(), key, c),
			}})
		}
		s.Series = append(s.Series, se)
	}
	return s
}

func slug(name string) string {
	return strings.ReplaceAll(strings.ToLower(name), " ", "-")
}

func (f *Fake) count(method string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[method]++
}

// Calls returns how often each method was called.
func (f *Fake) Calls() map[string]int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.calls)
}

// Profile returns the network profile pushed for a source, if any.
func (f *Fake) Profile(sourceID string) *sourcev1.NetworkProfile {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.profiles[sourceID]
}

func (f *Fake) source(id string) (*Source, error) {
	for _, s := range f.Sources {
		if s.Source.GetId() == id && f.listed(s) {
			return s, nil
		}
	}
	return nil, plugin.ErrNotFound("source %q not found", id)
}

func has(s *Source, c sourcev1.SourceCapability) bool {
	for _, x := range s.Source.GetCapabilities() {
		if x == c {
			return true
		}
	}
	return false
}

func (s *Source) series(key string) (*Series, error) {
	for _, se := range s.Series {
		if se.Series.GetKey() == key {
			return se, nil
		}
	}
	return nil, plugin.ErrNotFound("series %q not found", key)
}

func (s *Source) chapter(key string) (*Chapter, error) {
	for _, se := range s.Series {
		for _, c := range se.Chapters {
			if c.Chapter.GetKey() == key {
				return c, nil
			}
		}
	}
	return nil, plugin.ErrNotFound("chapter %q not found", key)
}

// listed is a series as lists show it: no details.
func listed(se *Series) *sourcev1.Series {
	return &sourcev1.Series{Key: se.Series.GetKey(), Title: se.Series.GetTitle(), CoverUrl: se.Series.GetCoverUrl(), Url: se.Series.GetUrl()}
}

func (f *Fake) page(all []*Series, page uint32) ([]*sourcev1.Series, bool) {
	size := f.PageSize
	if size <= 0 {
		size = 2
	}
	if page == 0 {
		page = 1
	}
	start := int(page-1) * size
	var out []*sourcev1.Series
	for i := start; i < start+size && i < len(all); i++ {
		out = append(out, listed(all[i]))
	}
	return out, start+size < len(all)
}

func (f *Fake) GetInfo(context.Context, *sourcev1.GetInfoRequest) (*sourcev1.GetInfoResponse, error) {
	f.count("GetInfo")
	id := f.ID
	if id == "" {
		id = "fake"
	}
	info := &sourcev1.PluginInfo{
		Id:             id,
		Name:           "Fake sources",
		Version:        "1.0.0",
		Contracts:      []string{plugin.Protocol},
		Capabilities:   f.capabilities(),
		CommonServices: []sourcev1.CommonService{sourcev1.CommonService_COMMON_SERVICE_NETWORK_PROFILE},
		Limits: &sourcev1.Limits{
			MaxConcurrencyPerSource: 2,
			DefaultRateLimit:        &sourcev1.RateLimit{Requests: 10, Period: durationpb.New(time.Second), Scope: sourcev1.RateLimitScope_RATE_LIMIT_SCOPE_SOURCE},
		},
	}
	if f.Config != nil {
		info.ConfigSchema = f.Config.SchemaStruct()
	}
	return &sourcev1.GetInfoResponse{Info: info}, nil
}

func (f *Fake) GetConfig(ctx context.Context, r *sourcev1.GetConfigRequest) (*sourcev1.GetConfigResponse, error) {
	if f.Config == nil {
		return f.Unimplemented.GetConfig(ctx, r)
	}
	return f.Config.GetConfig(ctx, r)
}

func (f *Fake) SetConfig(ctx context.Context, r *sourcev1.SetConfigRequest) (*sourcev1.SetConfigResponse, error) {
	if f.Config == nil {
		return f.Unimplemented.SetConfig(ctx, r)
	}
	return f.Config.SetConfig(ctx, r)
}

func (f *Fake) ListSources(_ context.Context, r *sourcev1.ListSourcesRequest) (*sourcev1.ListSourcesResponse, error) {
	f.count("ListSources")
	res := &sourcev1.ListSourcesResponse{}
	for _, s := range f.Sources {
		if (r.GetLang() == "" || r.GetLang() == s.Source.GetLang()) && f.listed(s) {
			src := proto.Clone(s.Source).(*sourcev1.Source)
			if p := f.packageOf(s.Source.GetId()); p != nil {
				src.PackageId = p.Package.GetId()
			}
			res.Sources = append(res.Sources, src)
		}
	}
	return res, nil
}

func (f *Fake) GetSource(_ context.Context, r *sourcev1.GetSourceRequest) (*sourcev1.GetSourceResponse, error) {
	f.count("GetSource")
	s, err := f.source(r.GetSourceId())
	if err != nil {
		return nil, err
	}
	return &sourcev1.GetSourceResponse{Source: s.Source}, nil
}

func (f *Fake) GetFilters(_ context.Context, r *sourcev1.GetFiltersRequest) (*sourcev1.GetFiltersResponse, error) {
	f.count("GetFilters")
	s, err := f.source(r.GetSourceId())
	if err != nil {
		return nil, err
	}
	if !has(s, sourcev1.SourceCapability_SOURCE_CAPABILITY_FILTERS) {
		return &sourcev1.GetFiltersResponse{}, nil
	}
	return &sourcev1.GetFiltersResponse{Filters: []*sourcev1.Filter{
		{Name: "Author", Kind: &sourcev1.Filter_Text{Text: &sourcev1.TextFilter{}}},
		{Name: "Status", Kind: &sourcev1.Filter_Select{Select: &sourcev1.SelectFilter{Options: []string{"Any", "Ongoing", "Completed"}}}},
		{Name: "Genres", Kind: &sourcev1.Filter_Group{Group: &sourcev1.GroupFilter{Filters: []*sourcev1.Filter{
			{Name: "Adventure", Kind: &sourcev1.Filter_TriState{TriState: &sourcev1.TriStateFilter{}}},
		}}}},
	}}, nil
}

func (f *Fake) Popular(_ context.Context, r *sourcev1.PopularRequest) (*sourcev1.PopularResponse, error) {
	f.count("Popular")
	s, err := f.source(r.GetSourceId())
	if err != nil {
		return nil, err
	}
	series, next := f.page(s.Series, r.GetPage())
	return &sourcev1.PopularResponse{Series: series, HasNextPage: next}, nil
}

func (f *Fake) Latest(_ context.Context, r *sourcev1.LatestRequest) (*sourcev1.LatestResponse, error) {
	f.count("Latest")
	s, err := f.source(r.GetSourceId())
	if err != nil {
		return nil, err
	}
	if !has(s, sourcev1.SourceCapability_SOURCE_CAPABILITY_LATEST) {
		return nil, plugin.ErrUnsupported("source %s has no latest list", s.Source.GetId())
	}
	rev := make([]*Series, len(s.Series))
	for i, se := range s.Series {
		rev[len(s.Series)-1-i] = se
	}
	series, next := f.page(rev, r.GetPage())
	return &sourcev1.LatestResponse{Series: series, HasNextPage: next}, nil
}

func (f *Fake) Search(_ context.Context, r *sourcev1.SearchRequest) (*sourcev1.SearchResponse, error) {
	f.count("Search")
	s, err := f.source(r.GetSourceId())
	if err != nil {
		return nil, err
	}
	if !has(s, sourcev1.SourceCapability_SOURCE_CAPABILITY_SEARCH) {
		return nil, plugin.ErrUnsupported("source %s can't search", s.Source.GetId())
	}
	q := strings.ToLower(strings.TrimSpace(r.GetQuery()))
	var hits []*Series
	for _, se := range s.Series {
		if q == "" || matches(se.Series, q) {
			hits = append(hits, se)
		}
	}
	series, next := f.page(hits, r.GetPage())
	return &sourcev1.SearchResponse{Series: series, HasNextPage: next}, nil
}

func matches(se *sourcev1.Series, q string) bool {
	if strings.Contains(strings.ToLower(se.GetTitle()), q) || strings.EqualFold(se.GetUrl(), q) {
		return true
	}
	for _, alt := range se.GetAltTitles() {
		if strings.Contains(strings.ToLower(alt), q) {
			return true
		}
	}
	return false
}

func (f *Fake) ResolveUrl(_ context.Context, r *sourcev1.ResolveUrlRequest) (*sourcev1.ResolveUrlResponse, error) { //nolint:revive // named after the RPC
	f.count("ResolveUrl")
	for _, s := range f.Sources {
		if r.GetSourceId() != "" && r.GetSourceId() != s.Source.GetId() {
			continue
		}
		if !has(s, sourcev1.SourceCapability_SOURCE_CAPABILITY_RESOLVE_URL) {
			continue
		}
		for _, se := range s.Series {
			if se.Series.GetUrl() == r.GetUrl() {
				return &sourcev1.ResolveUrlResponse{SourceId: s.Source.GetId(), Series: listed(se)}, nil
			}
		}
	}
	return nil, plugin.ErrNotFound("no source knows %q", r.GetUrl())
}

func (f *Fake) GetSeries(_ context.Context, r *sourcev1.GetSeriesRequest) (*sourcev1.GetSeriesResponse, error) {
	f.count("GetSeries")
	s, err := f.source(r.GetSourceId())
	if err != nil {
		return nil, err
	}
	se, err := s.series(r.GetKey())
	if err != nil {
		return nil, err
	}
	out := proto.Clone(se.Series).(*sourcev1.Series)
	out.DetailsLoaded = true
	return &sourcev1.GetSeriesResponse{Series: out}, nil
}

func (f *Fake) ListChapters(_ context.Context, r *sourcev1.ListChaptersRequest) (*sourcev1.ListChaptersResponse, error) {
	f.count("ListChapters")
	s, err := f.source(r.GetSourceId())
	if err != nil {
		return nil, err
	}
	se, err := s.series(r.GetSeriesKey())
	if err != nil {
		return nil, err
	}
	res := &sourcev1.ListChaptersResponse{}
	for _, c := range se.Chapters {
		res.Chapters = append(res.Chapters, c.Chapter)
	}
	return res, nil
}

func (f *Fake) ListPages(_ context.Context, r *sourcev1.ListPagesRequest) (*sourcev1.ListPagesResponse, error) {
	f.count("ListPages")
	s, err := f.source(r.GetSourceId())
	if err != nil {
		return nil, err
	}
	c, err := s.chapter(r.GetChapterKey())
	if err != nil {
		return nil, err
	}
	res := &sourcev1.ListPagesResponse{}
	for i := range c.Pages {
		res.Pages = append(res.Pages, &sourcev1.Page{Index: uint32(i), Ref: fmt.Sprintf("page:%s/%d", c.Chapter.GetKey(), i)}) //nolint:gosec // small
	}
	return res, nil
}

func (f *Fake) FetchImage(_ context.Context, r *sourcev1.FetchImageRequest, send plugin.ImageSendFunc) error {
	f.count("FetchImage")
	s, err := f.source(r.GetSourceId())
	if err != nil {
		return err
	}
	ref := r.GetRef()
	switch {
	case strings.HasPrefix(ref, "page:"):
		key := strings.TrimPrefix(ref, "page:")
		cut := strings.LastIndex(key, "/")
		c, err := s.chapter(key[:max(cut, 0)])
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(key[cut+1:])
		if err != nil || n < 0 || n >= c.Pages {
			return plugin.ErrNotFound("page %q not found", ref)
		}
	case strings.HasPrefix(ref, "fake://cover/"):
		if _, err := s.series(strings.TrimPrefix(ref, "fake://cover")); err != nil {
			return err
		}
	default:
		return plugin.ErrNotFound("image %q not found", ref)
	}
	img := Image(ref)
	return plugin.SendImage(send, "image/png", int64(len(img)), bytes.NewReader(img))
}

// Image returns the PNG the fake serves for ref: a small image in a colour
// derived from ref.
func Image(ref string) []byte {
	var h byte
	for _, b := range []byte(ref) {
		h = h*31 + b
	}
	m := image.NewRGBA(image.Rect(0, 0, 24, 32))
	for y := range 32 {
		for x := range 24 {
			m.Set(x, y, color.RGBA{R: h, G: 255 - h, B: byte(x * 10), A: 255}) //nolint:gosec // small
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, m)
	return buf.Bytes()
}

var settingsSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"show_adult": map[string]any{"type": "boolean", "title": "Show adult series", "default": false},
	},
	"additionalProperties": false,
}

// checkSettings fails unless the source exists and has settings.
func (f *Fake) checkSettings(id string) error {
	s, err := f.source(id)
	if err != nil {
		return err
	}
	if !has(s, sourcev1.SourceCapability_SOURCE_CAPABILITY_SETTINGS) {
		return plugin.ErrUnsupported("source %s has no settings", id)
	}
	return nil
}

func (f *Fake) GetSettingsSchema(_ context.Context, r *sourcev1.GetSettingsSchemaRequest) (*sourcev1.GetSettingsSchemaResponse, error) {
	if err := f.checkSettings(r.GetSourceId()); err != nil {
		return nil, err
	}
	st, err := structpb.NewStruct(settingsSchema)
	if err != nil {
		return nil, err
	}
	return &sourcev1.GetSettingsSchemaResponse{Schema: st}, nil
}

func (f *Fake) values(id string) map[string]any {
	out := map[string]any{"show_adult": false}
	maps.Copy(out, f.settings[id])
	return out
}

func (f *Fake) GetSettings(_ context.Context, r *sourcev1.GetSettingsRequest) (*sourcev1.GetSettingsResponse, error) {
	if err := f.checkSettings(r.GetSourceId()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	st, err := structpb.NewStruct(f.values(r.GetSourceId()))
	return &sourcev1.GetSettingsResponse{Values: st}, err
}

func (f *Fake) SetSettings(_ context.Context, r *sourcev1.SetSettingsRequest) (*sourcev1.SetSettingsResponse, error) {
	if err := f.checkSettings(r.GetSourceId()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, v := range r.GetValues().AsMap() {
		if _, ok := v.(bool); k != "show_adult" || !ok {
			return nil, plugin.ErrInvalidArgument("unknown or invalid setting %q", k)
		}
	}
	if f.settings == nil {
		f.settings = map[string]map[string]any{}
	}
	cur := f.values(r.GetSourceId())
	maps.Copy(cur, r.GetValues().AsMap())
	f.settings[r.GetSourceId()] = cur
	st, err := structpb.NewStruct(cur)
	return &sourcev1.SetSettingsResponse{Values: st}, err
}

// GetNetworkProfile returns the pushed profile, password left out, or none.
func (f *Fake) GetNetworkProfile(_ context.Context, r *sourcev1.GetNetworkProfileRequest) (*sourcev1.GetNetworkProfileResponse, error) {
	if _, err := f.source(r.GetSourceId()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.profiles[r.GetSourceId()]
	if p == nil {
		return &sourcev1.GetNetworkProfileResponse{Origin: sourcev1.NetworkProfileOrigin_NETWORK_PROFILE_ORIGIN_NONE}, nil
	}
	out := proto.Clone(p).(*sourcev1.NetworkProfile)
	if out.GetProxy() != nil {
		out.Proxy.Password = ""
	}
	return &sourcev1.GetNetworkProfileResponse{Profile: out, Origin: sourcev1.NetworkProfileOrigin_NETWORK_PROFILE_ORIGIN_PUSHED}, nil
}

func (f *Fake) SetNetworkProfile(_ context.Context, r *sourcev1.SetNetworkProfileRequest) (*sourcev1.SetNetworkProfileResponse, error) {
	if _, err := f.source(r.GetSourceId()); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.profiles == nil {
		f.profiles = map[string]*sourcev1.NetworkProfile{}
	}
	if r.GetProfile() == nil {
		delete(f.profiles, r.GetSourceId())
	} else {
		f.profiles[r.GetSourceId()] = proto.Clone(r.GetProfile()).(*sourcev1.NetworkProfile)
	}
	return &sourcev1.SetNetworkProfileResponse{}, nil
}
