package memory

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// BindAddress is the only address the server listens on. The database
// holds transcripts, so a non-loopback bind is refused before it happens.
const BindAddress = "127.0.0.1"

// DefaultPort is the port when neither --port nor AGENTVAULT_MEMORY_PORT
// names one.
const DefaultPort = 9030

// MaxBody caps a POST body; an entry is text, not an upload.
const MaxBody = 1 << 20

// MaxQuery caps the q parameter of GET /search, in bytes.
const MaxQuery = 256

// maxQueryFields caps the number of query parameters any route accepts.
const maxQueryFields = 8

// problemType prefixes the type URI of every problem+json body.
const problemType = "urn:agentvault:memory:problem:"

// csp allows nothing: the API serves JSON only, and a browser that lands on
// it must not run or load anything from it.
const csp = "default-src 'none'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// Server is the memory.api@1 HTTP handler over one Store.
type Server struct {
	store          *Store
	token          []byte
	port           int
	allowedHosts   map[string]bool
	allowedOrigins map[string]bool
	log            *log.Logger
}

// NewServer returns the handler for a listener on 127.0.0.1:port. logw
// receives one line per request (method, path, status; never the query)
// and the database errors the responses do not carry; nil means stderr.
func NewServer(store *Store, token string, port int, logw io.Writer) *Server {
	s := &Server{
		store:          store,
		token:          []byte(token),
		port:           port,
		allowedHosts:   map[string]bool{},
		allowedOrigins: map[string]bool{},
	}
	for _, h := range AllowedHosts(port) {
		s.allowedHosts[h] = true
		s.allowedOrigins["http://"+h] = true
	}
	s.log = log.New(logw, "agentvault memory: ", 0)
	if logw == nil {
		s.log = log.New(log.Writer(), "agentvault memory: ", 0)
	}
	return s
}

// AllowedHosts are the two Host values a request may carry for a bound
// port; any other is refused with 400 before routing (DNS rebinding from a
// browser tab resolves a name to 127.0.0.1 but keeps that name as Host).
func AllowedHosts(port int) []string {
	return []string{BindAddress + ":" + strconv.Itoa(port), "localhost:" + strconv.Itoa(port)}
}

// Listen binds 127.0.0.1:port (0 for an ephemeral port) and checks the
// address the kernel bound is loopback before anything is served on it.
func Listen(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(BindAddress, strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("memory: cannot bind %s:%d: %w", BindAddress, port, err)
	}
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok || !addr.IP.Equal(net.ParseIP(BindAddress)) {
		ln.Close()
		return nil, fmt.Errorf("memory: bound %s, not %s", ln.Addr(), BindAddress)
	}
	return ln, nil
}

// HTTPServer wraps s with the timeouts and header cap every listener gets.
func (s *Server) HTTPServer() *http.Server {
	return &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          s.log,
	}
}

// Serve serves on ln until ctx is done, then shuts down with a short grace
// period. It returns nil after a clean shutdown.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	srv := s.HTTPServer()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return srv.Close()
		}
		<-done
		return nil
	}
}

type problem struct {
	Type     string       `json:"type"`
	Title    string       `json:"title"`
	Status   int          `json:"status"`
	Detail   string       `json:"detail"`
	Instance string       `json:"instance"`
	Code     string       `json:"code"`
	Errors   []FieldError `json:"errors,omitempty"`
}

// statusWriter records the status for the request log.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// ServeHTTP runs the gates in the contract's order: Host, Origin, request
// target and query, /healthz, credential, then the route.
func (s *Server) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	w := &statusWriter{ResponseWriter: rw, status: http.StatusOK}
	s.handle(w, r)
	// Quoted: a control character in the method or path is one escaped
	// line, never a forged second one.
	s.log.Printf("%q %q %d", r.Method, instance(r), w.status)
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	if !s.allowedHosts[strings.ToLower(r.Host)] {
		s.problem(w, r, http.StatusBadRequest, "BAD_HOST", "Host must be 127.0.0.1:<port> or localhost:<port>", nil)
		return
	}
	if origins := r.Header.Values("Origin"); len(origins) > 0 &&
		(len(origins) != 1 || !s.allowedOrigins[strings.ToLower(origins[0])]) {
		s.problem(w, r, http.StatusForbidden, "FORBIDDEN_ORIGIN", "Only this server's own origin may send requests", nil)
		return
	}
	// Go's server keeps //evil.test/entries as a path; absolute-form targets
	// (GET http://evil.test/entries) set URL.Host and are refused the same way.
	path := r.URL.Path
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || r.URL.Host != "" {
		s.problem(w, r, http.StatusBadRequest, "BAD_REQUEST", "The request target must be a path", nil)
		return
	}
	query, err := parseQuery(r.URL.RawQuery)
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "BAD_QUERY",
			"The query string is malformed, repeats a name or has over 8 fields", nil)
		return
	}
	method := r.Method
	if method == http.MethodHead {
		method = http.MethodGet
	}
	if method == http.MethodGet && path == "/healthz" {
		if s.requireQuery(w, r, query, nil) {
			s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		}
		return
	}
	if !s.credentialOK(r) {
		s.problem(w, r, http.StatusUnauthorized, "UNAUTHORIZED",
			"Send Authorization: Bearer <token> (the "+TokenFileName+" file next to the database)", nil)
		return
	}
	switch path {
	case "/entries":
		switch method {
		case http.MethodGet:
			s.getEntries(w, r, query)
		case http.MethodPost:
			s.postEntries(w, r, query)
		default:
			s.methodNotAllowed(w, r, "GET, HEAD, POST")
		}
	case "/search":
		if method == http.MethodGet {
			s.getSearch(w, r, query)
		} else {
			s.methodNotAllowed(w, r, "GET, HEAD")
		}
	default:
		s.problem(w, r, http.StatusNotFound, "NOT_FOUND", "No such route", nil)
	}
}

