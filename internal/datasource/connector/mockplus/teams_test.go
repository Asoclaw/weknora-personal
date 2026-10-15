package mockplus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestTeamDiscoverySingleMultipleAndFallback(t *testing.T) {
	for _, scenario := range []struct {
		name  string
		teams string
		fail  bool
		want  string
		count int
	}{
		{"single", `{"teams":[{"_id":"team","name":"Team"}]}`, false, "team", 1},
		{"multiple", `{"teams":[{"_id":"one","name":"One"},{"_id":"two","name":"Two"}]}`, false, "", 2},
		{"unavailable", `{}`, true, "", 0},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := setup(t)
			original := f.server.Config.Handler
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/team" {
					if scenario.fail {
						w.WriteHeader(500)
						return
					}
					_, _ = w.Write([]byte(`{"code":0,"payload":` + scenario.teams + `}`))
					return
				}
				original.ServeHTTP(w, r)
			}))
			defer server.Close()
			f.config.Credentials = map[string]interface{}{"username": "page", "password": "page-password"}
			f.config.Settings["base_url"] = server.URL
			f.config.Settings["team_id"] = ""
			preview, err := f.connector.PreviewConnection(context.Background(), f.config)
			if err != nil || preview.TeamID != scenario.want || len(preview.Teams) != scenario.count {
				t.Fatalf("preview=%+v err=%v", preview, err)
			}
			if scenario.fail && preview.TeamDiscoveryError == "" {
				t.Fatal("missing fallback")
			}
			if scenario.count == 1 && len(preview.Resources) != 3 {
				t.Fatal("single team should load projects")
			}
			if scenario.count > 1 && len(preview.Resources) != 0 {
				t.Fatal("must not choose the first team")
			}
			if f.config.Settings["team_id"] != "" {
				t.Fatal("preview must not mutate saved config")
			}
		})
	}
}
func TestTeamParserDeduplicatesMemberships(t *testing.T) {
	teams := parseTeams(json.RawMessage(`{"teams":[{"_id":"a","name":"One"}],"guestTeams":[{"id":"a","name":"One"},{"id":"b","teamName":"Two"}],"user":{"id":"not-a-team","name":"User"}}`))
	if len(teams) != 2 {
		t.Fatalf("teams=%+v", teams)
	}
}
