package tool

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func def(method, url string, headers map[string]string, body, schema string) Definition {
	return Definition{Name: "t", Description: "d", Method: method, URLTemplate: url, Headers: headers,
		BodyTemplate: body, ParamsSchema: json.RawMessage(schema)}
}

const ownerRepo = `{"type":"object","properties":{"owner":{"type":"string"},"repo":{"type":"string"},"path":{"type":"string"},
	"title":{"type":"string"},"body":{"type":"string"},"q":{"type":"string"},"n":{"type":"integer"}}}`

// The three example-agent tools (D100, D106, D110) are valid templates.
func TestExampleToolsValidate(t *testing.T) {
	tools := []Definition{
		def("GET", "https://api.github.com/repos/{{owner}}/{{repo}}/contents/{{path}}",
			map[string]string{"Accept": "application/vnd.github.raw+json", "X-GitHub-Api-Version": "2022-11-28"}, "", ownerRepo),
		def("POST", "https://api.osv.dev/v1/query", nil,
			`{"package":{"name":"{{title}}","ecosystem":"npm"},"version":"{{body}}"}`, ownerRepo),
		def("POST", "https://api.github.com/repos/{{owner}}/{{repo}}/issues",
			map[string]string{"Authorization": "Bearer {{secret.GITHUB_TOKEN}}", "Accept": "application/vnd.github+json"},
			`{"title":"{{title}}","body":"{{body}}"}`, ownerRepo),
	}
	for _, d := range tools {
		if err := Validate(d); err != nil {
			t.Errorf("%s: %v", d.URLTemplate, err)
		}
	}
}

// D27, D54, D81, D93, D94, D106: what a template may not do.
func TestTemplateValidationRejects(t *testing.T) {
	cases := map[string]Definition{
		"http":                  def("GET", "http://api.github.com/x", nil, "", ownerRepo),
		"placeholder in host":   def("GET", "https://{{owner}}.example.com/x", nil, "", ownerRepo),
		"other port":            def("GET", "https://api.github.com:8443/x", nil, "", ownerRepo),
		"userinfo":              def("GET", "https://user@api.github.com/x", nil, "", ownerRepo),
		"partial segment":       def("GET", "https://api.github.com/repos/x-{{repo}}", nil, "", ownerRepo),
		"partial query":         def("GET", "https://api.github.com/x?q=a{{q}}", nil, "", ownerRepo),
		"undefined param":       def("GET", "https://api.github.com/{{nope}}", nil, "", ownerRepo),
		"secret in url":         def("GET", "https://api.github.com/{{secret.T}}", nil, "", ownerRepo),
		"secret in body":        def("POST", "https://api.github.com/x", nil, `{"a":"{{secret.T}}"}`, ownerRepo),
		"partial body string":   def("POST", "https://api.github.com/x", nil, `{"a":"hi {{title}}"}`, ownerRepo),
		"literal authorization": def("GET", "https://api.github.com/x", map[string]string{"Authorization": "Bearer abc"}, "", ownerRepo),
		"param authorization":   def("GET", "https://api.github.com/x", map[string]string{"Authorization": "{{title}}"}, "", ownerRepo),
		"secret not at the end": def("GET", "https://api.github.com/x", map[string]string{"Authorization": "{{secret.T}} x"}, "", ownerRepo),
		"secret plus parameter": def("GET", "https://api.github.com/x", map[string]string{"X-A": "{{title}}{{secret.T}}"}, "", ownerRepo),
		"host header":           def("GET", "https://api.github.com/x", map[string]string{"Host": "evil.com"}, "", ownerRepo),
		"idempotency key":       def("POST", "https://api.github.com/x", map[string]string{"Idempotency-Key": "1"}, "", ownerRepo),
		"method":                def("HEAD", "https://api.github.com/x", nil, "", ownerRepo),
		"non-object schema":     def("GET", "https://api.github.com/x", nil, "", `{"type":"string"}`),
		"body not json":         def("POST", "https://api.github.com/x", nil, `{"a":`, ownerRepo),
		"non-ascii host":        def("GET", "https://ápi.github.com/x", nil, "", ownerRepo),
	}
	for name, d := range cases {
		if err := Validate(d); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want a validation error", name, err)
		}
	}
	// A reference inside the document still works.
	local := def("GET", "https://api.github.com/{{a}}", nil, "",
		`{"type":"object","$defs":{"s":{"type":"string"}},"properties":{"a":{"$ref":"#/$defs/s"}}}`)
	if err := Validate(local); err != nil {
		t.Errorf("local $ref: %v", err)
	}
}

