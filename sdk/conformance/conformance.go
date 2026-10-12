// SPDX-License-Identifier: Apache-2.0

// Package conformance checks that a source plugin follows the
// panelarr.source.v1 protocol. For each source it walks list → popular →
// latest → search → details → chapters → pages → image, and it checks the
// plugin's info, configuration, health, capabilities and error mapping.
//
// Problems are protocol violations: a conformant plugin has none. A source
// whose site is down is not a protocol violation, so failing steps are
// reported per source, and only a malformed answer or error is a problem.
//
// Run it from a plugin's own tests with [Run], against any client with
// [Check], or from the command line with source-conformance.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/structpb"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/client"
	"github.com/panelarr/source-api/sdk/plugin"
	"github.com/panelarr/source-api/sdk/settings"
)

// Options select what to exercise.
type Options struct {
	// Config is applied with SetConfig before anything else, e.g. to point
	// the plugin at a test server. Only for plugins with a config schema.
	Config map[string]any
	// SourceIDs limits the walk to these sources. Empty means all.
	SourceIDs []string
	// MaxSources limits the walk to the first n sources. 0 means all.
	MaxSources int
	// SearchQuery is searched on every source that can search. Empty means
	// the title of the source's first popular series, which then should be
	// found.
	SearchQuery string
	// SkipImages skips FetchImage.
	SkipImages bool
	// PackageID, for plugins with the PACKAGES capability, is a package to
	// install, narrow to one language, restore and (if it wasn't installed)
	// uninstall again. Without it the package checks only read.
	PackageID string
	// Timeout bounds the whole check. Default 5 minutes.
	Timeout time.Duration
}

// Report is what Check found.
type Report struct {
	Info *sourcev1.PluginInfo
	// Problems break the protocol. A conformant plugin has none.
	Problems []string
	// Warnings are likely mistakes that don't break the protocol.
	Warnings []string
	// Sources are the walks of the checked sources.
	Sources []*SourceReport
	// Packages is how many packages ListPackages returned (PACKAGES plugins).
	Packages int
	// PackageSteps is the install walk of Options.PackageID.
	PackageSteps []Step
}

// SourceReport is the walk through one source.
type SourceReport struct {
	ID    string
	Name  string
	Lang  string
	Steps []Step
}

// OK reports whether every step that ran passed.
func (s *SourceReport) OK() bool {
	for _, st := range s.Steps {
		if !st.OK && !st.Skipped {
			return false
		}
	}
	return true
}

// Step is one call of the walk.
type Step struct {
	Name    string
	OK      bool
	Skipped bool
	// Detail says what came back ("12 series") or why it failed or was
	// skipped.
	Detail string
	// Reason is the error reason of a failed call, e.g. "SOURCE_DOWN".
	Reason   string
	Duration time.Duration
}

func (r *Report) problem(format string, args ...any) {
	r.Problems = append(r.Problems, fmt.Sprintf(format, args...))
}

func (r *Report) warn(format string, args ...any) {
	r.Warnings = append(r.Warnings, fmt.Sprintf(format, args...))
}

// OK reports whether no problems were found.
func (r *Report) OK() bool { return len(r.Problems) == 0 }

// Run checks the plugin behind c and fails t for every problem. Warnings
// and failing source steps are logged.
func Run(t testing.TB, c client.Client, opts Options) *Report {
	t.Helper()
	rep, err := Check(context.Background(), c, opts)
	if err != nil {
		t.Fatalf("conformance: %v", err)
	}
	for _, w := range rep.Warnings {
		t.Logf("conformance warning: %s", w)
	}
	for _, s := range rep.Sources {
		for _, st := range s.Steps {
			if !st.OK && !st.Skipped {
				t.Logf("conformance: source %s: %s failed: %s", s.ID, st.Name, st.Detail)
			}
		}
	}
	for _, p := range rep.Problems {
		t.Errorf("conformance: %s", p)
	}
	return rep
}

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// knownReasons are the error reasons of the protocol.
var knownReasons = func() map[string]bool {
	m := map[string]bool{}
	for v := range sourcev1.ErrorReason_name {
		if r := plugin.ReasonOf(sourcev1.ErrorReason(v)); r != "" {
			m[string(r)] = true
		}
	}
	return m
}()