func (s *Server) methodNotAllowed(w http.ResponseWriter, r *http.Request, allow string) {
	w.Header().Set("Allow", allow)
	s.problem(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "Allowed: "+allow, nil)
}

// parseQuery is url.ParseQuery plus the contract's limits: no repeated
// name, at most 8 fields, nothing unparsable.
func parseQuery(raw string) (map[string]string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(values))
	fields := 0
	for name, list := range values {
		if len(list) != 1 {
			return nil, errors.New("a query parameter is repeated")
		}
		fields++
		out[name] = list[0]
	}
	if fields > maxQueryFields {
		return nil, errors.New("too many query fields")
	}
	return out, nil
}

func (s *Server) requireQuery(w http.ResponseWriter, r *http.Request, query map[string]string, allowed []string) bool {
	var extra []string
	for name := range query {
		if !contains(allowed, name) {
			extra = append(extra, name)
		}
	}
	if len(extra) == 0 {
		return true
	}
	s.problem(w, r, http.StatusBadRequest, "BAD_QUERY", "unknown query parameter(s): "+joinSorted(extra), nil)
	return false
}

// joinSorted lists at most 8 names of at most 40 bytes each: the names are
// the client's own text, not a place to reflect a header's worth of it.
func joinSorted(names []string) string {
	sort.Strings(names)
	if len(names) > maxQueryFields {
		names = names[:maxQueryFields]
	}
	for i, n := range names {
		if len(n) > 40 {
			names[i] = n[:40] + "..."
		}
	}
	return strings.Join(names, ", ")
}

// credentialOK accepts exactly one Authorization header of the form
// "Bearer <token>" whose token equals the file's, compared in constant
// time. Nothing else is a credential: not a query parameter, not a cookie.
func (s *Server) credentialOK(r *http.Request) bool {
	values := r.Header.Values("Authorization")
	if len(values) != 1 {
		return false
	}
	scheme, token, found := strings.Cut(strings.TrimSpace(values[0]), " ")
	if !found || !strings.EqualFold(scheme, "Bearer") {
		return false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), s.token) == 1
}

func (s *Server) getEntries(w http.ResponseWriter, r *http.Request, query map[string]string) {
	if !s.requireQuery(w, r, query, []string{"state"}) {
		return
	}
	state, present := query["state"]
	if present && !contains(States, state) {
		s.problem(w, r, http.StatusBadRequest, "BAD_QUERY", "state must be one of "+strings.Join(States, ", "), nil)
		return
	}
	entries, err := s.store.ListEntries(state)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

func (s *Server) postEntries(w http.ResponseWriter, r *http.Request, query map[string]string) {
	if !s.requireQuery(w, r, query, nil) {
		return
	}
	if !jsonContentType(r.Header.Values("Content-Type")) {
		s.problem(w, r, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE",
			"POST /entries takes Content-Type: application/json", nil)
		return
	}
	if r.ContentLength < 0 {
		s.problem(w, r, http.StatusLengthRequired, "LENGTH_REQUIRED", "Send Content-Length, not chunked", nil)
		return
	}
	if r.ContentLength > MaxBody {
		s.problem(w, r, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE",
			"The body is over "+strconv.Itoa(MaxBody)+" bytes", nil)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxBody+1))
	if err != nil {
		s.problem(w, r, http.StatusBadRequest, "BAD_REQUEST", "The body could not be read", nil)
		return
	}
	if len(body) > MaxBody {
		s.problem(w, r, http.StatusRequestEntityTooLarge, "BODY_TOO_LARGE",
			"The body is over "+strconv.Itoa(MaxBody)+" bytes", nil)
		return
	}
	// encoding/json replaces bad UTF-8 with U+FFFD and says nothing; the
	// contract's text is UTF-8 or refused.
	var top any
	if !utf8.Valid(body) || json.Unmarshal(body, &top) != nil {
		s.problem(w, r, http.StatusBadRequest, "BAD_JSON", "The body is not UTF-8 JSON", nil)
		return
	}
	if _, ok := top.(map[string]any); !ok {
		s.problem(w, r, http.StatusUnprocessableEntity, "INVALID_ENTRY", "The body must be one memory.entry@1 object",
			[]FieldError{{Field: "", Code: "NOT_AN_OBJECT"}})
		return
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(body, &sent); err != nil {
		s.problem(w, r, http.StatusBadRequest, "BAD_JSON", "The body is not UTF-8 JSON", nil)
		return
	}
	n, errs := newEntryFrom(sent)
	if len(errs) > 0 {
		s.problem(w, r, http.StatusUnprocessableEntity, "INVALID_ENTRY", "memory.entry@1 rejects the body", errs)
		return
	}
	stored, err := s.store.InsertEntry(n, "api")
	var invalid *ErrInvalid
	if errors.As(err, &invalid) {
		s.problem(w, r, http.StatusUnprocessableEntity, "INVALID_ENTRY", "memory.entry@1 rejects the body", invalid.Errors)
		return
	}
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, stored)
}

