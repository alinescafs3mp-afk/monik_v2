// Fixed authenticated LAN ingress for the eight fixed V4 API ports.
// It never launches an engine or changes a model request.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	lanAddress      = "192.168.1.195"
	maxConnections  = 64
	maxInFlight     = 16
	maxHeaderBytes  = 32 << 10
	maxRequestBytes = 64 << 20
	headerTimeout   = 10 * time.Second
	uploadTimeout   = 120 * time.Second
	writeTimeout    = 30 * time.Second
	shutdownTimeout = 10 * time.Second
	lingModel       = "ling-3-flash-124b-heretic-text"
	nemoModel       = "nemotron-3-super-120b-heretic-v2"
)

var fixedPorts = [...]int{31001, 31002, 31003, 31004, 31005, 31006, 31007, 31008}

type gateway struct {
	key       []byte
	inFlight  chan struct{}
	transport http.RoundTripper
	// Ling and Nemo retain exact validated JSON bytes. The existing Ling byte
	// budget is shared by both, independently of the 16 request slots.
	lingBodyBytes atomic.Int64
}

func newGateway(key string, transport http.RoundTripper) (*gateway, error) {
	// Do not trim a credential or include it in any error/log.
	if len(key) == 0 || len(key) > 1024 || strings.IndexFunc(key, func(r rune) bool {
		return r <= ' ' || r == 127
	}) >= 0 {
		return nil, errors.New("SPARK_API_KEY is missing or invalid")
	}
	if transport == nil {
		transport = &http.Transport{
			Proxy:                  nil,
			DialContext:            (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:      false,
			MaxIdleConns:           16,
			MaxIdleConnsPerHost:    2,
			MaxConnsPerHost:        maxInFlight,
			IdleConnTimeout:        30 * time.Second,
			ResponseHeaderTimeout:  0, // Native thinking may precede response headers.
			ExpectContinueTimeout:  time.Second,
			MaxResponseHeaderBytes: maxHeaderBytes,
			DisableCompression:     true,
		}
	}
	return &gateway{key: []byte(key), inFlight: make(chan struct{}, maxInFlight), transport: transport}, nil
}

func fixedPort(port int) bool {
	for _, allowed := range fixedPorts {
		if port == allowed {
			return true
		}
	}
	return false
}

func authenticated(r *http.Request, key []byte) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || len(values[0]) > 1031 {
		return false
	}
	parts := strings.SplitN(values[0], " ", 2)
	return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && hmac.Equal([]byte(parts[1]), key)
}

func allowedPath(u *url.URL) bool {
	if u.IsAbs() || u.Host != "" || u.Fragment != "" {
		return false
	}
	escaped := strings.ToLower(u.EscapedPath())
	if strings.Contains(escaped, "%2f") || strings.Contains(escaped, "%5c") || strings.ContainsAny(u.Path, "\\\x00\r\n") {
		return false
	}
	for _, component := range strings.Split(u.Path, "/") {
		if component == "." || component == ".." {
			return false
		}
	}
	return u.Path == "/health" || strings.HasPrefix(u.Path, "/v1/")
}

func fail(w http.ResponseWriter, status int) {
	w.Header().Set("Connection", "close")
	w.Header().Set("Cache-Control", "no-store")
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer")
	}
	// Fixed text only: no URL, credential, request payload or upstream error.
	http.Error(w, http.StatusText(status), status)
}

func failNemoModel(w http.ResponseWriter) {
	w.Header().Set("Connection", "close")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	// Fixed OpenAI error shape; never echo the supplied model or request.
	_, _ = io.WriteString(w, "{\"error\":{\"message\":\"Invalid model for this endpoint.\",\"type\":\"invalid_request_error\",\"param\":\"model\",\"code\":\"model_not_found\"}}\n")
}