// Check runs the checks. The error is for failures to run them at all
// (e.g. the plugin can't be reached); protocol violations go to the report.
func Check(ctx context.Context, c client.Client, opts Options) (*Report, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	rep := &Report{}

	info, err := c.GetInfo(ctx)
	if err != nil {
		return nil, fmt.Errorf("get info: %w", err)
	}
	rep.Info = info
	checkInfo(rep, info)
	checkConfig(ctx, rep, c, info, opts.Config)

	checkHealth(ctx, rep, c)

	sources := checkSources(ctx, rep, c)
	if len(opts.SourceIDs) > 0 {
		sources = slices.DeleteFunc(sources, func(s *sourcev1.Source) bool { return !slices.Contains(opts.SourceIDs, s.GetId()) })
		if len(sources) == 0 {
			return nil, errors.New("none of the requested sources exists")
		}
	}
	if opts.MaxSources > 0 && len(sources) > opts.MaxSources {
		sources = sources[:opts.MaxSources]
	}

	checkUnknownSource(ctx, rep, c)
	checkPackages(ctx, rep, c, info, opts.PackageID)
	if slices.Contains(info.GetCapabilities(), sourcev1.PluginCapability_PLUGIN_CAPABILITY_NETWORK_PROFILES) && len(sources) > 0 {
		// Removing a pushed profile is always allowed and changes nothing a
		// test cares about.
		if err := c.SetNetworkProfile(ctx, sources[0].GetId(), nil); err != nil {
			rep.problem("SetNetworkProfile: removing the profile of source %s failed: %v", sources[0].GetId(), err)
		}
		checkNetworkProfile(ctx, rep, c, sources[0].GetId())
	}

	for _, s := range sources {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		w := &walk{ctx: ctx, rep: rep, c: c, src: s, opts: opts,
			out: &SourceReport{ID: s.GetId(), Name: s.GetName(), Lang: s.GetLang()}}
		w.run()
		rep.Sources = append(rep.Sources, w.out)
	}
	return rep, nil
}

func checkHealth(ctx context.Context, rep *Report, c client.Client) {
	first, err := c.Health(ctx)
	if err != nil {
		rep.problem("Health: %v", err)
		return
	}
	if first.GetStatus() == sourcev1.HealthStatus_HEALTH_STATUS_UNSPECIFIED {
		rep.problem("Health: status is unspecified")
	}
	if first.GetInstanceId() == "" {
		rep.warn("Health: no instance_id; hosts can't tell when the plugin restarted")
		return
	}
	if again, err := c.Health(ctx); err == nil && again.GetInstanceId() != first.GetInstanceId() {
		rep.problem("Health: instance_id changed from %q to %q while the plugin ran", first.GetInstanceId(), again.GetInstanceId())
	}
}

// checkNetworkProfile, after a pushed profile was removed: the source's
// profile isn't the pushed one, and no secret comes back. The call is optional.
func checkNetworkProfile(ctx context.Context, rep *Report, c client.Client, sourceID string) {
	res, err := c.GetNetworkProfile(ctx, sourceID)
	if plugin.ErrorCode(err) == plugin.CodeUnimplemented {
		return
	}
	if err != nil {
		rep.problem("GetNetworkProfile (source %s): %v", sourceID, err)
		return
	}
	switch {
	case res.GetOrigin() == sourcev1.NetworkProfileOrigin_NETWORK_PROFILE_ORIGIN_UNSPECIFIED:
		rep.problem("GetNetworkProfile (source %s): origin is unspecified", sourceID)
	case res.GetOrigin() == sourcev1.NetworkProfileOrigin_NETWORK_PROFILE_ORIGIN_PUSHED:
		rep.problem("GetNetworkProfile (source %s): still the pushed profile after it was removed", sourceID)
	}
	if res.GetProfile().GetProxy().GetPassword() != "" {
		rep.problem("GetNetworkProfile (source %s): returned the proxy password", sourceID)
	}
}

