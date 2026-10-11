// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/panelarr/source-api/sdk/plugin"
	"github.com/panelarr/source-api/sdk/plugintest"
)

func TestRunAgainstAPlugin(t *testing.T) {
	srv := httptest.NewUnstartedServer(plugin.NewHandler(plugintest.New(), "k"))
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	defer srv.Close()

	var out, errOut bytes.Buffer
	code := run([]string{"--target", strings.TrimPrefix(srv.URL, "http://"), "--api-key", "k"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d\n%s%s", code, out.String(), errOut.String())
	}
	for _, want := range []string{"Plugin fake 1.0.0", "ok   1001  Example Comics (en)", "FetchImage", "follows the protocol"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in:\n%s", want, out.String())
		}
	}

	out.Reset()
	if code := run([]string{"--target", srv.URL, "--api-key", "wrong"}, &out, &errOut); code != 2 {
		t.Fatalf("wrong key: exit %d", code)
	}
	if code := run([]string{}, &out, &errOut); code != 2 {
		t.Fatalf("no target: exit %d", code)
	}
	out.Reset()
	if code := run([]string{"--target", srv.URL, "--api-key", "k", "--json", "--sources", "2001"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), `"ID": "2001"`) {
		t.Fatalf("json: exit %d\n%s", code, out.String())
	}
}