// D93: parameters cannot change the request's structure.
func TestRenderEscapesAndEncodes(t *testing.T) {
	d := def("POST", "https://API.GitHub.com./repos/{{owner}}/{{repo}}/issues?q={{q}}&per_page=5",
		map[string]string{"Authorization": "Bearer {{secret.GITHUB_TOKEN}}", "X-Title": "{{title}}"},
		`{"title":"{{title}}","body":"{{body}}","n":"{{n}}"}`, ownerRepo)
	if err := Validate(d); err != nil {
		t.Fatal(err)
	}
	args, err := ParseArgs(d, `{"owner":"a/../b","repo":"r?x=1","q":"a&b=c","title":"t","body":"line1\n\"q\"}","n":3}`)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Render(d, args)
	if err != nil {
		t.Fatal(err)
	}
	want := "https://api.github.com/repos/a%2F..%2Fb/r%3Fx=1/issues?q=a%26b%3Dc&per_page=5"
	if r.URL != want {
		t.Fatalf("url\n got %s\nwant %s", r.URL, want)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
		t.Fatal(err)
	}
	if body["body"] != "line1\n\"q\"}" || body["n"] != float64(3) {
		t.Fatalf("body %s", r.Body)
	}
	for _, h := range r.Headers {
		if h.Name == "Authorization" && (h.Secret != "GITHUB_TOKEN" || h.Prefix != "Bearer " || h.Value != "") {
			t.Fatalf("secret header rendered with a value: %+v", h)
		}
	}
	if p, _ := json.Marshal(r.Preview()); strings.Contains(string(p), "ghp_") {
		t.Fatal("preview contains a secret")
	}

	bad := []string{`{"owner":"..","repo":"r"}`, `{"owner":"","repo":"r"}`}
	g := def("GET", "https://api.github.com/{{owner}}/{{repo}}", nil, "", ownerRepo)
	for _, a := range bad {
		args, _ := ParseArgs(g, a)
		if _, err := Render(g, args); err == nil {
			t.Errorf("%s: dot or empty path segment accepted", a)
		}
	}
	h := def("GET", "https://api.github.com/x", map[string]string{"X-T": "{{title}}"}, "", ownerRepo)
	args, _ = ParseArgs(h, `{"title":"a\r\nX-Evil: 1"}`)
	if _, err := Render(h, args); err == nil {
		t.Error("CR/LF accepted in a header value")
	}
}

func TestParseArgsErrors(t *testing.T) {
	d := def("GET", "https://api.github.com/{{owner}}", nil, "",
		`{"type":"object","properties":{"owner":{"type":"string"}},"required":["owner"]}`)
	for raw, code := range map[string]string{"{oops": "malformed_json", "[1]": "malformed_json", `{"owner":5}`: "schema_violation", `{}`: "schema_violation"} {
		_, err := ParseArgs(d, raw)
		var ae *ArgError
		if !errors.As(err, &ae) || ae.Code != code {
			t.Errorf("%s: got %v, want %s", raw, err, code)
		}
	}
}

// D81, D94: the address policy.
func TestAddressPolicy(t *testing.T) {
	refused := []string{"127.0.0.1:443", "10.0.0.1:443", "192.168.1.1:443", "169.254.169.254:443", "[::1]:443",
		"[fd00::1]:443", "[::ffff:127.0.0.1]:443", "100.64.0.1:443", "0.0.0.0:443", "[fe80::1]:443", "224.0.0.1:443",
		"8.8.8.8:80"}
	for _, a := range refused {
		if checkAddress(netip.MustParseAddrPort(a)) == nil {
			t.Errorf("%s allowed", a)
		}
	}
	if err := checkAddress(netip.MustParseAddrPort("140.82.112.6:443")); err != nil {
		t.Errorf("public address refused: %v", err)
	}
	// The production client refuses loopback before anything is sent.
	_, err := NewClient().Do(context.Background(), Request{Method: "GET", URL: "https://127.0.0.1/x", Host: "127.0.0.1"}, nil)
	var f *Failure
	if !errors.As(err, &f) || f.Code != "address_refused" || f.MaySent {
		t.Fatalf("loopback: %v", err)
	}
}