func checkInfo(rep *Report, info *sourcev1.PluginInfo) {
	if !idPattern.MatchString(info.GetId()) {
		rep.problem("GetInfo: id %q must match %s", info.GetId(), idPattern)
	}
	if info.GetName() == "" {
		rep.problem("GetInfo: name is empty")
	}
	if info.GetVersion() == "" {
		rep.problem("GetInfo: version is empty")
	}
	if !slices.Contains(info.GetContracts(), plugin.Protocol) {
		rep.problem("GetInfo: contracts must include %q", plugin.Protocol)
	}
	if slices.Contains(info.GetCapabilities(), sourcev1.PluginCapability_PLUGIN_CAPABILITY_UNSPECIFIED) {
		rep.problem("GetInfo: capabilities include UNSPECIFIED")
	}
	if slices.Contains(info.GetCommonServices(), sourcev1.CommonService_COMMON_SERVICE_UNSPECIFIED) {
		rep.problem("GetInfo: common services include UNSPECIFIED")
	}
	if rl := info.GetLimits().GetDefaultRateLimit(); rl != nil && (rl.GetRequests() == 0 || rl.GetPeriod().AsDuration() <= 0) {
		rep.problem("GetInfo: the default rate limit needs requests and a period")
	}
	if s := info.GetConfigSchema(); s != nil {
		if _, err := settings.FromStruct(s); err != nil {
			rep.problem("GetInfo: config schema: %v", err)
		}
	}
	uses := slices.Contains(info.GetCommonServices(), sourcev1.CommonService_COMMON_SERVICE_NETWORK_PROFILE)
	applies := slices.Contains(info.GetCapabilities(), sourcev1.PluginCapability_PLUGIN_CAPABILITY_NETWORK_PROFILES)
	if uses != applies {
		rep.warn("GetInfo: the network profile common service and the NETWORK_PROFILES capability usually go together")
	}
}

func checkConfig(ctx context.Context, rep *Report, c client.Client, info *sourcev1.PluginInfo, values map[string]any) {
	if info.GetConfigSchema() == nil {
		if _, err := c.GetConfig(ctx); plugin.ErrorCode(err) != plugin.CodeUnimplemented {
			rep.warn("GetConfig: a plugin without a config schema should answer UNIMPLEMENTED, got %v", err)
		}
		if len(values) > 0 {
			rep.problem("config given, but the plugin declares no config schema")
		}
		return
	}
	schema, err := settings.FromStruct(info.GetConfigSchema())
	if err != nil {
		return // reported by checkInfo
	}
	if len(values) > 0 {
		st, err := structpb.NewStruct(values)
		if err != nil {
			rep.problem("config: %v", err)
			return
		}
		got, err := c.SetConfig(ctx, st)
		if err != nil {
			rep.problem("SetConfig: %v", err)
			return
		}
		checkRedacted(rep, "SetConfig", schema, got)
	}
	got, err := c.GetConfig(ctx)
	if err != nil {
		rep.problem("GetConfig: %v", err)
		return
	}
	checkRedacted(rep, "GetConfig", schema, got)
	if err := schema.Validate(got.AsMap()); err != nil && len(schema.WriteOnly()) == 0 {
		rep.problem("GetConfig: the values don't match the config schema: %v", err)
	}
}

func checkRedacted(rep *Report, call string, schema *settings.Schema, values *structpb.Struct) {
	for _, name := range schema.WriteOnly() {
		if v := values.GetFields()[name]; v != nil && v.GetStringValue() != "" {
			rep.problem("%s: returned the writeOnly value %q; secrets must come back empty", call, name)
		}
	}
}

