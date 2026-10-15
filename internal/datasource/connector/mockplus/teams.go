package mockplus

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

// The deployed Mockplus frontend uses this membership-scoped endpoint.
// Never fall back to admin/team/list or infer teams from project identifiers.
func (c *Connector) PreviewConnection(ctx context.Context, config *types.DataSourceConfig) (*types.DataSourceConnectionPreview, error) {
	if err := c.Validate(ctx, config); err != nil {
		return nil, err
	}
	cl, err := c.clientWithTeam(config, false)
	if err != nil {
		return nil, err
	}
	var raw json.RawMessage
	preview := &types.DataSourceConnectionPreview{Teams: []types.ConnectorTeam{}, Resources: []types.Resource{}, TeamID: cl.team}
	if err := cl.api(ctx, "/api/v1/team?isNeedGuestTeams=true", &raw); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		preview.TeamDiscoveryError = "自动获取团队失败，请填写团队 ID"
	} else {
		preview.Teams = parseTeams(raw)
		if len(preview.Teams) == 0 {
			preview.TeamDiscoveryError = "未获取到可访问的团队，请填写团队 ID"
		}
	}
	if preview.TeamID == "" && len(preview.Teams) == 1 {
		preview.TeamID = preview.Teams[0].ID
	}
	if preview.TeamID != "" {
		copyConfig := *config
		copyConfig.Settings = make(map[string]interface{}, len(config.Settings)+1)
		for k, v := range config.Settings {
			copyConfig.Settings[k] = v
		}
		copyConfig.Settings["team_id"] = preview.TeamID
		preview.Resources, err = c.ListResources(ctx, &copyConfig, "")
		if err != nil {
			return nil, err
		}
	}
	return preview, nil
}

func parseTeams(raw json.RawMessage) []types.ConnectorTeam {
	seen := map[string]types.ConnectorTeam{}
	var walk func(json.RawMessage, int)
	walk = func(data json.RawMessage, depth int) {
		if depth > 8 {
			return
		}
		var list []json.RawMessage
		if json.Unmarshal(data, &list) == nil {
			for _, item := range list {
				walk(item, depth+1)
			}
			return
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(data, &object) != nil {
			return
		}
		value := func(keys ...string) string {
			for _, key := range keys {
				var s string
				if json.Unmarshal(object[key], &s) == nil && s != "" {
					return s
				}
			}
			return ""
		}
		id := value("_id", "id", "teamID", "teamId")
		name := value("name", "teamName")
		if id != "" && name != "" {
			seen[id] = types.ConnectorTeam{ID: id, Name: name}
			return
		}
		for _, key := range []string{"teams", "guestTeams", "myTeams", "ownTeams", "joinedTeams", "list", "data", "payload"} {
			if child, ok := object[key]; ok {
				walk(child, depth+1)
			}
		}
	}
	walk(raw, 0)
	result := make([]types.ConnectorTeam, 0, len(seen))
	for _, team := range seen {
		result = append(result, team)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Name == result[j].Name {
			return result[i].ID < result[j].ID
		}
		return result[i].Name < result[j].Name
	})
	return result
}
