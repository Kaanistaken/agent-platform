// Package tool turns a tool definition (an HTTP request template, R2) into a
// request, and sends it under the platform's HTTP safety rules.
//
// Template grammar (D81, D93, D106):
//   - URL: `https://<literal host>/<segments>?<query>`. A model parameter
//     `{{name}}` may only be a whole path segment or a whole query value.
//   - Headers: a literal value, a whole `{{name}}`, or a literal prefix
//     followed by exactly one `{{secret.NAME}}` at the end.
//   - Body: JSON; a model parameter may only be a whole JSON string value
//     `"{{name}}"`, replaced by the JSON-encoded parameter value.
package tool

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Definition is a tool as the platform stores it.
type Definition struct {
	Name             string            `json:"name"`
	Description      string            `json:"description"`
	Method           string            `json:"method"`
	URLTemplate      string            `json:"url_template"`
	Headers          map[string]string `json:"headers_template"`
	BodyTemplate     string            `json:"body_template"`
	ParamsSchema     json.RawMessage   `json:"params_schema"`
	RequiresApproval bool              `json:"requires_approval"`
}

var (
	nameRe       = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	paramRe      = regexp.MustCompile(`^\{\{([A-Za-z_][A-Za-z0-9_]*)\}\}$`)
	secretRe     = regexp.MustCompile(`^([^{}\r\n]*)\{\{secret\.([A-Za-z_][A-Za-z0-9_]*)\}\}$`)
	headerNameRe = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// Methods allowed for tools (D81).
var allowedMethods = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// Headers a template may never set (D94): the transport owns them, and Go may
// replay a request carrying Idempotency-Key or X-Idempotency-Key (D63).
var forbiddenHeaders = map[string]bool{
	"Host": true, "Connection": true, "Keep-Alive": true, "Proxy-Connection": true,
	"Transfer-Encoding": true, "Upgrade": true, "Te": true, "Trailer": true,
	"Content-Length": true, "Idempotency-Key": true, "X-Idempotency-Key": true, "Proxy-Authenticate": true,
}

// ForbiddenHeader reports whether a template may not set the header. The
// client checks it again before sending, so a request stored before a rule was
// added is refused too.
func ForbiddenHeader(name string) bool { return forbiddenHeaders[http.CanonicalHeaderKey(name)] }

// Host returns the normalised literal host of a definition's URL template, or
// "" if the template is not valid.
func Host(d Definition) string {
	p, err := parseURLTemplate(d.URLTemplate)
	if err != nil {
		return ""
	}
	return p.host
}

// Headers that must carry a secret reference, never a literal value (D54).
var sensitiveHeaders = map[string]bool{
	"Authorization": true, "Proxy-Authorization": true, "X-Api-Key": true,
	"Api-Key": true, "Cookie": true, "X-Auth-Token": true, "Private-Token": true,
}

// ErrInvalid wraps every validation error so callers can return 400.
var ErrInvalid = errors.New("invalid tool")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// parsedURL is the URL template split into its parts.
type parsedURL struct {
	host     string   // normalised literal host (no port)
	segments []string // path segments, each literal or "{{name}}"
	query    []queryPart
}

type queryPart struct {
	key, value string
	hasValue   bool // "k=v" or "k=" rather than a bare "k"
}

// Validate checks a definition when it is saved.
func Validate(d Definition) error {
	if !nameRe.MatchString(d.Name) {
		return invalid("name must be 1-64 letters, digits, '_' or '-'")
	}
	if strings.TrimSpace(d.Description) == "" {
		return invalid("description is required")
	}
	if !allowedMethods[d.Method] {
		return invalid("method must be one of GET, POST, PUT, PATCH, DELETE")
	}
	_, props, err := compileSchema(d.ParamsSchema)
	if err != nil {
		return err
	}

	used := map[string]bool{}
	pu, err := parseURLTemplate(d.URLTemplate)
	if err != nil {
		return err
	}
	for _, s := range pu.segments {
		if m := paramRe.FindStringSubmatch(s); m != nil {
			used[m[1]] = true
		}
	}
	for _, q := range pu.query {
		if m := paramRe.FindStringSubmatch(q.value); m != nil {
			used[m[1]] = true
		}
	}

	for name, value := range d.Headers {
		canon := http.CanonicalHeaderKey(name)
		if !headerNameRe.MatchString(name) {
			return invalid("header name %q is not valid", name)
		}
		if forbiddenHeaders[canon] {
			return invalid("header %s may not be set by a tool", canon)
		}
		if strings.ContainsAny(value, "\r\n") {
			return invalid("header %s contains a line break", canon)
		}
		switch {
		case strings.Contains(value, "{{secret."):
			if secretRe.FindStringSubmatch(value) == nil {
				return invalid("header %s: a secret must be the last part of the value, as `prefix{{secret.NAME}}`", canon)
			}
		case strings.Contains(value, "{{"):
			m := paramRe.FindStringSubmatch(value)
			if m == nil {
				return invalid("header %s: a parameter must be the whole value", canon)
			}
			if sensitiveHeaders[canon] {
				return invalid("header %s must use a secret reference", canon)
			}
			used[m[1]] = true
		default:
			if sensitiveHeaders[canon] {
				return invalid("header %s must use a secret reference, not a literal value", canon)
			}
		}
	}

	if strings.TrimSpace(d.BodyTemplate) != "" {
		var body any
		if err := json.Unmarshal([]byte(d.BodyTemplate), &body); err != nil {
			return invalid("body template is not valid JSON: %v", err)
		}
		if err := walkBody(body, func(s string) error {
			if strings.Contains(s, "{{secret.") {
				return invalid("secrets are only allowed in headers")
			}
			if !strings.Contains(s, "{{") {
				return nil
			}
			m := paramRe.FindStringSubmatch(s)
			if m == nil {
				return invalid("body: a parameter must be a whole JSON string value, e.g. \"{{name}}\"")
			}
			used[m[1]] = true
			return nil
		}); err != nil {
			return err
		}
	}

	for name := range used {
		if !props[name] {
			return invalid("placeholder {{%s}} is not defined in params_schema", name)
		}
	}
	return nil
}

// parseURLTemplate enforces https, a literal host, port 443, no userinfo and
// placeholders only as whole path segments or query values (D81, D93).
func parseURLTemplate(t string) (parsedURL, error) {
	const scheme = "https://"
	if !strings.HasPrefix(t, scheme) {
		return parsedURL{}, invalid("URL must start with https://")
	}
	if strings.ContainsAny(t, "#\r\n \t") {
		return parsedURL{}, invalid("URL may not contain a fragment or whitespace")
	}
	rest := t[len(scheme):]
	end := strings.IndexAny(rest, "/?")
	if end < 0 {
		end = len(rest)
	}
	authority := rest[:end]
	if strings.Contains(authority, "{{") {
		return parsedURL{}, invalid("the host may not contain a placeholder")
	}
	if strings.Contains(authority, "@") {
		return parsedURL{}, invalid("URL may not contain user information")
	}
	host, err := NormalizeHost(authority)
	if err != nil {
		return parsedURL{}, err
	}
	pu := parsedURL{host: host}

	rest = rest[end:]
	path, query, _ := strings.Cut(rest, "?")
	if path != "" {
		for _, seg := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
			if strings.Contains(seg, "{{") && !paramRe.MatchString(seg) {
				return parsedURL{}, invalid("a URL parameter must be a whole path segment")
			}
			pu.segments = append(pu.segments, seg)
		}
	}
	if query != "" {
		for _, pair := range strings.Split(query, "&") {
			k, v, hasValue := strings.Cut(pair, "=")
			if strings.Contains(k, "{{") {
				return parsedURL{}, invalid("a query parameter name may not be a placeholder")
			}
			if strings.Contains(v, "{{") && !paramRe.MatchString(v) {
				return parsedURL{}, invalid("a query parameter must be a whole query value")
			}
			pu.query = append(pu.query, queryPart{k, v, hasValue})
		}
	}
	return pu, nil
}

