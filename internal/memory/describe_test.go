package memory

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The descriptor holds the memory.api@1 constants, the checks the contract
// writes as consts and allOf rules, for the port it is made for.
func TestDescribeHoldsTheContractConstants(t *testing.T) {
	for _, port := range []int{DefaultPort, 1, 65535} {
		d := Describe(port)
		if d.SchemaVersion != "1" || d.Bind != "127.0.0.1" || d.CORS || d.PostContentType != "application/json" ||
			d.OriginPolicy != "same-origin-only" || d.Port != port {
			t.Fatalf("constants wrong: %+v", d)
		}
		want := []string{"127.0.0.1:" + strconv.Itoa(port), "localhost:" + strconv.Itoa(port)}
		if len(d.AllowedHosts) != 2 || d.AllowedHosts[0] != want[0] || d.AllowedHosts[1] != want[1] {
			t.Fatalf("allowed_hosts %v, want %v", d.AllowedHosts, want)
		}
		hostPattern := regexp.MustCompile(`^(127\.0\.0\.1|localhost):[0-9]{1,5}$`)
		for _, h := range d.AllowedHosts {
			if !hostPattern.MatchString(h) {
				t.Fatalf("allowed host %q", h)
			}
		}
		b := d.Auth.Bearer
		if b.Header != "Authorization" || b.Scheme != "Bearer" || b.TokenMode != "0600" || !strings.HasPrefix(b.TokenDir, "~/") {
			t.Fatalf("bearer block %+v", b)
		}
		c := d.Auth.Cookie
		if !c.HTTPOnly || c.SameSite != "Strict" || c.SetBy != "one-time-url" || c.OneTimeURLTTLSeconds != 300 {
			t.Fatalf("cookie block %+v", c)
		}
		if d.Views.Auth != "token-or-cookie" || d.Views.Paths == nil {
			t.Fatalf("views %+v", d.Views)
		}
		seen := map[string]bool{}
		for _, r := range d.Routes {
			key := r.Method + " " + r.Path
			if seen[key] {
				t.Fatalf("route %s twice", key)
			}
			seen[key] = true
			if r.Query == nil {
				t.Fatalf("%s: query is null, want a list", key)
			}
			for _, q := range r.Query {
				if !regexp.MustCompile(`^[a-z_]+$`).MatchString(q) {
					t.Fatalf("%s: query name %q", key, q)
				}
			}
			if r.Path == "/healthz" {
				if r.Method != "GET" || r.Auth != "none" || r.ReturnsData || len(r.Query) != 0 ||
					r.RequestBody != nil || r.ResponseBody != nil {
					t.Fatalf("healthz route %+v", r)
				}
				continue
			}
			if r.Auth != "token-or-cookie" || !r.ReturnsData || r.ResponseBody == nil {
				t.Fatalf("%s: %+v", key, r)
			}
			switch key {
			case "POST /entries":
				if len(r.Query) != 0 || r.RequestBody == nil || r.RequestBody.Schema != "memory.entry@1" {
					t.Fatalf("%s: %+v", key, r)
				}
				e := r.RequestBody.Example.(Entry)
				if errs := e.Validate(); errs != nil {
					t.Fatalf("%s example fails memory.entry@1: %v", key, errs)
				}
			case "GET /entries":
				if len(r.Query) != 1 || r.Query[0] != "state" || r.RequestBody != nil {
					t.Fatalf("%s: %+v", key, r)
				}
			case "GET /search":
				if len(r.Query) != 1 || r.Query[0] != "q" || r.RequestBody != nil {
					t.Fatalf("%s: %+v", key, r)
				}
				hits := r.ResponseBody.Example.(map[string]any)["hits"].([]Hit)
				for _, h := range hits {
					switch h.Source {
					case "entry":
						if h.Entry == nil || h.Record != nil || h.Entry.Validate() != nil {
							t.Fatalf("entry hit example %+v", h)
						}
					case "transcript":
						if h.Record == nil || h.Entry != nil || h.Record.Validate() != nil {
							t.Fatalf("transcript hit example %+v", h)
						}
					default:
						t.Fatalf("hit source %q", h.Source)
					}
				}
			default:
				t.Fatalf("unexpected route %s", key)
			}
		}
		if len(d.Routes) != 4 || len(seen) != 4 {
			t.Fatalf("routes %v", seen)
		}
		p := d.ErrorBody.Example.(problem)
		if p.Status < 400 || p.Status > 599 || !problemCode.MatchString(p.Code) || p.Title == "" || p.Type == "" {
			t.Fatalf("error example %+v", p)
		}
	}
}

func TestDescribeJSONRoundTrip(t *testing.T) {
	data, err := json.Marshal(Describe(DefaultPort))
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema_version", "bind", "port", "allowed_hosts", "cors", "post_content_type",
		"origin_policy", "auth", "views", "routes", "error_body"} {
		if _, ok := back[key]; !ok {
			t.Fatalf("descriptor lacks %s", key)
		}
	}
	if len(back) != 11 {
		t.Fatalf("descriptor has %d keys, want 11 (no additional properties)", len(back))
	}
	if strings.Contains(string(data), `"query":null`) || strings.Contains(string(data), `"paths":null`) {
		t.Fatalf("null where the contract wants a list: %s", data)
	}
}
