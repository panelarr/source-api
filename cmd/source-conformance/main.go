// SPDX-License-Identifier: Apache-2.0

// Command source-conformance checks a running source plugin (or a plugin
// binary it starts itself) against the panelarr.source.v1 protocol and
// prints a report.
//
//	source-conformance --target http://localhost:8904 --api-key KEY
//	source-conformance --managed ./bin/my-plugin --config config.json
//
// It exits with 1 when the plugin breaks the protocol, with 2 on bad usage
// or when the plugin can't be reached, and with 3 when --strict is set and
// a source failed a step.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/panelarr/source-api/sdk/client"
	"github.com/panelarr/source-api/sdk/conformance"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("source-conformance", flag.ContinueOnError)
	fs.SetOutput(stderr)
	target := fs.String("target", "", "URL (or host:port) of a running plugin")
	managed := fs.String("managed", "", "path of a plugin binary to start in managed mode instead")
	apiKey := fs.String("api-key", os.Getenv("PLUGIN_API_KEY"), "API key for --target (env PLUGIN_API_KEY)")
	useConnect := fs.Bool("connect", false, "use the Connect protocol instead of gRPC")
	configFile := fs.String("config", "", "plugin configuration (JSON file) to apply with SetConfig first")
	sources := fs.String("sources", "", "comma-separated source IDs to walk (default all)")
	maxSources := fs.Int("max-sources", 0, "walk at most this many sources (default all)")
	query := fs.String("query", "", "search query (default each source's first popular title)")
	skipImages := fs.Bool("skip-images", false, "don't fetch images")
	pkg := fs.String("package", "", "a package to install, narrow to one language and remove again (plugins with packages; changes the plugin)")
	timeout := fs.Duration("timeout", 5*time.Minute, "time limit for the whole check")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	strict := fs.Bool("strict", false, "also fail when a source fails a step (e.g. its site is down)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if (*target == "") == (*managed == "") {
		fmt.Fprintln(stderr, "give exactly one of --target and --managed")
		fs.Usage()
		return 2
	}
	opts := conformance.Options{MaxSources: *maxSources, SearchQuery: *query, SkipImages: *skipImages, Timeout: *timeout, PackageID: *pkg}
	if *sources != "" {
		for _, id := range strings.Split(*sources, ",") {
			if id = strings.TrimSpace(id); id != "" {
				opts.SourceIDs = append(opts.SourceIDs, id)
			}
		}
	}
	if *configFile != "" {
		raw, err := os.ReadFile(*configFile)
		if err == nil {
			err = json.Unmarshal(raw, &opts.Config)
		}
		if err != nil {
			fmt.Fprintf(stderr, "config: %v\n", err)
			return 2
		}
	}

	var c client.Client
	if *managed != "" {
		var err error
		if c, err = client.Managed(*managed, client.ManagedOptions{}); err != nil {
			fmt.Fprintf(stderr, "starting %s: %v\n", *managed, err)
			return 2
		}
	} else {
		url := *target
		if !strings.Contains(url, "://") {
			url = "http://" + url
		}
		c = client.Remote(url, client.RemoteOptions{APIKey: *apiKey, Connect: *useConnect})
	}
	defer c.Close()

	rep, err := conformance.Check(context.Background(), c, opts)
	if err != nil {
		fmt.Fprintf(stderr, "conformance: %v\n", err)
		return 2
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		printReport(stdout, rep)
	}
	switch {
	case !rep.OK():
		return 1
	case *strict && failing(rep) > 0:
		return 3
	}
	return 0
}

func failing(rep *conformance.Report) int {
	n := 0
	for _, s := range rep.Sources {
		if !s.OK() {
			n++
		}
	}
	return n
}

func printReport(w io.Writer, rep *conformance.Report) {
	info := rep.Info
	fmt.Fprintf(w, "Plugin %s %s (%s)\n\n", info.GetId(), info.GetVersion(), info.GetName())
	for _, s := range rep.Sources {
		mark := "ok  "
		if !s.OK() {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "%s %s  %s (%s)\n", mark, s.ID, s.Name, s.Lang)
		for _, st := range s.Steps {
			state := "ok"
			switch {
			case st.Skipped:
				state = "skipped"
			case !st.OK:
				state = "failed"
				if st.Reason != "" {
					state += " " + st.Reason
				}
			}
			fmt.Fprintf(w, "       %-14s %-22s %s\n", st.Name, state, st.Detail)
		}
	}
	if rep.Packages > 0 || len(rep.PackageSteps) > 0 {
		fmt.Fprintf(w, "\n%d packages\n", rep.Packages)
		for _, st := range rep.PackageSteps {
			state := "ok"
			if !st.OK {
				state = "failed"
			}
			fmt.Fprintf(w, "       %-20s %-8s %s\n", st.Name, state, st.Detail)
		}
	}
	fmt.Fprintf(w, "\n%d sources, %d failing\n", len(rep.Sources), failing(rep))
	for _, warn := range rep.Warnings {
		fmt.Fprintf(w, "warning: %s\n", warn)
	}
	for _, p := range rep.Problems {
		fmt.Fprintf(w, "PROBLEM: %s\n", p)
	}
	if rep.OK() {
		fmt.Fprintln(w, "\nThe plugin follows the protocol.")
	} else {
		fmt.Fprintf(w, "\n%d protocol problems.\n", len(rep.Problems))
	}
}
