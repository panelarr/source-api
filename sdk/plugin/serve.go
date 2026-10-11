// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"connectrpc.com/connect"
	goplugin "github.com/hashicorp/go-plugin"

	"github.com/panelarr/source-api/gen/panelarr/source/v1/sourcev1connect"
)

// Handshake is the go-plugin handshake shared by hosts and managed source
// plugins. The cookie only stops people from running a plugin binary by hand
// by mistake; it is not a security measure.
var Handshake = goplugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "PANELARR_SOURCE_PLUGIN",
	MagicCookieValue: "8d2e6a31-panelarr.source.v1",
}

// PluginName is the name the plugin is dispensed under in go-plugin.
const PluginName = "source"

// MaxMessageSize bounds a single message between host and plugin. Images
// are streamed in chunks, so no single message needs to be large.
const MaxMessageSize = 16 << 20

// healthPath is the Health RPC, which needs no credentials.
const healthPath = "/" + sourcev1connect.SourcePluginServiceName + "/Health"

// Config controls how [Serve] runs a plugin.
type Config struct {
	// Listen is the address for standalone mode, e.g. ":8904". Empty means
	// managed mode (started by a host).
	Listen string
	// APIKey, when set, is required as "Authorization: Bearer <key>" on
	// every standalone call except Health and the /healthz probe.
	APIKey string
	// ConfigFile, when set, is a JSON file with the plugin-level
	// configuration, applied with SetConfig before serving (standalone mode).
	ConfigFile string
	// Routes are extra HTTP routes served next to the API in standalone
	// mode, e.g. /metrics or icons.
	Routes []Route
	// Logger defaults to JSON on stderr.
	Logger *slog.Logger
}

// Route is an extra HTTP route of a standalone plugin. It needs the API key
// like the API unless Public is set.
type Route struct {
	// Pattern is an http.ServeMux pattern, e.g. "GET /metrics".
	Pattern string
	Handler http.Handler
	Public  bool
}

// IsManaged reports whether the process was started by a host through the
// go-plugin handshake.
func IsManaged() bool {
	return os.Getenv(Handshake.MagicCookieKey) == Handshake.MagicCookieValue
}

