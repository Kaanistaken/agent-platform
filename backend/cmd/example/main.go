// Command example defines the dependency-check example agent through the
// platform's own REST API, exactly as a user would in the UI (EA: defined
// through the platform, not hard-coded). It reads the tool and agent
// definitions from examples/dependency-check/ and skips anything that already
// exists in the user's team.
//
//	cd backend && go run ./cmd/example -user alice
//
// The password is read from SEED_PASSWORD_<USER> in ../.env unless -password
// is given. The platform (Go API and Next.js) must be running.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"strings"

	"github.com/joho/godotenv"
)

var (
	base   = flag.String("url", "http://localhost:3000", "platform URL (the Next.js app)")
	user   = flag.String("user", "alice", "username")
	pass   = flag.String("password", "", "password (default: SEED_PASSWORD_<USER> from ../.env)")
	dir    = flag.String("dir", "../examples/dependency-check", "directory with tools.json and agents.json")
	client *http.Client
)

func main() {
	flag.Parse()
	godotenv.Load("../.env")
	if *pass == "" {
		*pass = os.Getenv("SEED_PASSWORD_" + strings.ToUpper(*user))
	}
	jar, _ := cookiejar.New(nil)
	client = &http.Client{Jar: jar}
	must(call("POST", "/api/login", map[string]string{"username": *user, "password": *pass}, nil))

	var tools []map[string]any
	readJSON("tools.json", &tools)
	var existing []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	must(call("GET", "/api/tools", nil, &existing))
	ids := map[string]int64{}
	for _, t := range existing {
		ids[t.Name] = t.ID
	}
	for _, t := range tools {
		name := t["name"].(string)
		if _, ok := ids[name]; ok {
			fmt.Printf("tool %s exists (id %d)\n", name, ids[name])
			continue
		}
		var out struct{ ID int64 }
		must(call("POST", "/api/tools", t, &out))
		ids[name] = out.ID
		fmt.Printf("created tool %s (id %d)\n", name, out.ID)
	}

	var agents []struct {
		Name         string   `json:"name"`
		Provider     string   `json:"provider"`
		Model        string   `json:"model"`
		SystemPrompt string   `json:"system_prompt"`
		Tools        []string `json:"tools"`
	}
	readJSON("agents.json", &agents)
	var existingAgents []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	must(call("GET", "/api/agents", nil, &existingAgents))
	for _, a := range agents {
		found := int64(0)
		for _, e := range existingAgents {
			if e.Name == a.Name {
				found = e.ID
			}
		}
		if found != 0 {
			fmt.Printf("agent %q exists (id %d)\n", a.Name, found)
			continue
		}
		var toolIDs []int64
		for _, n := range a.Tools {
			toolIDs = append(toolIDs, ids[n])
		}
		var out struct{ ID int64 }
		must(call("POST", "/api/agents", map[string]any{"name": a.Name, "provider": a.Provider, "model": a.Model,
			"system_prompt": a.SystemPrompt, "tool_ids": toolIDs}, &out))
		fmt.Printf("created agent %q (id %d)\n", a.Name, out.ID)
	}
	fmt.Println("done: open", *base, "and run the agent")
}

func call(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, *base+path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", *base) // the API checks Origin on state-changing requests (D30)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, data)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func readJSON(name string, v any) {
	data, err := os.ReadFile(*dir + "/" + name)
	if err != nil {
		log.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		log.Fatalf("%s: %v", name, err)
	}
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