func (g *gateway) handler(port int) (http.Handler, error) {
	if !fixedPort(port) {
		return nil, errors.New("port is outside the fixed V4 collection")
	}
	var requiredModel string
	switch port {
	case 31002:
		requiredModel = lingModel
	case 31004:
		requiredModel = nemoModel
	case 31006:
		requiredModel = "glm45-air-106b-derestricted"
	case 31007:
		requiredModel = "gemma4-31b-deckard-heretic"
	case 31008:
		requiredModel = "nemotron-super-49b-v15-abliterated"
	}
	target := "127.0.0.1:" + strconv.Itoa(port)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Fixed Gemma count alias only; keep query/body/model settings.
			if port == 31007 && pr.In.Method == http.MethodPost && pr.In.URL.Path == "/v1/tokenize" {
				pr.Out.URL.Path = "/tokenize"
				pr.Out.URL.RawPath = ""
			}
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = target
			pr.Out.Host = target
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Proxy-Authorization")
			pr.Out.Header.Del("Proxy-Connection")
		},
		Transport:     g.transport,
		FlushInterval: -1, // Flush each upstream read, including chunked SSE.
		ErrorLog:      log.New(io.Discard, "", 0),
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			fail(w, http.StatusBadGateway)
		},
		ModifyResponse: func(r *http.Response) error {
			// HTTP upgrades/tunnels are outside this API surface.
			if r.StatusCode == http.StatusSwitchingProtocols {
				return errors.New("upstream upgrade is forbidden")
			}
			return nil
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Connection", "close")
		if !authenticated(r, g.key) {
			fail(w, http.StatusUnauthorized)
			return
		}
		if !allowedPath(r.URL) {
			fail(w, http.StatusNotFound)
			return
		}
		gemmaCount := port == 31007 && r.URL.Path == "/v1/tokenize"
		if gemmaCount && r.URL.EscapedPath() != "/v1/tokenize" {
			fail(w, http.StatusNotFound)
			return
		}
		if gemmaCount && r.Method != http.MethodPost {
			fail(w, http.StatusMethodNotAllowed)
			return
		}
		if (r.Method != http.MethodGet && r.Method != http.MethodPost) || (r.URL.Path == "/health" && r.Method != http.MethodGet) {
			fail(w, http.StatusMethodNotAllowed)
			return
		}
		// Go's parser rejects conflicting Content-Length. Reject every request
		// Transfer-Encoding, trailer and upgrade as well, including TE+CL.
		if len(r.TransferEncoding) != 0 || len(r.Trailer) != 0 || r.Header.Get("Trailer") != "" || r.Header.Get("Upgrade") != "" || forbiddenConnectionTokens(r.Header.Values("Connection")) {
			fail(w, http.StatusBadRequest)
			return
		}
		if r.ContentLength < 0 {
			fail(w, http.StatusLengthRequired)
			return
		}
		if r.ContentLength > maxRequestBytes {
			fail(w, http.StatusRequestEntityTooLarge)
			return
		}
		if r.Method == http.MethodGet && r.ContentLength != 0 {
			fail(w, http.StatusBadRequest)
			return
		}
		select {
		case g.inFlight <- struct{}{}:
			defer func() { <-g.inFlight }()
		default:
			fail(w, http.StatusServiceUnavailable)
			return
		}
		if r.ContentLength != 0 {
			controller := http.NewResponseController(w)
			_ = controller.SetReadDeadline(time.Now().Add(uploadTimeout))
			body := &uploadBody{ReadCloser: http.MaxBytesReader(w, r.Body, maxRequestBytes), controller: controller}
			body.remaining.Store(r.ContentLength)
			r.Body = body
		}
		if requiredModel != "" && r.Method == http.MethodPost && (r.URL.Path == "/v1/chat/completions" || gemmaCount) {
			if !g.reserveLingBody(r.ContentLength) {
				fail(w, http.StatusServiceUnavailable)
				return
			}
			defer g.lingBodyBytes.Add(-r.ContentLength)
			// Content-Length was already bounded above; allocate once rather
			// than using ReadAll growth. Unknown fields are never decoded into
			// extra copies, and forwarding preserves every accepted byte.
			body := make([]byte, int(r.ContentLength))
			_, err := io.ReadFull(r.Body, body)
			if err != nil {
				fail(w, http.StatusBadRequest)
				return
			}
			if !exactModel(body, requiredModel) {
				if port != 31002 {
					failNemoModel(w)
				} else {
					fail(w, http.StatusBadRequest)
				}
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		proxy.ServeHTTP(&streamWriter{ResponseWriter: w}, r)
	}), nil
}

