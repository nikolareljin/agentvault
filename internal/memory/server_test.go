package memory

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testToken = "test-token-0123456789abcdefghijklmnopqrstuv"

type testServer struct {
	store *Store
	port  int
	base  string
	logs  *bytes.Buffer
}

// startServer serves on an ephemeral loopback port with a real listener,
// so the Host header a client sends is the one the gate checks.
func startServer(t *testing.T) *testServer {
	t.Helper()
	store := openTestStore(t)
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	logs := &bytes.Buffer{}
	srv := NewServer(store, testToken, port, logs)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return &testServer{store: store, port: port, base: "http://127.0.0.1:" + strconv.Itoa(port), logs: logs}
}

type response struct {
	status  int
	headers http.Header
	body    []byte
}

func (ts *testServer) do(t *testing.T, method, target string, body string, headers map[string]string) response {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.base+target, rd)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return response{status: res.StatusCode, headers: res.Header, body: data}
}

func (ts *testServer) auth() map[string]string {
	return map[string]string{"Authorization": "Bearer " + testToken}
}

func (ts *testServer) authJSON() map[string]string {
	return map[string]string{"Authorization": "Bearer " + testToken, "Content-Type": "application/json"}
}

// raw sends the request bytes as written and returns the first response.
func (ts *testServer) raw(t *testing.T, request string) response {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(ts.port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("no HTTP response: %v", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return response{status: res.StatusCode, headers: res.Header, body: data}
}

func (ts *testServer) hostHeader() string { return "127.0.0.1:" + strconv.Itoa(ts.port) }

var problemCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{2,63}$`)

// assertProblem checks a non-2xx response is error.problem@1 with the code.
func assertProblem(t *testing.T, res response, status int, code string) map[string]any {
	t.Helper()
	if res.status != status {
		t.Fatalf("status %d, want %d; body %s", res.status, status, res.body)
	}
	if ct := res.headers.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type %q, want application/problem+json", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(res.body, &doc); err != nil {
		t.Fatalf("problem body is not JSON: %v: %s", err, res.body)
	}
	for _, key := range []string{"type", "title", "status", "code"} {
		if _, ok := doc[key]; !ok {
			t.Fatalf("problem body lacks %s: %s", key, res.body)
		}
	}
	if doc["code"] != code {
		t.Fatalf("code %v, want %s", doc["code"], code)
	}
	if !problemCode.MatchString(doc["code"].(string)) {
		t.Fatalf("code %q does not match the contract pattern", doc["code"])
	}
	if int(doc["status"].(float64)) != status {
		t.Fatalf("body status %v, want %d", doc["status"], status)
	}
	assertSecurityHeaders(t, res)
	return doc
}

func assertSecurityHeaders(t *testing.T, res response) {
	t.Helper()
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := res.headers.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if csp := res.headers.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") ||
		!strings.Contains(csp, "connect-src 'self'") {
		t.Errorf("Content-Security-Policy = %q", csp)
	}
	for name := range res.headers {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("CORS header sent: %s", name)
		}
	}
	if res.headers.Get("Server") != "" {
		t.Errorf("Server header sent: %q", res.headers.Get("Server"))
	}
}

func validBody(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "entry", "valid-inbox-decision.json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestListenIsLoopbackOnly(t *testing.T) {
	ln, err := Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().(*net.TCPAddr)
	if addr.IP.String() != BindAddress {
		t.Fatalf("bound %s, want %s", addr.IP, BindAddress)
	}
	// The same port on IPv6 loopback is free: nothing listens there.
	c, err := net.DialTimeout("tcp", net.JoinHostPort("::1", strconv.Itoa(addr.Port)), time.Second)
	if err == nil {
		c.Close()
		t.Fatal("the port answers on ::1")
	}
}

func TestHealthz(t *testing.T) {
	ts := startServer(t)
	res := ts.do(t, "GET", "/healthz", "", nil)
	if res.status != 200 || strings.TrimSpace(string(res.body)) != `{"status":"ok"}` {
		t.Fatalf("GET /healthz = %d %s", res.status, res.body)
	}
	assertSecurityHeaders(t, res)
	if ct := res.headers.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type %q", ct)
	}
	head := ts.do(t, "HEAD", "/healthz", "", nil)
	if head.status != 200 || len(head.body) != 0 || head.headers.Get("Content-Length") != strconv.Itoa(len(res.body)) {
		t.Fatalf("HEAD /healthz = %d body %d bytes, Content-Length %q", head.status, len(head.body), head.headers.Get("Content-Length"))
	}
	assertProblem(t, ts.do(t, "GET", "/healthz?x=1", "", nil), 400, "BAD_QUERY")
	assertProblem(t, ts.do(t, "POST", "/healthz", "", nil), 401, "UNAUTHORIZED")
}

func TestHostGate(t *testing.T) {
	ts := startServer(t)
	port := strconv.Itoa(ts.port)
	for _, host := range []string{"evil.test:" + port, "127.0.0.1", "127.0.0.1:1", "localhost",
		"[::1]:" + port, "127.0.0.1." + ":" + port, "0x7f000001:" + port, "127.1:" + port} {
		res := ts.do(t, "GET", "/healthz", "", map[string]string{"Host": host})
		assertProblem(t, res, 400, "BAD_HOST")
	}
	for _, host := range []string{"127.0.0.1:" + port, "localhost:" + port, "LOCALHOST:" + port} {
		if res := ts.do(t, "GET", "/healthz", "", map[string]string{"Host": host}); res.status != 200 {
			t.Fatalf("Host %s refused: %d %s", host, res.status, res.body)
		}
	}
	// No Host at all (HTTP/1.0): refused before routing, with the problem body.
	assertProblem(t, ts.raw(t, "GET /healthz HTTP/1.0\r\n\r\n"), 400, "BAD_HOST")
	// Two Host headers: Go's server refuses the request itself, before the handler.
	two := ts.raw(t, "GET /healthz HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\nHost: evil.test\r\n\r\n")
	if two.status != 400 {
		t.Fatalf("two Host headers: %d", two.status)
	}
	// Absolute-form target naming another host: the Host gate sees that host.
	assertProblem(t, ts.raw(t, "GET http://evil.test/healthz HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\n\r\n"), 400, "BAD_HOST")
	assertProblem(t, ts.raw(t, "GET http://"+ts.hostHeader()+"/healthz HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\n\r\n"), 400, "BAD_REQUEST")
}

func TestOriginGate(t *testing.T) {
	ts := startServer(t)
	own := "http://127.0.0.1:" + strconv.Itoa(ts.port)
	for _, origin := range []string{"http://evil.test", "null", "https://127.0.0.1:" + strconv.Itoa(ts.port),
		"http://127.0.0.1:1", "http://127.0.0.1:" + strconv.Itoa(ts.port) + ".evil.test", ""} {
		res := ts.do(t, "GET", "/healthz", "", map[string]string{"Origin": origin})
		assertProblem(t, res, 403, "FORBIDDEN_ORIGIN")
	}
	for _, origin := range []string{own, "http://localhost:" + strconv.Itoa(ts.port), strings.ToUpper(own)} {
		if res := ts.do(t, "GET", "/healthz", "", map[string]string{"Origin": origin}); res.status != 200 {
			t.Fatalf("own origin %s refused: %d", origin, res.status)
		}
	}
	// Two Origin headers, one of them ours, is not ours.
	assertProblem(t, ts.raw(t, "GET /healthz HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\nOrigin: "+own+
		"\r\nOrigin: http://evil.test\r\n\r\n"), 403, "FORBIDDEN_ORIGIN")
	// A CORS preflight gets no Access-Control-* header and no credential-free answer.
	pre := ts.do(t, "OPTIONS", "/entries", "", map[string]string{"Origin": own,
		"Access-Control-Request-Method": "POST"})
	assertProblem(t, pre, 401, "UNAUTHORIZED")
}

func TestCredentialGate(t *testing.T) {
	ts := startServer(t)
	cases := map[string]map[string]string{
		"none":            nil,
		"wrong token":     {"Authorization": "Bearer " + testToken[:len(testToken)-1] + "X"},
		"prefix of token": {"Authorization": "Bearer " + testToken[:20]},
		"empty bearer":    {"Authorization": "Bearer "},
		"basic":           {"Authorization": "Basic " + testToken},
		"no scheme":       {"Authorization": testToken},
		"api key header":  {"X-Api-Key": testToken},
		"cookie":          {"Cookie": "session=" + testToken},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			res := ts.do(t, "GET", "/entries", "", headers)
			doc := assertProblem(t, res, 401, "UNAUTHORIZED")
			if strings.Contains(fmt.Sprint(doc), testToken) {
				t.Fatal("the token is in the 401 body")
			}
		})
	}
	t.Run("token in query", func(t *testing.T) {
		for _, target := range []string{"/entries?token=" + testToken, "/entries?access_token=" + testToken,
			"/search?q=x&bearer=" + testToken} {
			assertProblem(t, ts.do(t, "GET", target, "", nil), 401, "UNAUTHORIZED")
		}
	})
	t.Run("two Authorization headers", func(t *testing.T) {
		res := ts.raw(t, "GET /entries HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\nAuthorization: Bearer "+testToken+
			"\r\nAuthorization: Bearer "+testToken+"\r\n\r\n")
		assertProblem(t, res, 401, "UNAUTHORIZED")
	})
	t.Run("accepted", func(t *testing.T) {
		for _, value := range []string{"Bearer " + testToken, "bearer " + testToken, "  Bearer   " + testToken + " "} {
			res := ts.do(t, "GET", "/entries", "", map[string]string{"Authorization": value})
			if res.status != 200 {
				t.Fatalf("%q refused: %d %s", value, res.status, res.body)
			}
		}
	})
	t.Run("nothing logged from the query or the token", func(t *testing.T) {
		if logs := ts.logs.String(); strings.Contains(logs, testToken) || strings.Contains(logs, "access_token") {
			t.Fatalf("log leaks the credential or the query: %s", logs)
		}
	})
}

func TestRoutesAfterAuth(t *testing.T) {
	ts := startServer(t)
	assertProblem(t, ts.do(t, "GET", "/nope", "", nil), 401, "UNAUTHORIZED")
	assertProblem(t, ts.do(t, "GET", "/nope", "", ts.auth()), 404, "NOT_FOUND")
	assertProblem(t, ts.do(t, "GET", "/entries/01JCA4N7Q8ZKX2VD5HTW3BRM9E", "", ts.auth()), 404, "NOT_FOUND")
	assertProblem(t, ts.do(t, "GET", "/entries/", "", ts.auth()), 404, "NOT_FOUND")
	res := ts.do(t, "PUT", "/entries", "{}", ts.authJSON())
	assertProblem(t, res, 405, "METHOD_NOT_ALLOWED")
	if allow := res.headers.Get("Allow"); !strings.Contains(allow, "POST") {
		t.Fatalf("Allow = %q", allow)
	}
	assertProblem(t, ts.do(t, "POST", "/search", "{}", ts.authJSON()), 405, "METHOD_NOT_ALLOWED")
	assertProblem(t, ts.do(t, "DELETE", "/entries", "", ts.auth()), 405, "METHOD_NOT_ALLOWED")
	// Path traversal and odd targets never reach a file: there is none to reach.
	for _, target := range []string{"/../entries", "/entries/../search", "/%2e%2e/entries", "/entries%00", "/entries/.."} {
		res := ts.raw(t, "GET "+target+" HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\nAuthorization: Bearer "+testToken+"\r\n\r\n")
		if res.status != 404 && res.status != 400 {
			t.Fatalf("%s: %d %s", target, res.status, res.body)
		}
	}
	assertProblem(t, ts.raw(t, "GET //evil.test/entries HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\nAuthorization: Bearer "+
		testToken+"\r\n\r\n"), 400, "BAD_REQUEST")
	// The instance field never reflects more than 200 bytes of the path.
	long := "/" + strings.Repeat("a", 5000)
	doc := assertProblem(t, ts.do(t, "GET", long, "", ts.auth()), 404, "NOT_FOUND")
	if inst := doc["instance"].(string); len(inst) > 200 {
		t.Fatalf("instance is %d bytes", len(inst))
	}
	// Query limits: repeated name, too many fields, malformed.
	assertProblem(t, ts.do(t, "GET", "/entries?state=inbox&state=inbox", "", ts.auth()), 400, "BAD_QUERY")
	assertProblem(t, ts.do(t, "GET", "/entries?a=1&b=2&c=3&d=4&e=5&f=6&g=7&h=8&i=9", "", ts.auth()), 400, "BAD_QUERY")
	assertProblem(t, ts.do(t, "GET", "/entries?state=%zz", "", ts.auth()), 400, "BAD_QUERY")
	assertProblem(t, ts.do(t, "GET", "/entries?state=inbox;x=1", "", ts.auth()), 400, "BAD_QUERY")
}

func TestPostEntriesContentType(t *testing.T) {
	ts := startServer(t)
	body := validBody(t)
	for _, ct := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data",
		"application/json; charset=latin1", "application/jsonx", "application/vnd.api+json", "text/json"} {
		headers := map[string]string{"Authorization": "Bearer " + testToken}
		if ct != "" {
			headers["Content-Type"] = ct
		}
		assertProblem(t, ts.do(t, "POST", "/entries", body, headers), 415, "UNSUPPORTED_MEDIA_TYPE")
	}
	for _, ct := range []string{"application/json", "application/json; charset=utf-8", "Application/JSON; charset=UTF8"} {
		res := ts.do(t, "POST", "/entries", body, map[string]string{"Authorization": "Bearer " + testToken, "Content-Type": ct})
		if res.status != 201 {
			t.Fatalf("Content-Type %q: %d %s", ct, res.status, res.body)
		}
	}
	// Two Content-Type headers are none.
	res := ts.raw(t, "POST /entries HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\nAuthorization: Bearer "+testToken+
		"\r\nContent-Type: application/json\r\nContent-Type: text/plain\r\nContent-Length: "+strconv.Itoa(len(body))+
		"\r\n\r\n"+body)
	assertProblem(t, res, 415, "UNSUPPORTED_MEDIA_TYPE")
	assertProblem(t, ts.do(t, "POST", "/entries?x=1", body, ts.authJSON()), 400, "BAD_QUERY")
}

func TestPostEntriesStoresServerOwnedFields(t *testing.T) {
	ts := startServer(t)
	sent := map[string]any{
		"schema_version": "1", "id": "NOTAULID", "kind": "idea", "repo_code": nil,
		"title": "  Trim me  ", "body": "b", "session_id": "s", "agent": "codex",
		"created_at": "1999-01-01T00:00:00Z", "state": "landed",
		"exported_pr": "https://github.com/example-org/example-repo/pull/1", "provenance": "cli",
	}
	raw, _ := json.Marshal(sent)
	res := ts.do(t, "POST", "/entries", string(raw), ts.authJSON())
	if res.status != 201 {
		t.Fatalf("POST = %d %s", res.status, res.body)
	}
	assertSecurityHeaders(t, res)
	var stored Entry
	dec := json.NewDecoder(bytes.NewReader(res.body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&stored); err != nil {
		t.Fatalf("201 body: %v: %s", err, res.body)
	}
	if errs := stored.Validate(); errs != nil {
		t.Fatalf("stored row fails the contract: %v", errs)
	}
	if !ulidPattern.MatchString(stored.ID) || stored.ID == "NOTAULID" {
		t.Fatalf("id %q is not the server's", stored.ID)
	}
	if stored.State != "inbox" || stored.ExportedPR != nil || stored.Provenance != "api" ||
		strings.HasPrefix(stored.CreatedAt, "1999") || stored.Title != "Trim me" || stored.RepoCode != nil {
		t.Fatalf("server-owned fields not overwritten: %+v", stored)
	}
	list := ts.do(t, "GET", "/entries?state=inbox", "", ts.auth())
	var got struct{ Entries []Entry }
	if err := json.Unmarshal(list.body, &got); err != nil || len(got.Entries) != 1 || !reflect.DeepEqual(got.Entries[0], stored) {
		t.Fatalf("GET /entries?state=inbox = %d %s (%v)", list.status, list.body, err)
	}
	empty := ts.do(t, "GET", "/entries?state=landed", "", ts.auth())
	if strings.TrimSpace(string(empty.body)) != `{"entries":[]}` {
		t.Fatalf("empty list = %s", empty.body)
	}
	assertProblem(t, ts.do(t, "GET", "/entries?state=done", "", ts.auth()), 400, "BAD_QUERY")
	assertProblem(t, ts.do(t, "GET", "/entries?kind=idea", "", ts.auth()), 400, "BAD_QUERY")
}

// The contract vectors against POST /entries. The server owns id,
// created_at, state, exported_pr and provenance, so a vector whose only
// defect is in one of those is stored (as an inbox row) and the client
// field defects are 422.
func TestPostEntriesVectors(t *testing.T) {
	ts := startServer(t)
	want := map[string]int{
		"valid-inbox-decision":               201,
		"valid-landed-task-state":            201,
		"invalid-inbox-with-exported-pr":     201,
		"invalid-landed-without-exported-pr": 201,
		"invalid-missing-provenance":         201,
		"invalid-repo-named-not-coded":       422,
	}
	for _, path := range vectorFiles(t, "entry") {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			res := ts.do(t, "POST", "/entries", string(data), ts.authJSON())
			if res.status != want[name] {
				t.Fatalf("status %d, want %d: %s", res.status, want[name], res.body)
			}
			if res.status == 201 {
				var stored Entry
				if err := json.Unmarshal(res.body, &stored); err != nil {
					t.Fatal(err)
				}
				if errs := stored.Validate(); errs != nil || stored.State != "inbox" || stored.Provenance != "api" {
					t.Fatalf("stored row: %+v (%v)", stored, errs)
				}
				return
			}
			doc := assertProblem(t, res, 422, "INVALID_ENTRY")
			errs, _ := doc["errors"].([]any)
			if len(errs) != 1 || errs[0].(map[string]any)["field"] != "repo_code" {
				t.Fatalf("errors = %v", doc["errors"])
			}
		})
	}
}

func TestPostEntriesBodyChecks(t *testing.T) {
	ts := startServer(t)
	cases := []struct {
		name   string
		body   string
		status int
		code   string
		field  string
	}{
		{"empty", "", 400, "BAD_JSON", ""},
		{"not json", "{kind:", 400, "BAD_JSON", ""},
		{"bad utf-8", "{\"kind\":\"\xff\"}", 400, "BAD_JSON", ""},
		{"trailing garbage", `{"kind":"idea"} x`, 400, "BAD_JSON", ""},
		{"array", `[]`, 422, "INVALID_ENTRY", ""},
		{"string", `"x"`, 422, "INVALID_ENTRY", ""},
		{"unknown field", `{"kind":"idea","title":"t","session_id":"s","agent":"a","extra":1}`, 422, "INVALID_ENTRY", "extra"},
		{"kind number", `{"kind":1,"title":"t","session_id":"s","agent":"a"}`, 422, "INVALID_ENTRY", "kind"},
		{"title missing", `{"kind":"idea","session_id":"s","agent":"a"}`, 422, "INVALID_ENTRY", "title"},
		{"title null", `{"kind":"idea","title":null,"session_id":"s","agent":"a"}`, 422, "INVALID_ENTRY", "title"},
		{"title blank", `{"kind":"idea","title":"   ","session_id":"s","agent":"a"}`, 422, "INVALID_ENTRY", "title"},
		{"title control", "{\"kind\":\"idea\",\"title\":\"a\\tb\",\"session_id\":\"s\",\"agent\":\"a\"}", 422, "INVALID_ENTRY", "title"},
		{"body number", `{"kind":"idea","title":"t","body":2,"session_id":"s","agent":"a"}`, 422, "INVALID_ENTRY", "body"},
		{"repo_code number", `{"kind":"idea","title":"t","repo_code":765,"session_id":"s","agent":"a"}`, 422, "INVALID_ENTRY", "repo_code"},
		{"repo_code name", `{"kind":"idea","title":"t","repo_code":"agentvault","session_id":"s","agent":"a"}`, 422, "INVALID_ENTRY", "repo_code"},
		{"agent bad", `{"kind":"idea","title":"t","session_id":"s","agent":"Agent One"}`, 422, "INVALID_ENTRY", "agent"},
		{"kind bad", `{"kind":"todo","title":"t","session_id":"s","agent":"a"}`, 422, "INVALID_ENTRY", "kind"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := ts.do(t, "POST", "/entries", tc.body, ts.authJSON())
			doc := assertProblem(t, res, tc.status, tc.code)
			if tc.field == "" {
				return
			}
			errs, _ := doc["errors"].([]any)
			found := false
			for _, e := range errs {
				if e.(map[string]any)["field"] == tc.field {
					found = true
				}
			}
			if !found {
				t.Fatalf("no error on %s: %v", tc.field, doc["errors"])
			}
		})
	}
	// Minimal body: body and repo_code are optional.
	res := ts.do(t, "POST", "/entries", `{"kind":"note","title":"t","session_id":"s","agent":"a"}`, ts.authJSON())
	if res.status != 201 || !strings.Contains(string(res.body), `"body":""`) || !strings.Contains(string(res.body), `"repo_code":null`) {
		t.Fatalf("minimal POST = %d %s", res.status, res.body)
	}
	// Nothing stored by the refused bodies.
	list := ts.do(t, "GET", "/entries", "", ts.auth())
	if strings.Count(string(list.body), `"id"`) != 1 {
		t.Fatalf("refused bodies left rows: %s", list.body)
	}
}

func TestPostEntriesBodySize(t *testing.T) {
	ts := startServer(t)
	huge := `{"kind":"note","title":"t","session_id":"s","agent":"a","body":"` + strings.Repeat("x", MaxBody) + `"}`
	assertProblem(t, ts.do(t, "POST", "/entries", huge, ts.authJSON()), 413, "BODY_TOO_LARGE")
	// Content-Length claims less than the cap, the connection carries more: still refused.
	lied := ts.raw(t, "POST /entries HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\nAuthorization: Bearer "+testToken+
		"\r\nContent-Type: application/json\r\nContent-Length: 10\r\n\r\n"+huge)
	if lied.status == 201 {
		t.Fatal("a body cut at Content-Length was stored")
	}
	// Chunked: no Content-Length, refused.
	chunked := ts.raw(t, "POST /entries HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\nAuthorization: Bearer "+testToken+
		"\r\nContent-Type: application/json\r\nTransfer-Encoding: chunked\r\n\r\n2\r\n{}\r\n0\r\n\r\n")
	assertProblem(t, chunked, 411, "LENGTH_REQUIRED")
	// A body of exactly the cap with a 200-character title is fine.
	fits := `{"kind":"note","title":"t","session_id":"s","agent":"a","body":"` + strings.Repeat("x", MaxBody-70) + `"}`
	if len(fits) > MaxBody {
		t.Fatalf("test body is %d bytes", len(fits))
	}
	if res := ts.do(t, "POST", "/entries", fits, ts.authJSON()); res.status != 201 {
		t.Fatalf("body under the cap refused: %d %s", res.status, res.body)
	}
	// An oversized header block is refused by the server before any handler.
	big := ts.raw(t, "GET /healthz HTTP/1.1\r\nHost: "+ts.hostHeader()+"\r\nX-Pad: "+strings.Repeat("p", 20<<10)+"\r\n\r\n")
	if big.status != 431 {
		t.Fatalf("20 KiB header: %d", big.status)
	}
}

func TestSearch(t *testing.T) {
	ts := startServer(t)
	res := ts.do(t, "POST", "/entries", `{"kind":"decision","title":"Rebinding guard","body":"The Host gate stops DNS rebinding.","session_id":"s","agent":"a"}`, ts.authJSON())
	if res.status != 201 {
		t.Fatal(string(res.body))
	}
	assertProblem(t, ts.do(t, "GET", "/search?q=rebinding", "", nil), 401, "UNAUTHORIZED")
	assertProblem(t, ts.do(t, "GET", "/search", "", ts.auth()), 400, "BAD_QUERY")
	assertProblem(t, ts.do(t, "GET", "/search?q=x&state=inbox", "", ts.auth()), 400, "BAD_QUERY")
	assertProblem(t, ts.do(t, "GET", "/search?q="+strings.Repeat("a", MaxQuery+1), "", ts.auth()), 400, "BAD_QUERY")
	for _, q := range []string{"rebinding", "REBINDING%20guard", "rebinding%22%20OR%20%22x", "dns%20rebinding"} {
		res = ts.do(t, "GET", "/search?q="+q, "", ts.auth())
		if res.status != 200 {
			t.Fatalf("q=%s: %d %s", q, res.status, res.body)
		}
		var got struct {
			Query string `json:"query"`
			Hits  []Hit  `json:"hits"`
		}
		if err := json.Unmarshal(res.body, &got); err != nil {
			t.Fatal(err)
		}
		if got.Query == "" {
			t.Fatalf("query echo missing: %s", res.body)
		}
		if q == "rebinding%22%20OR%20%22x" {
			if len(got.Hits) != 0 {
				t.Fatalf("FTS syntax reached the parser: %s", res.body)
			}
			continue
		}
		if len(got.Hits) != 1 || got.Hits[0].Source != "entry" || got.Hits[0].Entry == nil ||
			!strings.Contains(strings.ToLower(got.Hits[0].Snippet), "[rebinding]") {
			t.Fatalf("q=%s hits = %s", q, res.body)
		}
		if got.Hits[0].Record != nil {
			t.Fatalf("entry hit carries a record: %s", res.body)
		}
	}
	assertSecurityHeaders(t, res)
	if res.headers.Get("Search-Mode") != "keyword" {
		t.Fatalf("Search-Mode = %q", res.headers.Get("Search-Mode"))
	}
	empty := ts.do(t, "GET", "/search?q=", "", ts.auth())
	if empty.status != 200 || !strings.Contains(string(empty.body), `"hits":[]`) {
		t.Fatalf("empty q = %d %s", empty.status, empty.body)
	}
	// The log line names the path, never the query text.
	if logs := ts.logs.String(); strings.Contains(logs, "rebinding") || strings.Contains(logs, "?") {
		t.Fatalf("log carries the query: %s", logs)
	}
}

func TestDatabaseErrorLeaksNothing(t *testing.T) {
	ts := startServer(t)
	ts.store.Close()
	res := ts.do(t, "GET", "/entries", "", ts.auth())
	doc := assertProblem(t, res, 503, "DATABASE_UNAVAILABLE")
	text := strings.ToLower(fmt.Sprint(doc))
	for _, word := range []string{"sql", "sqlite", "closed", ts.store.Path()} {
		if strings.Contains(text, strings.ToLower(word)) {
			t.Fatalf("503 body leaks %q: %s", word, res.body)
		}
	}
	if !strings.Contains(ts.logs.String(), "database error") {
		t.Fatalf("database error not logged: %s", ts.logs.String())
	}
	post := ts.do(t, "POST", "/entries", validBody(t), ts.authJSON())
	assertProblem(t, post, 503, "DATABASE_UNAVAILABLE")
}

func TestProblemTypeIsURI(t *testing.T) {
	ts := startServer(t)
	doc := assertProblem(t, ts.do(t, "GET", "/entries", "", nil), 401, "UNAUTHORIZED")
	if typ := doc["type"].(string); !strings.HasPrefix(typ, "urn:") || strings.ContainsAny(typ, " _") {
		t.Fatalf("type %q is not a URI", typ)
	}
	if doc["instance"] != "/entries" {
		t.Fatalf("instance %v", doc["instance"])
	}
}