func checkSources(ctx context.Context, rep *Report, c client.Client) []*sourcev1.Source {
	res, err := c.ListSources(ctx, &sourcev1.ListSourcesRequest{})
	if err != nil {
		rep.problem("ListSources: %v", err)
		return nil
	}
	if len(res.GetSources()) == 0 {
		rep.warn("ListSources: no sources")
	}
	seen := map[string]bool{}
	langs := map[string]int{}
	for i, s := range res.GetSources() {
		switch {
		case s.GetId() == "":
			rep.problem("ListSources: source %d has no id", i)
		case seen[s.GetId()]:
			rep.problem("ListSources: source id %q appears twice", s.GetId())
		}
		seen[s.GetId()] = true
		if s.GetName() == "" {
			rep.problem("ListSources: source %q has no name", s.GetId())
		}
		if s.GetLang() == "" {
			rep.problem("ListSources: source %q has no language", s.GetId())
		}
		langs[s.GetLang()]++
		if slices.Contains(s.GetCapabilities(), sourcev1.SourceCapability_SOURCE_CAPABILITY_UNSPECIFIED) {
			rep.problem("ListSources: source %q has an UNSPECIFIED capability", s.GetId())
		}
		if h := s.GetHealth(); h.GetStatus() == sourcev1.SourceHealthStatus_SOURCE_HEALTH_STATUS_FAILING && h.GetReason() == "" {
			rep.warn("ListSources: source %q is failing without a reason", s.GetId())
		}
	}
	for lang, n := range langs {
		if lang == "" {
			continue
		}
		byLang, err := c.ListSources(ctx, &sourcev1.ListSourcesRequest{Lang: lang})
		if err != nil {
			rep.problem("ListSources(lang=%s): %v", lang, err)
			continue
		}
		if len(byLang.GetSources()) != n || slices.ContainsFunc(byLang.GetSources(), func(s *sourcev1.Source) bool { return s.GetLang() != lang }) {
			rep.problem("ListSources(lang=%s): expected the %d sources in that language", lang, n)
		}
	}
	return res.GetSources()
}

func checkUnknownSource(ctx context.Context, rep *Report, c client.Client) {
	_, err := c.GetSource(ctx, &sourcev1.GetSourceRequest{SourceId: "conformance-no-such-source"})
	if plugin.ErrorCode(err) != plugin.CodeNotFound && plugin.ErrorCode(err) != plugin.CodeInvalidArgument {
		rep.problem("GetSource: an unknown source must be NOT_FOUND (or INVALID_ARGUMENT for a malformed id), got %v", err)
	}
}

// checkError checks that a failed call carries a well-formed error.
func checkError(rep *Report, call string, err error) {
	code := plugin.ErrorCode(err)
	reason := plugin.ErrorReason(err)
	if reason != "" && !knownReasons[string(reason)] {
		rep.problem("%s: unknown error reason %q", call, reason)
	}
	if (code == plugin.CodeUnavailable || code == plugin.CodeResourceExhausted) && reason == "" {
		rep.warn("%s: %s without an error reason; hosts can't tell why the source failed", call, code)
	}
	if code == plugin.CodeInternal {
		rep.warn("%s: internal error: %v", call, err)
	}
}

// walk goes through one source.
type walk struct {
	ctx  context.Context
	rep  *Report
	c    client.Client
	src  *sourcev1.Source
	opts Options
	out  *SourceReport

	first    *sourcev1.Series
	chapters []*sourcev1.Chapter
	pages    []*sourcev1.Page
}

func (w *walk) has(c sourcev1.SourceCapability) bool {
	return slices.Contains(w.src.GetCapabilities(), c)
}

func (w *walk) call(prefix string) string {
	return prefix + " (source " + w.src.GetId() + ")"
}

// step runs f as a step. f returns a detail; an error fails the step and is
// checked for a well-formed status.
func (w *walk) step(name string, f func() (string, error)) bool {
	started := time.Now()
	detail, err := f()
	st := Step{Name: name, OK: err == nil, Detail: detail, Duration: time.Since(started)}
	if err != nil {
		st.Detail = err.Error()
		st.Reason = string(plugin.ErrorReason(err))
		checkError(w.rep, w.call(name), err)
	}
	w.out.Steps = append(w.out.Steps, st)
	return err == nil
}

func (w *walk) skip(name, why string) {
	w.out.Steps = append(w.out.Steps, Step{Name: name, Skipped: true, Detail: why})
}

