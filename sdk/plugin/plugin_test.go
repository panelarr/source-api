// SPDX-License-Identifier: Apache-2.0

package plugin_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/plugin"
)

func TestSendImageChunks(t *testing.T) {
	data := bytes.Repeat([]byte{7}, plugin.ImageChunkSize*2+10)
	var msgs []*sourcev1.FetchImageResponse
	err := plugin.SendImage(func(m *sourcev1.FetchImageResponse) error { msgs = append(msgs, m); return nil }, "image/webp", int64(len(data)), bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if md := msgs[0].GetMetadata(); md.GetContentType() != "image/webp" || md.GetContentLength() != uint64(len(data)) {
		t.Fatalf("metadata: %v", md)
	}
	var got []byte
	for _, m := range msgs[1:] {
		got = append(got, m.GetChunk()...)
	}
	if len(msgs) != 4 || !bytes.Equal(got, data) {
		t.Fatalf("%d messages, %d bytes", len(msgs), len(got))
	}
	stop := errors.New("host gone")
	if err := plugin.SendImage(func(*sourcev1.FetchImageResponse) error { return stop }, "image/png", 0, bytes.NewReader(data)); !errors.Is(err, stop) {
		t.Fatalf("send error: %v", err)
	}
}

func TestErrors(t *testing.T) {
	e := plugin.ErrRateLimited(3*time.Second, "slow down")
	if s := e.Error(); !strings.Contains(s, "RATE_LIMITED") || !strings.Contains(s, "retry after 3s") {
		t.Fatalf("message: %s", s)
	}
	if plugin.ReasonOf(sourcev1.ErrorReason_ERROR_REASON_SOURCE_DOWN) != plugin.ReasonSourceDown || plugin.ReasonOf(0) != "" {
		t.Fatal("ReasonOf")
	}
	if plugin.ErrorCode(context.Canceled) != plugin.Code(1) || plugin.ErrorCode(errors.New("x")) != plugin.CodeInternal {
		t.Fatal("ErrorCode")
	}
	// Round trips through both wire formats.
	for name, back := range map[string]error{
		"grpc":    plugin.FromGRPCError(plugin.ToGRPCError(e)),
		"connect": plugin.FromConnectError(plugin.ToConnectError(e)),
	} {
		var pe *plugin.Error
		if !errors.As(back, &pe) || pe.Reason != plugin.ReasonRateLimited || pe.RetryAfter != 3*time.Second || pe.Code != plugin.CodeResourceExhausted {
			t.Errorf("%s: %#v", name, back)
		}
	}
}

func TestUnimplementedDefaults(t *testing.T) {
	var u plugin.Unimplemented
	ctx := context.Background()
	if h, _ := u.Health(ctx, nil); h.GetStatus() != sourcev1.HealthStatus_HEALTH_STATUS_SERVING {
		t.Fatal("health")
	}
	if _, err := u.Popular(ctx, nil); plugin.ErrorCode(err) != plugin.CodeUnimplemented || plugin.ErrorReason(err) != plugin.ReasonUnsupported {
		t.Fatalf("popular: %v", err)
	}
	if _, err := u.GetConfig(ctx, nil); plugin.ErrorCode(err) != plugin.CodeUnimplemented {
		t.Fatalf("config: %v", err)
	}
}
