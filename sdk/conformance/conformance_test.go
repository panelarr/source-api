// SPDX-License-Identifier: Apache-2.0

package conformance_test

import (
	"context"
	"strings"
	"testing"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/client"
	"github.com/panelarr/source-api/sdk/conformance"
	"github.com/panelarr/source-api/sdk/plugin"
	"github.com/panelarr/source-api/sdk/plugintest"
)

// broken breaks several rules on purpose.
type broken struct {
	*plugintest.Fake
}

func (b broken) GetInfo(ctx context.Context, r *sourcev1.GetInfoRequest) (*sourcev1.GetInfoResponse, error) {
	res, _ := b.Fake.GetInfo(ctx, r)
	res.Info.Id = "Not An ID"
	res.Info.Contracts = nil
	return res, nil
}

func (b broken) GetSource(ctx context.Context, r *sourcev1.GetSourceRequest) (*sourcev1.GetSourceResponse, error) {
	if r.GetSourceId() == "conformance-no-such-source" {
		return nil, plugin.Errorf(plugin.CodeInternal, "boom")
	}
	return b.Fake.GetSource(ctx, r)
}

func (b broken) ListPages(ctx context.Context, r *sourcev1.ListPagesRequest) (*sourcev1.ListPagesResponse, error) {
	res, err := b.Fake.ListPages(ctx, r)
	if err == nil && len(res.Pages) > 1 {
		res.Pages[1].Index = 7
	}
	return res, err
}

func (b broken) FetchImage(_ context.Context, _ *sourcev1.FetchImageRequest, send plugin.ImageSendFunc) error {
	// Bytes before the metadata.
	return send(&sourcev1.FetchImageResponse{Part: &sourcev1.FetchImageResponse_Chunk{Chunk: []byte{1}}})
}

func (b broken) Popular(ctx context.Context, r *sourcev1.PopularRequest) (*sourcev1.PopularResponse, error) {
	if r.GetSourceId() == "2001" {
		return nil, &plugin.Error{Code: plugin.CodeUnavailable, Reason: "MADE_UP", Message: "down"}
	}
	return b.Fake.Popular(ctx, r)
}

func (b broken) Search(ctx context.Context, r *sourcev1.SearchRequest) (*sourcev1.SearchResponse, error) {
	// Finds nothing, so title matching would fail.
	return &sourcev1.SearchResponse{}, nil
}

func TestReportsViolations(t *testing.T) {
	rep, err := conformance.Check(context.Background(), client.InProcess(broken{plugintest.New()}), conformance.Options{})
	if err != nil {
		t.Fatal(err)
	}
	problems := strings.Join(rep.Problems, "\n")
	for _, want := range []string{
		`id "Not An ID"`,
		"contracts must include",
		"an unknown source must be NOT_FOUND",
		"page 1 has index 7",
		"the first message must carry the metadata",
		`unknown error reason "MADE_UP"`,
	} {
		if !strings.Contains(problems, want) {
			t.Errorf("missing problem %q in:\n%s", want, problems)
		}
	}
	warnings := strings.Join(rep.Warnings, "\n")
	if !strings.Contains(warnings, "didn't find that series") {
		t.Errorf("missing the search warning in:\n%s", warnings)
	}
	// The failing source is a failed step, not only a problem.
	var de *conformance.SourceReport
	for _, s := range rep.Sources {
		if s.ID == "2001" {
			de = s
		}
	}
	if de == nil || de.OK() {
		t.Fatalf("source 2001 should have failed: %+v", de)
	}
}

func TestOptionsSelectSources(t *testing.T) {
	rep, err := conformance.Check(context.Background(), client.InProcess(plugintest.New()), conformance.Options{SourceIDs: []string{"2001"}, SkipImages: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Sources) != 1 || rep.Sources[0].ID != "2001" || !rep.OK() {
		t.Fatalf("report: %+v", rep)
	}
	if _, err := conformance.Check(context.Background(), client.InProcess(plugintest.New()), conformance.Options{SourceIDs: []string{"nope"}}); err == nil {
		t.Fatal("an unknown source was accepted")
	}
}