func (g *gateway) reserveLingBody(size int64) bool {
	for {
		used := g.lingBodyBytes.Load()
		if size < 0 || size > maxRequestBytes-used {
			return false
		}
		if g.lingBodyBytes.CompareAndSwap(used, used+size) {
			return true
		}
	}
}

func exactLingModel(body []byte) bool {
	return exactModel(body, lingModel)
}

// exactModel first delegates all JSON grammar/escaping/depth validation to
// encoding/json. The following scan only locates top-level members in that
// already-valid document; it never materializes large messages or other values.
func exactModel(body []byte, requiredModel string) bool {
	if !json.Valid(body) {
		return false
	}
	i := skipSpace(body, 0)
	if body[i] != '{' {
		return false
	}
	i = skipSpace(body, i+1)
	found := false
	for body[i] != '}' {
		keyStart := i
		i = stringEnd(body, i)
		var key string
		// "model" has at most 5 six-byte Unicode escapes plus its quotes.
		if i-keyStart <= 32 {
			_ = json.Unmarshal(body[keyStart:i], &key)
		}
		i = skipSpace(body, i)
		i = skipSpace(body, i+1) // Colon, guaranteed by json.Valid.
		valueStart := i
		i = valueEnd(body, i)
		if key == "model" {
			if found || body[valueStart] != '"' {
				return false
			}
			modelEnd := stringEnd(body, valueStart)
			if modelEnd-valueStart > 256 {
				return false
			}
			var model string
			if json.Unmarshal(body[valueStart:modelEnd], &model) != nil || model != requiredModel {
				return false
			}
			found = true
		}
		i = skipSpace(body, i)
		if body[i] == ',' {
			i = skipSpace(body, i+1)
		}
	}
	return found
}

func skipSpace(body []byte, i int) int {
	for i < len(body) && (body[i] == ' ' || body[i] == '\n' || body[i] == '\r' || body[i] == '\t') {
		i++
	}
	return i
}

func stringEnd(body []byte, i int) int {
	for i++; ; i++ {
		if body[i] == '\\' {
			i++
		} else if body[i] == '"' {
			return i + 1
		}
	}
}

func valueEnd(body []byte, i int) int {
	depth := 0
	for ; i < len(body); i++ {
		switch body[i] {
		case '"':
			i = stringEnd(body, i) - 1
		case '{', '[':
			depth++
		case '}', ']':
			if depth == 0 {
				return i
			}
			depth--
		case ',':
			if depth == 0 {
				return i
			}
		}
	}
	return i
}

func forbiddenConnectionTokens(values []string) bool {
	for _, value := range values {
		for _, token := range strings.Split(value, ",") {
			switch strings.ToLower(strings.TrimSpace(token)) {
			case "upgrade", "content-length", "transfer-encoding", "host", "authorization", "proxy-authorization", "trailer":
				return true
			}
		}
	}
	return false
}

type uploadBody struct {
	io.ReadCloser
	controller *http.ResponseController
	remaining  atomic.Int64
}

func (b *uploadBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if b.remaining.Add(-int64(n)) <= 0 {
		_ = b.controller.SetReadDeadline(time.Time{})
	}
	return n, err
}

func (b *uploadBody) Close() error {
	// Closing an incomplete HTTP body may drain its unread bytes. Do not
	// remove the upload deadline before that drain; abort it immediately.
	if b.remaining.Load() > 0 {
		_ = b.controller.SetReadDeadline(time.Now())
	}
	err := b.ReadCloser.Close()
	_ = b.controller.SetReadDeadline(time.Time{})
	return err
}