func (w *walk) run() {
	w.step("GetSource", func() (string, error) {
		res, err := w.c.GetSource(w.ctx, &sourcev1.GetSourceRequest{SourceId: w.src.GetId()})
		if err != nil {
			return "", err
		}
		if res.GetSource().GetId() != w.src.GetId() {
			w.rep.problem("%s: returned source %q", w.call("GetSource"), res.GetSource().GetId())
		}
		return res.GetSource().GetName(), nil
	})
	w.filters()
	popular := w.step("Popular", func() (string, error) {
		res, err := w.c.Popular(w.ctx, &sourcev1.PopularRequest{SourceId: w.src.GetId(), Page: 1})
		if err != nil {
			return "", err
		}
		w.checkSeriesList("Popular", res.GetSeries())
		if len(res.GetSeries()) > 0 {
			w.first = res.GetSeries()[0]
		}
		return fmt.Sprintf("%d series", len(res.GetSeries())), nil
	})
	w.latest()
	w.search()
	if !popular || w.first == nil {
		for _, name := range []string{"GetSeries", "ResolveUrl", "ListChapters", "ListPages", "FetchImage"} {
			w.skip(name, "no series from Popular")
		}
		w.settings()
		return
	}
	w.details()
	w.chaptersAndPages()
	w.image()
	w.settings()
	w.notFound()
}

func (w *walk) filters() {
	if !w.has(sourcev1.SourceCapability_SOURCE_CAPABILITY_FILTERS) {
		return
	}
	w.step("GetFilters", func() (string, error) {
		res, err := w.c.GetFilters(w.ctx, &sourcev1.GetFiltersRequest{SourceId: w.src.GetId()})
		if err != nil {
			return "", err
		}
		if len(res.GetFilters()) == 0 {
			w.rep.warn("%s: the source has the FILTERS capability but no filters", w.call("GetFilters"))
		}
		for i, f := range res.GetFilters() {
			if f.GetKind() == nil {
				w.rep.problem("%s: filter %d has no kind", w.call("GetFilters"), i)
			}
		}
		return fmt.Sprintf("%d filters", len(res.GetFilters())), nil
	})
}

func (w *walk) checkSeriesList(call string, list []*sourcev1.Series) {
	seen := map[string]bool{}
	for i, s := range list {
		switch {
		case s.GetKey() == "":
			w.rep.problem("%s: series %d has no key", w.call(call), i)
		case seen[s.GetKey()]:
			w.rep.warn("%s: series key %q appears twice on one page", w.call(call), s.GetKey())
		}
		seen[s.GetKey()] = true
		if s.GetTitle() == "" {
			w.rep.problem("%s: series %q has no title", w.call(call), s.GetKey())
		}
	}
}

func (w *walk) latest() {
	res, err := w.c.Latest(w.ctx, &sourcev1.LatestRequest{SourceId: w.src.GetId(), Page: 1})
	if !w.has(sourcev1.SourceCapability_SOURCE_CAPABILITY_LATEST) {
		if err == nil || plugin.ErrorCode(err) != plugin.CodeUnimplemented {
			w.rep.warn("%s: a source without the LATEST capability should answer UNIMPLEMENTED, got %v", w.call("Latest"), err)
		}
		w.skip("Latest", "no LATEST capability")
		return
	}
	w.step("Latest", func() (string, error) {
		if err != nil {
			return "", err
		}
		w.checkSeriesList("Latest", res.GetSeries())
		return fmt.Sprintf("%d series", len(res.GetSeries())), nil
	})
}