// NormalizeHost lowercases a host, drops a trailing dot and the default port,
// and rejects any other port and non-ASCII hosts (D81, D94). It is used for
// tool URLs and for the allowed host of a secret, so both compare equal.
func NormalizeHost(authority string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(authority))
	if h == "" {
		return "", invalid("host is empty")
	}
	for _, r := range h {
		if r > 127 {
			return "", invalid("host must be ASCII (use the punycode form)")
		}
	}
	if strings.ContainsAny(h, "@/?#{} ") {
		return "", invalid("host %q is not valid", authority)
	}
	if i := strings.LastIndex(h, ":"); i >= 0 && !strings.HasSuffix(h, "]") {
		port := h[i+1:]
		h = h[:i]
		if port != "443" {
			return "", invalid("only port 443 is allowed")
		}
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "", invalid("host is empty")
	}
	if u, err := url.Parse("https://" + h); err != nil || u.Hostname() != h {
		return "", invalid("host %q is not valid", authority)
	}
	return h, nil
}

func compileSchema(raw json.RawMessage) (*jsonschema.Schema, map[string]bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, invalid("params_schema is not valid JSON: %v", err)
	}
	obj, ok := doc.(map[string]any)
	if !ok || obj["type"] != "object" {
		return nil, nil, invalid(`params_schema must be a JSON schema with "type": "object"`)
	}
	c := jsonschema.NewCompiler()
	// The library's default loader reads local files for "$ref"; a tool
	// owner must not be able to make the server load files or URLs (D11).
	c.UseLoader(refuseLoader{})
	if err := c.AddResource("params.json", doc); err != nil {
		return nil, nil, invalid("params_schema: %v", err)
	}
	s, err := c.Compile("params.json")
	if err != nil {
		return nil, nil, invalid("params_schema: %v", err)
	}
	props := map[string]bool{}
	if p, ok := obj["properties"].(map[string]any); ok {
		for k := range p {
			props[k] = true
		}
	}
	return s, props, nil
}