// testClient talks to an httptest TLS server while the request keeps its
// logical host:443 (D110). Only tests in this package can build it.
func testClient(t *testing.T, h http.Handler) (*Client, *httptest.Server) {
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	target := srv.Listener.Addr().String()
	c := newClient(func(netip.AddrPort) error { return nil },
		func(string) string { return target },
		&tls.Config{RootCAs: pool, ServerName: "example.com"})
	return c, srv
}

func TestClientSecretsMaskingAndLimits(t *testing.T) {
	const secret = "ghp_super_secret_token"
	var gotAuth string
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/echo":
			// A server that reflects the request's credentials in several forms.
			b64 := base64.StdEncoding.EncodeToString([]byte(secret))
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"auth":"`+gotAuth+`","b64":"`+b64+`"}`)
		case "/redirect":
			http.Redirect(w, r, "https://evil.example/steal", http.StatusFound)
		case "/big":
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, strings.Repeat("é", MaxModelBytes)) // 2 bytes each
		case "/binary":
			w.Header().Set("Content-Type", "image/png")
			w.Write([]byte{0x89, 'P', 'N', 'G'})
		}
	}))
	secrets := map[string]Secret{"GITHUB_TOKEN": {Value: secret, Host: "example.com"}}
	req := func(path string) Request {
		return Request{Method: "GET", URL: "https://example.com" + path, Host: "example.com",
			Headers: []Header{{Name: "Authorization", Prefix: "Bearer ", Secret: "GITHUB_TOKEN"}}}
	}

	res, err := c.Do(context.Background(), req("/echo"), secrets)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+secret {
		t.Fatalf("secret not attached: %q", gotAuth)
	}
	if strings.Contains(res.Text, secret) || strings.Contains(res.Text, base64.StdEncoding.EncodeToString([]byte(secret))) {
		t.Fatalf("secret echoed back: %s", res.Text)
	}

	res, _ = c.Do(context.Background(), req("/redirect"), secrets)
	if res.Status != http.StatusFound || !strings.Contains(res.Text, "Location: https://evil.example/steal") {
		t.Fatalf("redirect: %d %s", res.Status, res.Text)
	}

	res, _ = c.Do(context.Background(), req("/big"), secrets)
	if len(res.Text) > MaxModelBytes+100 || !strings.Contains(res.Text, "[truncated:") {
		t.Fatalf("not truncated: %d bytes", len(res.Text))
	}
	if !strings.HasSuffix(strings.SplitN(res.Text, "\n[truncated", 2)[0], "é") {
		t.Fatal("truncation split a UTF-8 character")
	}

	res, _ = c.Do(context.Background(), req("/binary"), secrets)
	if !strings.Contains(res.Text, "[non-text response: image/png, 4 bytes]") {
		t.Fatalf("binary: %s", res.Text)
	}

	// D32: a secret bound to another host is never attached.
	gotAuth = ""
	_, err = c.Do(context.Background(), req("/echo"), map[string]Secret{"GITHUB_TOKEN": {Value: secret, Host: "api.github.com"}})
	var f *Failure
	if !errors.As(err, &f) || f.Code != "host_mismatch" || gotAuth != "" {
		t.Fatalf("host mismatch: %v (sent auth %q)", err, gotAuth)
	}
	_, err = c.Do(context.Background(), req("/echo"), nil)
	if !errors.As(err, &f) || f.Code != "secret_missing" {
		t.Fatalf("missing secret: %v", err)
	}
}

// D91: GET is retried once on 5xx; POST never.
func TestClientRetries(t *testing.T) {
	hits := map[string]int{}
	c, _ := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.Method]++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	c.Do(context.Background(), Request{Method: "GET", URL: "https://example.com/x", Host: "example.com"}, nil)
	c.Do(context.Background(), Request{Method: "POST", URL: "https://example.com/x", Host: "example.com", Body: "{}"}, nil)
	if hits["GET"] != 2 || hits["POST"] != 1 {
		t.Fatalf("hits %v", hits)
	}
}

func TestNormalizeHost(t *testing.T) {
	for in, want := range map[string]string{"API.GitHub.com": "api.github.com", "api.github.com.": "api.github.com", "api.github.com:443": "api.github.com"} {
		if got, err := NormalizeHost(in); err != nil || got != want {
			t.Errorf("%s: %s %v", in, got, err)
		}
	}
	if _, err := NormalizeHost("a.com:80"); err == nil {
		t.Error("port 80 accepted")
	}
}

var _ = net.IPv4len