func (w *walk) search() {
	if !w.has(sourcev1.SourceCapability_SOURCE_CAPABILITY_SEARCH) {
		_, err := w.c.Search(w.ctx, &sourcev1.SearchRequest{SourceId: w.src.GetId(), Page: 1, Query: "a"})
		if err == nil || plugin.ErrorCode(err) != plugin.CodeUnimplemented {
			w.rep.warn("%s: a source without the SEARCH capability should answer UNIMPLEMENTED, got %v", w.call("Search"), err)
		}
		w.skip("Search", "no SEARCH capability")
		return
	}
	query := w.opts.SearchQuery
	expect := ""
	if query == "" {
		if w.first == nil {
			w.skip("Search", "no query and no series from Popular")
			return
		}
		query, expect = w.first.GetTitle(), w.first.GetKey()
	}
	w.step("Search", func() (string, error) {
		res, err := w.c.Search(w.ctx, &sourcev1.SearchRequest{SourceId: w.src.GetId(), Page: 1, Query: query})
		if err != nil {
			return "", err
		}
		w.checkSeriesList("Search", res.GetSeries())
		if expect != "" && !slices.ContainsFunc(res.GetSeries(), func(s *sourcev1.Series) bool { return s.GetKey() == expect }) {
			// Hosts match series across sources by searching for titles.
			w.rep.warn("%s: searching for the title %q didn't find that series", w.call("Search"), query)
		}
		return fmt.Sprintf("%d series for %q", len(res.GetSeries()), query), nil
	})
}

func (w *walk) details() {
	w.step("GetSeries", func() (string, error) {
		res, err := w.c.GetSeries(w.ctx, &sourcev1.GetSeriesRequest{SourceId: w.src.GetId(), Key: w.first.GetKey()})
		if err != nil {
			return "", err
		}
		s := res.GetSeries()
		if s.GetKey() != w.first.GetKey() {
			w.rep.problem("%s: asked for %q, got %q", w.call("GetSeries"), w.first.GetKey(), s.GetKey())
		}
		if s.GetTitle() == "" {
			w.rep.problem("%s: series %q has no title", w.call("GetSeries"), s.GetKey())
		}
		if !s.GetDetailsLoaded() {
			w.rep.warn("%s: details_loaded is false after GetSeries", w.call("GetSeries"))
		}
		if s.GetUrl() != "" {
			w.first.Url = s.GetUrl()
		}
		return s.GetTitle(), nil
	})
	if !w.has(sourcev1.SourceCapability_SOURCE_CAPABILITY_RESOLVE_URL) {
		w.skip("ResolveUrl", "no RESOLVE_URL capability")
		return
	}
	if w.first.GetUrl() == "" {
		w.skip("ResolveUrl", "the series has no URL")
		return
	}
	w.step("ResolveUrl", func() (string, error) {
		res, err := w.c.ResolveUrl(w.ctx, &sourcev1.ResolveUrlRequest{Url: w.first.GetUrl(), SourceId: w.src.GetId()})
		if err != nil {
			return "", err
		}
		if res.GetSourceId() != w.src.GetId() || res.GetSeries().GetKey() != w.first.GetKey() {
			w.rep.warn("%s: %s resolved to %s %q, not the series it belongs to", w.call("ResolveUrl"),
				w.first.GetUrl(), res.GetSourceId(), res.GetSeries().GetKey())
		}
		return res.GetSeries().GetTitle(), nil
	})
}

func (w *walk) chaptersAndPages() {
	ok := w.step("ListChapters", func() (string, error) {
		res, err := w.c.ListChapters(w.ctx, &sourcev1.ListChaptersRequest{SourceId: w.src.GetId(), SeriesKey: w.first.GetKey()})
		if err != nil {
			return "", err
		}
		seen := map[string]bool{}
		for i, ch := range res.GetChapters() {
			switch {
			case ch.GetKey() == "":
				w.rep.problem("%s: chapter %d has no key", w.call("ListChapters"), i)
			case seen[ch.GetKey()]:
				w.rep.problem("%s: chapter key %q appears twice", w.call("ListChapters"), ch.GetKey())
			}
			seen[ch.GetKey()] = true
		}
		w.chapters = res.GetChapters()
		return fmt.Sprintf("%d chapters", len(w.chapters)), nil
	})
	if !ok || len(w.chapters) == 0 {
		w.skip("ListPages", "no chapters")
		return
	}
	// The newest chapter is the most likely to work.
	chapter := w.chapters[0]
	w.step("ListPages", func() (string, error) {
		res, err := w.c.ListPages(w.ctx, &sourcev1.ListPagesRequest{SourceId: w.src.GetId(), ChapterKey: chapter.GetKey()})
		if err != nil {
			return "", err
		}
		for i, p := range res.GetPages() {
			if p.GetIndex() != uint32(i) { //nolint:gosec // small
				w.rep.problem("%s: page %d has index %d; pages are in reading order from 0", w.call("ListPages"), i, p.GetIndex())
			}
			if p.GetRef() == "" && p.GetImageUrl() == "" {
				w.rep.problem("%s: page %d has neither a ref nor an image URL", w.call("ListPages"), i)
			}
		}
		if len(res.GetPages()) == 0 {
			w.rep.warn("%s: chapter %q has no pages", w.call("ListPages"), chapter.GetKey())
		}
		w.pages = res.GetPages()
		return fmt.Sprintf("%d pages", len(w.pages)), nil
	})
}