// newEntryFrom reads the client's fields of a posted object. Unknown
// fields are refused; the server-owned ones are ignored whatever they hold.
func newEntryFrom(sent map[string]json.RawMessage) (NewEntry, []FieldError) {
	var errs []FieldError
	var unknown []string
	for name := range sent {
		if !contains(EntryFields, name) {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	for _, name := range unknown {
		errs = append(errs, FieldError{Field: name, Code: "UNKNOWN_FIELD"})
	}
	var n NewEntry
	str := func(name string, required bool) *string {
		raw, ok := sent[name]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			if required {
				errs = append(errs, FieldError{Field: name, Code: "REQUIRED_STRING"})
			}
			return nil
		}
		var v string
		if json.Unmarshal(raw, &v) != nil {
			errs = append(errs, FieldError{Field: name, Code: "REQUIRED_STRING"})
			return nil
		}
		return &v
	}
	if v := str("kind", true); v != nil {
		n.Kind = *v
	}
	if v := str("title", true); v != nil {
		n.Title = *v
	}
	if v := str("session_id", true); v != nil {
		n.SessionID = *v
	}
	if v := str("agent", true); v != nil {
		n.Agent = *v
	}
	if v := str("body", false); v != nil {
		n.Body = *v
	}
	n.RepoCode = str("repo_code", false)
	return n, errs
}

// jsonContentType accepts exactly one Content-Type of application/json
// with no charset, or charset utf-8.
func jsonContentType(values []string) bool {
	if len(values) != 1 {
		return false
	}
	media, params, err := mime.ParseMediaType(values[0])
	if err != nil || media != "application/json" {
		return false
	}
	charset := strings.ToLower(params["charset"])
	return charset == "" || charset == "utf-8" || charset == "utf8"
}

func (s *Server) getSearch(w http.ResponseWriter, r *http.Request, query map[string]string) {
	if !s.requireQuery(w, r, query, []string{"q"}) {
		return
	}
	q, present := query["q"]
	if !present {
		s.problem(w, r, http.StatusBadRequest, "BAD_QUERY", "q is required", nil)
		return
	}
	if len(q) > MaxQuery {
		s.problem(w, r, http.StatusBadRequest, "BAD_QUERY", "q is over "+strconv.Itoa(MaxQuery)+" bytes", nil)
		return
	}
	hits, err := s.store.Search(q)
	if err != nil {
		s.databaseError(w, r, err)
		return
	}
	w.Header().Set("Search-Mode", "keyword")
	s.writeJSON(w, http.StatusOK, map[string]any{"query": q, "hits": hits})
}

// databaseError logs the driver's message and answers 503 without it.
func (s *Server) databaseError(w http.ResponseWriter, r *http.Request, err error) {
	s.log.Printf("database error: %v", err)
	s.problem(w, r, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE", "The database did not answer; retry.", nil)
}

func commonHeaders(h http.Header) {
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
}

func (s *Server) writeBytes(w http.ResponseWriter, status int, body []byte, contentType string) {
	commonHeaders(w.Header())
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, doc any) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		s.log.Printf("encode: %v", err)
		s.writeBytes(w, http.StatusInternalServerError, []byte(`{"type":"`+problemType+`internal","title":"Internal Server Error","status":500,"code":"INTERNAL"}`), "application/problem+json")
		return
	}
	s.writeBytes(w, status, buf.Bytes(), "application/json")
}

func (s *Server) problem(w http.ResponseWriter, r *http.Request, status int, code, detail string, errs []FieldError) {
	doc := problem{
		Type:     problemType + strings.ToLower(strings.ReplaceAll(code, "_", "-")),
		Title:    http.StatusText(status),
		Status:   status,
		Detail:   detail,
		Instance: instance(r),
		Code:     code,
		Errors:   errs,
	}
	body, _ := json.Marshal(doc)
	s.writeBytes(w, status, body, "application/problem+json")
}

// instance is the request path, query dropped, cut at 200 bytes: it is the
// client's own input, not a place to reflect 64 KB of it.
func instance(r *http.Request) string {
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	if len(path) > 200 {
		path = path[:200]
	}
	return path
}