// Main runs a plugin from its main function. It reads the standard flags and
// environment variables and calls [Serve]:
//
//	--listen addr    standalone mode on addr (env PLUGIN_LISTEN)
//	--api-key key    key callers must send in standalone mode (env PLUGIN_API_KEY)
//	--config file    plugin-level configuration as JSON (env PLUGIN_CONFIG)
//
// Started by a host (managed), it ignores --listen and --config: the host
// sends the configuration with SetConfig. Run by hand without --listen (and
// without [WithDefaultListen]), it prints usage and exits.
func Main(p Plugin, opts ...MainOption) {
	var mo mainOptions
	for _, o := range opts {
		o(&mo)
	}
	defListen := os.Getenv("PLUGIN_LISTEN")
	if defListen == "" {
		defListen = mo.defaultListen
	}
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	listen := fs.String("listen", defListen, "serve standalone on this address, e.g. :8904 (env PLUGIN_LISTEN)")
	apiKey := fs.String("api-key", "", "API key callers must send in standalone mode (default from PLUGIN_API_KEY)")
	configFile := fs.String("config", os.Getenv("PLUGIN_CONFIG"), "plugin configuration as a JSON file (env PLUGIN_CONFIG)")
	_ = fs.Parse(os.Args[1:])
	if *apiKey == "" {
		*apiKey = os.Getenv("PLUGIN_API_KEY")
	}
	cfg := Config{Listen: *listen, APIKey: *apiKey, ConfigFile: *configFile, Routes: mo.routes}
	if !IsManaged() && cfg.Listen == "" {
		fmt.Fprintf(os.Stderr, "This is a Panelarr source plugin.\n\n"+
			"Let a Panelarr host start it (managed mode), or run it as a service:\n\n"+
			"  %s --listen :8904 [--api-key KEY] [--config config.json]\n\n", os.Args[0])
		fs.PrintDefaults()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := Serve(ctx, p, cfg)
	stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// MainOption changes how [Main] behaves.
type MainOption func(*mainOptions)

type mainOptions struct {
	defaultListen string
	routes        []Route
}

// WithRoutes adds HTTP routes to the standalone server (see [Route]).
func WithRoutes(routes ...Route) MainOption {
	return func(o *mainOptions) { o.routes = append(o.routes, routes...) }
}

// WithDefaultListen makes the plugin serve standalone on addr when it is
// run by hand without --listen. Standalone plugins use ports from 8903 up.
func WithDefaultListen(addr string) MainOption {
	return func(o *mainOptions) { o.defaultListen = addr }
}

// Serve runs p until ctx is cancelled (standalone) or the host ends the
// plugin (managed).
func Serve(ctx context.Context, p Plugin, cfg Config) error {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
	}
	if IsManaged() {
		serveManaged(p)
		return nil
	}
	if cfg.Listen == "" {
		return errors.New("plugin: no listen address for standalone mode")
	}
	return serveStandalone(ctx, p, cfg)
}

func serveStandalone(ctx context.Context, p Plugin, cfg Config) error {
	if cfg.ConfigFile != "" {
		if err := applyConfigFile(ctx, p, cfg.ConfigFile); err != nil {
			return err
		}
		cfg.Logger.Info("configuration applied", "file", cfg.ConfigFile)
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return fmt.Errorf("plugin: listen: %w", err)
	}
	srv := NewHTTPServer(p, cfg.APIKey, cfg.Routes...)
	cfg.Logger.Info("source plugin listening", "addr", ln.Addr().String(), "auth", cfg.APIKey != "")
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// NewHTTPServer returns an HTTP server (HTTP/1.1 and cleartext HTTP/2) that
// serves the plugin over gRPC, gRPC-Web and Connect, plus an unauthenticated
// GET /healthz and any extra routes. Use it to embed a plugin in your own
// server or in tests.
func NewHTTPServer(p Plugin, apiKey string, routes ...Route) *http.Server {
	srv := &http.Server{
		Handler:           NewHandler(p, apiKey, routes...),
		ReadHeaderTimeout: 10 * time.Second,
	}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)
	return srv
}

// NewHandler returns the plugin's HTTP handler (see [NewHTTPServer]).
func NewHandler(p Plugin, apiKey string, routes ...Route) http.Handler {
	mux := http.NewServeMux()
	path, h := sourcev1connect.NewSourcePluginServiceHandler(
		&connectServer{p: p},
		connect.WithReadMaxBytes(MaxMessageSize),
		connect.WithSendMaxBytes(MaxMessageSize),
	)
	mux.Handle(path, requireKey(apiKey, h))
	for _, r := range routes {
		h := r.Handler
		if !r.Public {
			h = requireKey(apiKey, h)
		}
		mux.Handle(r.Pattern, h)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "ok\n")
	})
	return mux
}

func requireKey(key string, next http.Handler) http.Handler {
	if key == "" {
		return next
	}
	want := []byte("Bearer " + key)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == healthPath {
			next.ServeHTTP(w, r)
			return
		}
		got := []byte(strings.TrimSpace(r.Header.Get("Authorization")))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			unauthenticated(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// unauthenticated answers in the caller's protocol: a gRPC status for gRPC
// clients, a Connect error otherwise.
func unauthenticated(w http.ResponseWriter, r *http.Request) {
	const msg = "missing or wrong API key"
	if ct := r.Header.Get("Content-Type"); strings.HasPrefix(ct, "application/grpc") {
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Grpc-Status", "16")
		w.Header().Set("Grpc-Message", msg)
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = io.WriteString(w, `{"code":"unauthenticated","message":"`+msg+`"}`)
}