func (w *walk) image() {
	if w.opts.SkipImages {
		w.skip("FetchImage", "images skipped")
		return
	}
	if len(w.pages) == 0 {
		w.skip("FetchImage", "no pages")
		return
	}
	ref := w.pages[0].GetRef()
	if ref == "" {
		ref = w.pages[0].GetImageUrl()
	}
	w.step("FetchImage", func() (string, error) {
		s, err := w.c.FetchImage(w.ctx, &sourcev1.FetchImageRequest{SourceId: w.src.GetId(), Ref: ref})
		if err != nil {
			return "", err
		}
		defer s.Close()
		first, err := s.Recv()
		if err != nil {
			return "", err
		}
		md := first.GetMetadata()
		if md == nil {
			w.rep.problem("%s: the first message must carry the metadata", w.call("FetchImage"))
			return "", errors.New("no metadata")
		}
		if !strings.HasPrefix(md.GetContentType(), "image/") {
			w.rep.warn("%s: content type %q isn't an image type", w.call("FetchImage"), md.GetContentType())
		}
		var n uint64
		for {
			msg, err := s.Recv()
			if err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				return "", err
			}
			if msg.GetMetadata() != nil {
				w.rep.problem("%s: metadata may only come first", w.call("FetchImage"))
			}
			n += uint64(len(msg.GetChunk()))
		}
		if n == 0 {
			w.rep.problem("%s: no image bytes", w.call("FetchImage"))
		}
		if md.GetContentLength() > 0 && md.GetContentLength() != n {
			w.rep.problem("%s: content length %d, but %d bytes came", w.call("FetchImage"), md.GetContentLength(), n)
		}
		return fmt.Sprintf("%s, %d bytes", md.GetContentType(), n), nil
	})
}

func (w *walk) settings() {
	if !w.has(sourcev1.SourceCapability_SOURCE_CAPABILITY_SETTINGS) {
		return
	}
	w.step("Settings", func() (string, error) {
		sres, err := w.c.GetSettingsSchema(w.ctx, &sourcev1.GetSettingsSchemaRequest{SourceId: w.src.GetId()})
		if err != nil {
			return "", err
		}
		schema, err := settings.FromStruct(sres.GetSchema())
		if err != nil {
			w.rep.problem("%s: %v", w.call("GetSettingsSchema"), err)
			return "", err
		}
		vres, err := w.c.GetSettings(w.ctx, &sourcev1.GetSettingsRequest{SourceId: w.src.GetId()})
		if err != nil {
			return "", err
		}
		if err := schema.Validate(vres.GetValues().AsMap()); err != nil {
			w.rep.problem("%s: the values don't match the schema: %v", w.call("GetSettings"), err)
		}
		return fmt.Sprintf("%d settings", len(vres.GetValues().GetFields())), nil
	})
}

func (w *walk) notFound() {
	_, err := w.c.GetSeries(w.ctx, &sourcev1.GetSeriesRequest{SourceId: w.src.GetId(), Key: "/conformance/no-such-series"})
	if err == nil {
		w.rep.warn("%s: a made-up series key didn't fail", w.call("GetSeries"))
		return
	}
	checkError(w.rep, w.call("GetSeries"), err)
}