// refuseLoader refuses every external schema reference. References inside the
// document ("#/...") and the built-in meta-schemas still work.
type refuseLoader struct{}

func (refuseLoader) Load(string) (any, error) {
	return nil, errors.New("external schema references are not allowed")
}

func walkBody(v any, fn func(string) error) error {
	switch t := v.(type) {
	case string:
		return fn(t)
	case []any:
		for _, e := range t {
			if err := walkBody(e, fn); err != nil {
				return err
			}
		}
	case map[string]any:
		for k, e := range t {
			if strings.Contains(k, "{{") {
				return invalid("a JSON object key may not be a placeholder")
			}
			if err := walkBody(e, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// ArgError is a problem with the arguments the model sent. It goes back to
// the model (R3).
type ArgError struct {
	Code string // malformed_json or schema_violation
	Msg  string
}

func (e *ArgError) Error() string { return e.Msg }

// ParseArgs parses the model's raw argument text and validates it against the
// tool's parameter schema.
func ParseArgs(d Definition, raw string) (map[string]any, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		return nil, &ArgError{"malformed_json", "arguments are not valid JSON: " + err.Error()}
	}
	args, ok := doc.(map[string]any)
	if !ok {
		return nil, &ArgError{"malformed_json", "arguments must be a JSON object"}
	}
	schema, _, err := compileSchema(d.ParamsSchema)
	if err != nil {
		return nil, err
	}
	if err := schema.Validate(doc); err != nil {
		return nil, &ArgError{"schema_violation", "arguments do not match the tool's schema: " + err.Error()}
	}
	return args, nil
}

// Header is one rendered request header. If Secret is set, the header value
// is Prefix + the secret's value, attached only at send time (D33).
type Header struct {
	Name   string `json:"name"`
	Value  string `json:"value,omitempty"`
	Secret string `json:"secret,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

// Request is a fully rendered request without any secret value in it. It is
// what the approval preview, events and records show.
type Request struct {
	Method  string   `json:"method"`
	URL     string   `json:"url"`
	Host    string   `json:"host"`
	Headers []Header `json:"headers"`
	Body    string   `json:"body,omitempty"`
}

// Preview returns a secret-free, human-readable form of the request.
func (r Request) Preview() map[string]any {
	headers := map[string]string{}
	for _, h := range r.Headers {
		if h.Secret != "" {
			headers[h.Name] = h.Prefix + "***{{secret." + h.Secret + "}}"
		} else {
			headers[h.Name] = h.Value
		}
	}
	p := map[string]any{"method": r.Method, "url": r.URL, "headers": headers}
	if r.Body != "" {
		var body any
		if json.Unmarshal([]byte(r.Body), &body) == nil {
			p["body"] = body
		} else {
			p["body"] = r.Body
		}
	}
	return p
}

// SecretNames lists the secret names the request needs.
func (r Request) SecretNames() []string {
	var names []string
	for _, h := range r.Headers {
		if h.Secret != "" {
			names = append(names, h.Secret)
		}
	}
	return names
}

// SecretRefs lists the secret names a definition references.
func SecretRefs(d Definition) []string {
	var names []string
	for _, v := range d.Headers {
		if m := secretRe.FindStringSubmatch(v); m != nil {
			names = append(names, m[2])
		}
	}
	return names
}

// Render builds the secret-free request from a validated definition and
// validated arguments.
func Render(d Definition, args map[string]any) (Request, error) {
	pu, err := parseURLTemplate(d.URLTemplate)
	if err != nil {
		return Request{}, err
	}
	var b strings.Builder
	b.WriteString("https://" + pu.host)
	for _, seg := range pu.segments {
		b.WriteString("/")
		if m := paramRe.FindStringSubmatch(seg); m != nil {
			v, err := scalar(args, m[1])
			if err != nil {
				return Request{}, err
			}
			if v == "" || v == "." || v == ".." {
				return Request{}, &ArgError{"schema_violation", fmt.Sprintf("parameter %s may not be empty, '.' or '..' in a URL path", m[1])}
			}
			b.WriteString(url.PathEscape(v))
		} else {
			b.WriteString(seg)
		}
	}
	for i, q := range pu.query {
		if i == 0 {
			b.WriteString("?")
		} else {
			b.WriteString("&")
		}
		b.WriteString(q.key)
		if m := paramRe.FindStringSubmatch(q.value); m != nil {
			v, err := scalar(args, m[1])
			if err != nil {
				return Request{}, err
			}
			b.WriteString("=" + url.QueryEscape(v))
		} else if q.hasValue {
			b.WriteString("=" + q.value)
		}
	}
	req := Request{Method: d.Method, URL: b.String(), Host: pu.host}

	names := make([]string, 0, len(d.Headers))
	for n := range d.Headers {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := d.Headers[n]
		canon := http.CanonicalHeaderKey(n)
		if m := secretRe.FindStringSubmatch(v); m != nil {
			req.Headers = append(req.Headers, Header{Name: canon, Prefix: m[1], Secret: m[2]})
			continue
		}
		if m := paramRe.FindStringSubmatch(v); m != nil {
			s, err := scalar(args, m[1])
			if err != nil {
				return Request{}, err
			}
			if strings.ContainsAny(s, "\r\n") {
				return Request{}, &ArgError{"schema_violation", "header values may not contain line breaks"}
			}
			v = s
		}
		req.Headers = append(req.Headers, Header{Name: canon, Value: v})
	}

	if strings.TrimSpace(d.BodyTemplate) != "" {
		var body any
		if err := json.Unmarshal([]byte(d.BodyTemplate), &body); err != nil {
			return Request{}, err
		}
		body, err = fillBody(body, args)
		if err != nil {
			return Request{}, err
		}
		out, err := json.Marshal(body)
		if err != nil {
			return Request{}, err
		}
		req.Body = string(out)
		if !hasHeader(req.Headers, "Content-Type") {
			req.Headers = append(req.Headers, Header{Name: "Content-Type", Value: "application/json"})
		}
	}
	return req, nil
}

// fillBody replaces whole-string placeholders with the parameter value. The
// value is inserted as a JSON value, never spliced into JSON text, so quotes
// and line breaks cannot change the body's structure (D93).
func fillBody(v any, args map[string]any) (any, error) {
	switch t := v.(type) {
	case string:
		if m := paramRe.FindStringSubmatch(t); m != nil {
			val, ok := args[m[1]]
			if !ok {
				return nil, &ArgError{"schema_violation", "missing parameter " + m[1]}
			}
			return val, nil
		}
		return t, nil
	case []any:
		for i, e := range t {
			f, err := fillBody(e, args)
			if err != nil {
				return nil, err
			}
			t[i] = f
		}
		return t, nil
	case map[string]any:
		for k, e := range t {
			f, err := fillBody(e, args)
			if err != nil {
				return nil, err
			}
			t[k] = f
		}
		return t, nil
	}
	return v, nil
}

func scalar(args map[string]any, name string) (string, error) {
	v, ok := args[name]
	if !ok {
		return "", &ArgError{"schema_violation", "missing parameter " + name}
	}
	switch t := v.(type) {
	case string:
		return t, nil
	case bool:
		return strconv.FormatBool(t), nil
	case json.Number:
		return t.String(), nil
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64), nil
	default:
		// jsonschema.UnmarshalJSON decodes numbers as json.Number; other types
		// (objects, arrays, null) cannot go into a URL or header.
		return "", &ArgError{"schema_violation", fmt.Sprintf("parameter %s must be a string, number or boolean", name)}
	}
}

func hasHeader(hs []Header, name string) bool {
	for _, h := range hs {
		if h.Name == name {
			return true
		}
	}
	return false
}