// A blocked downstream write is bounded, while gaps between LLM/SSE output
// have no write deadline and there is no total response duration limit.
type streamWriter struct{ http.ResponseWriter }

func (w *streamWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *streamWriter) Write(p []byte) (int, error) {
	c := http.NewResponseController(w.ResponseWriter)
	_ = c.SetWriteDeadline(time.Now().Add(writeTimeout))
	defer c.SetWriteDeadline(time.Time{})
	return w.ResponseWriter.Write(p)
}

func (w *streamWriter) FlushError() error {
	c := http.NewResponseController(w.ResponseWriter)
	_ = c.SetWriteDeadline(time.Now().Add(writeTimeout))
	defer c.SetWriteDeadline(time.Time{})
	return c.Flush()
}

type limitedListener struct {
	net.Listener
	slots chan struct{}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		select {
		case l.slots <- struct{}{}:
			return &limitedConn{Conn: conn, slots: l.slots}, nil
		default:
			_ = conn.Close()
		}
	}
}

type limitedConn struct {
	net.Conn
	slots chan struct{}
	once  sync.Once
}

func (c *limitedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { <-c.slots })
	return err
}

func bindListeners(listen func(string, string) (net.Listener, error)) ([]net.Listener, error) {
	slots := make(chan struct{}, maxConnections)
	listeners := make([]net.Listener, 0, len(fixedPorts))
	for _, port := range fixedPorts {
		listener, err := listen("tcp4", net.JoinHostPort(lanAddress, strconv.Itoa(port)))
		if err != nil {
			for _, previous := range listeners {
				_ = previous.Close()
			}
			return nil, fmt.Errorf("cannot bind fixed LAN port %d", port)
		}
		listeners = append(listeners, &limitedListener{Listener: listener, slots: slots})
	}
	return listeners, nil
}

func serve(ctx context.Context, g *gateway, listen func(string, string) (net.Listener, error)) error {
	listeners, err := bindListeners(listen)
	if err != nil {
		return err
	}
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
		if transport, ok := g.transport.(interface{ CloseIdleConnections() }); ok {
			transport.CloseIdleConnections()
		}
	}()
	servers := make([]*http.Server, len(listeners))
	results := make(chan error, len(listeners))
	baseContext, cancelRequests := context.WithCancel(ctx)
	defer cancelRequests()
	for i, listener := range listeners {
		handler, _ := g.handler(fixedPorts[i])
		servers[i] = &http.Server{
			Handler: handler, ReadHeaderTimeout: headerTimeout, IdleTimeout: 15 * time.Second,
			MaxHeaderBytes: maxHeaderBytes, ErrorLog: log.New(io.Discard, "", 0),
			BaseContext: func(net.Listener) context.Context { return baseContext },
		}
		go func(server *http.Server, listener net.Listener) { results <- server.Serve(listener) }(servers[i], listener)
	}
	var failure error
	select {
	case <-ctx.Done():
	case err := <-results:
		if !errors.Is(err, http.ErrServerClosed) {
			failure = errors.New("fixed LAN listener stopped unexpectedly")
		}
	}
	cancelRequests() // Cancel active upstreams before closing downstream sockets.
	deadline, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	var shutdown sync.WaitGroup
	for _, server := range servers {
		shutdown.Add(1)
		go func(server *http.Server) {
			defer shutdown.Done()
			if server.Shutdown(deadline) != nil {
				_ = server.Close()
			}
		}(server)
	}
	shutdown.Wait()
	return failure
}

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "This fixed gateway accepts no arguments")
		os.Exit(1)
	}
	g, err := newGateway(os.Getenv("SPARK_API_KEY"), nil)
	_ = os.Unsetenv("SPARK_API_KEY")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := serve(ctx, g, net.Listen); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
