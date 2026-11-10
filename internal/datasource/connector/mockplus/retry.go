package mockplus

import (
	"context"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

// FetchRetryStream keeps the full existing cursor while restricting source reads
// to the failed projects/boards. It never reconciles source deletions.
func (c *Connector) FetchRetryStream(ctx context.Context, config *types.DataSourceConfig, cursor *types.SyncCursor, targets []types.SyncRetryTarget, h datasource.StreamHandler) (*types.SyncCursor, error) {
	if len(targets) == 0 {
		return nil, errors.New("没有可定位的失败项")
	}
	copyConfig := *config
	copyConfig.RetryOnly = true
	copyConfig.RetryTargets = targets
	return c.fetch(ctx, &copyConfig, cursor, h, false)
}

func retryFailure(target types.SyncRetryTarget, reason string) types.FetchedItem {
	item := failure(target.ExternalID, target.Title, reason)
	item.Metadata["retry_kind"] = target.Kind
	item.Metadata["project_id"] = target.ProjectID
	item.Metadata["board_id"] = target.BoardID
	item.Metadata["retry_phase"] = target.Phase
	return item
}

func retryProjects(ctx context.Context, config *types.DataSourceConfig, projects []treeNode, state map[string]boardState, h datasource.StreamHandler) ([]treeNode, map[string][]types.SyncRetryTarget, error) {
	byID := map[string]treeNode{}
	for _, p := range projects {
		byID[p.id()] = p
	}
	groups := map[string][]types.SyncRetryTarget{}
	for _, target := range config.RetryTargets {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if target.Kind == "legacy" && target.ProjectID == "" {
			// Prefer a unique saved identity over matching a current display name.
			known := []types.SyncRetryTarget{}
			for id, old := range state {
				if old.Path+" / "+old.Title == target.Title {
					known = append(known, types.SyncRetryTarget{Kind: "board", ExternalID: id, ProjectID: old.AppID, BoardID: old.BoardID, Title: target.Title, Phase: target.Phase})
				}
			}
			if len(known) == 1 {
				target = known[0]
			}
		}
		if target.Kind == "legacy" && target.ProjectID == "" {
			matches := []treeNode{}
			for _, p := range projects {
				if target.Title == p.Path {
					matches = []treeNode{p}
					target.Kind = "project"
					break
				}
				if strings.HasPrefix(target.Title, p.Path+" / ") {
					matches = append(matches, p)
				}
			}
			if len(matches) == 1 {
				target.ProjectID = matches[0].id()
				if target.Kind == "project" {
					target.ExternalID = target.ProjectID
				}
			}
		}
		p, ok := byID[target.ProjectID]
		if !ok || p.Inactive || (target.Kind != "project" && target.Kind != "board" && target.Kind != "legacy") || target.Phase == "delete" {
			if _, err := emit(ctx, h, retryFailure(target, "失败项无法唯一定位、已停用或不在当前同步范围内，未扩大重试范围")); err != nil {
				return nil, nil, err
			}
			continue
		}
		groups[target.ProjectID] = append(groups[target.ProjectID], target)
	}
	selected := []treeNode{}
	for _, p := range projects {
		if len(groups[p.id()]) > 0 {
			selected = append(selected, p)
		}
	}
	return selected, groups, nil
}

func retryBoards(ctx context.Context, cl *client, project treeNode, boards []node, targets []types.SyncRetryTarget, h datasource.StreamHandler) ([]node, error) {
	for _, target := range targets {
		if target.Kind == "project" {
			return boards, nil
		}
	}
	selected := map[string]bool{}
	for _, target := range targets {
		matches := []node{}
		for _, b := range boards {
			id := "mockplus:" + cl.team + ":" + project.id() + ":" + b.id()
			if target.Kind == "board" && target.ExternalID == id && target.BoardID == b.id() {
				matches = append(matches, b)
			}
			if target.Kind == "legacy" && target.Title == project.Path+" / "+b.Name {
				matches = append(matches, b)
			}
		}
		if len(matches) != 1 || matches[0].inactive() {
			if _, err := emit(ctx, h, retryFailure(target, "失败画板缺失、已停用或存在同名，未处理其他画板")); err != nil {
				return nil, err
			}
			continue
		}
		selected[matches[0].id()] = true
	}
	result := []node{}
	for _, b := range boards {
		if selected[b.id()] {
			result = append(result, b)
		}
	}
	return result, nil
}
