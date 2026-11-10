package memory

// Descriptor is the memory.api@1 descriptor: what this server publishes
// about itself (`agentvault memory serve --describe`) and what its tests
// assert. The security fields are the contract's constants. Contracts are
// named as name@version here, not by the schema files' $id URIs.
type Descriptor struct {
	SchemaVersion   string            `json:"schema_version"`
	Bind            string            `json:"bind"`
	Port            int               `json:"port"`
	AllowedHosts    []string          `json:"allowed_hosts"`
	CORS            bool              `json:"cors"`
	PostContentType string            `json:"post_content_type"`
	OriginPolicy    string            `json:"origin_policy"`
	Auth            DescriptorAuth    `json:"auth"`
	Views           DescriptorViews   `json:"views"`
	Routes          []DescriptorRoute `json:"routes"`
	ErrorBody       DescriptorBody    `json:"error_body"`
}

// DescriptorAuth is the auth block: bearer token and the browser cookie
// rules. This server sets no cookie (it renders no view); the block holds
// the contract's constants so a client that reads it sees the one policy.
type DescriptorAuth struct {
	Bearer DescriptorBearer `json:"bearer"`
	Cookie DescriptorCookie `json:"cookie"`
}

// DescriptorBearer names the credential header and where the token lives.
type DescriptorBearer struct {
	Header    string `json:"header"`
	Scheme    string `json:"scheme"`
	TokenDir  string `json:"token_dir"`
	TokenMode string `json:"token_mode"`
}

// DescriptorCookie is the contract's cookie policy.
type DescriptorCookie struct {
	HTTPOnly             bool   `json:"http_only"`
	SameSite             string `json:"same_site"`
	SetBy                string `json:"set_by"`
	OneTimeURLTTLSeconds int    `json:"one_time_url_ttl_seconds"`
}

// DescriptorViews lists the HTML views; this server has none.
type DescriptorViews struct {
	Auth  string   `json:"auth"`
	Paths []string `json:"paths"`
}

// DescriptorRoute is one of the four routes.
type DescriptorRoute struct {
	Method       string          `json:"method"`
	Path         string          `json:"path"`
	Auth         string          `json:"auth"`
	ReturnsData  bool            `json:"returns_data"`
	Query        []string        `json:"query"`
	RequestBody  *DescriptorBody `json:"request_body"`
	ResponseBody *DescriptorBody `json:"response_body"`
}

// DescriptorBody names a body's contract and gives one example of it.
type DescriptorBody struct {
	Schema  string `json:"schema"`
	Example any    `json:"example"`
}

// DescriptorDataDir is where the token file lives, as the descriptor states it.
const DescriptorDataDir = "~/.local/share/agentvault"

// Describe returns the descriptor for a server on port.
func Describe(port int) Descriptor {
	repo := "R-765"
	entry := Entry{
		SchemaVersion: SchemaVersion, ID: "01JCA4N7Q8ZKX2VD5HTW3BRM9E", Kind: "decision", RepoCode: &repo,
		Title:     "Transcripts never leave the machine",
		Body:      "The transcripts table is filled by the importer and read by search only.",
		SessionID: "6b1f0c2e-3d4a-4f5b-9c6d-7e8f9a0b1c2d", Agent: "claude",
		CreatedAt: "2026-11-09T14:02:11Z", State: "inbox", ExportedPR: nil, Provenance: "api",
	}
	text := "Keep the transcripts table local only."
	offset := int64(18432)
	record := Record{
		SchemaVersion: SchemaVersion, SessionID: "6b1f0c2e-3d4a-4f5b-9c6d-7e8f9a0b1c2d", Agent: "claude",
		TS: "2026-11-09T14:01:58Z", Role: "user", Text: &text,
		Source: Source{Path: "/home/user/.claude/projects/-home-user-work/6b1f0c2e.jsonl", Offset: &offset},
	}
	return Descriptor{
		SchemaVersion:   SchemaVersion,
		Bind:            BindAddress,
		Port:            port,
		AllowedHosts:    AllowedHosts(port),
		CORS:            false,
		PostContentType: "application/json",
		OriginPolicy:    "same-origin-only",
		Auth: DescriptorAuth{
			Bearer: DescriptorBearer{Header: "Authorization", Scheme: "Bearer", TokenDir: DescriptorDataDir, TokenMode: "0600"},
			Cookie: DescriptorCookie{HTTPOnly: true, SameSite: "Strict", SetBy: "one-time-url", OneTimeURLTTLSeconds: 300},
		},
		Views: DescriptorViews{Auth: "token-or-cookie", Paths: []string{}},
		Routes: []DescriptorRoute{
			{Method: "POST", Path: "/entries", Auth: "token-or-cookie", ReturnsData: true, Query: []string{},
				RequestBody:  &DescriptorBody{Schema: "memory.entry@1", Example: entry},
				ResponseBody: &DescriptorBody{Schema: "memory.entry@1", Example: entry}},
			{Method: "GET", Path: "/entries", Auth: "token-or-cookie", ReturnsData: true, Query: []string{"state"},
				ResponseBody: &DescriptorBody{Schema: "memory.api@1#/$defs/entry_list",
					Example: map[string]any{"entries": []Entry{entry}}}},
			{Method: "GET", Path: "/search", Auth: "token-or-cookie", ReturnsData: true, Query: []string{"q"},
				ResponseBody: &DescriptorBody{Schema: "memory.api@1#/$defs/search_result",
					Example: map[string]any{"query": "transcripts", "hits": []Hit{
						{Source: "entry", Rank: -2.1, Entry: &entry, Snippet: "The [transcripts] table is filled by the importer"},
						{Source: "transcript", Rank: -1.4, Record: &record, Snippet: "Keep the [transcripts] table local only."},
					}}}},
			{Method: "GET", Path: "/healthz", Auth: "none", ReturnsData: false, Query: []string{}},
		},
		ErrorBody: DescriptorBody{Schema: "error.problem@1", Example: problem{
			Type: problemType + "unauthorized", Title: "Unauthorized", Status: 401,
			Detail: "Send the bearer token.", Instance: "/entries", Code: "UNAUTHORIZED",
		}},
	}
}
