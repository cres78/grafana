package plugincheck

import (
	"context"
	"fmt"
	sysruntime "runtime"

	"github.com/Masterminds/semver/v3"
	"github.com/grafana/grafana-app-sdk/logging"
	advisor "github.com/grafana/grafana/apps/advisor/pkg/apis/advisor/v0alpha1"
	"github.com/grafana/grafana/apps/advisor/pkg/app/checks"
	"github.com/grafana/grafana/pkg/plugins/repo"
	"github.com/grafana/grafana/pkg/services/pluginsintegration/pluginchecker"
	"github.com/grafana/grafana/pkg/services/pluginsintegration/pluginstore"
)

const (
	CheckID           = "plugin"
	DeprecationStepID = "deprecation"
	UpdateStepID      = "update"
)

func New(
	pluginStore pluginstore.Store,
	pluginRepo repo.Service,
	updateChecker pluginchecker.PluginUpdateChecker,
	grafanaVersion string,
) checks.Check {
	return &check{
		PluginStore:    pluginStore,
		PluginRepo:     pluginRepo,
		GrafanaVersion: grafanaVersion,
		updateChecker:  updateChecker,
	}
}

type check struct {
	PluginStore    pluginstore.Store
	PluginRepo     repo.Service
	updateChecker  pluginchecker.PluginUpdateChecker
	GrafanaVersion string
}

func (c *check) ID() string {
	return CheckID
}

func (c *check) Items(ctx context.Context) ([]any, error) {
	ps := c.PluginStore.Plugins(ctx)
	res := make([]any, len(ps))
	for i, p := range ps {
		res[i] = p
	}
	return res, nil
}

func (c *check) Item(ctx context.Context, id string) (any, error) {
	p, exists := c.PluginStore.Plugin(ctx, id)
	if !exists {
		return nil, fmt.Errorf("plugin %s not found", id)
	}
	return p, nil
}

func (c *check) Steps() []checks.Step {
	return []checks.Step{
		&deprecationStep{
			PluginRepo:     c.PluginRepo,
			GrafanaVersion: c.GrafanaVersion,
			updateChecker:  c.updateChecker,
		},
		&updateStep{
			PluginRepo:     c.PluginRepo,
			GrafanaVersion: c.GrafanaVersion,
			updateChecker:  c.updateChecker,
		},
	}
}

type deprecationStep struct {
	PluginRepo     repo.Service
	GrafanaVersion string
	updateChecker  pluginchecker.PluginUpdateChecker
}

func (s *deprecationStep) Title() string {
	return "Deprecation check"
}

func (s *deprecationStep) Description() string {
	return "Check if any installed plugins are deprecated."
}

func (s *deprecationStep) Resolution() string {
	return "Check the <a href='https://grafana.com/legal/plugin-deprecation/#a-plugin-i-use-is-deprecated-what-should-i-do'" +
		"target=_blank>documentation</a> for recommended steps or delete the plugin."
}

func (s *deprecationStep) ID() string {
	return DeprecationStepID
}

func (s *deprecationStep) Run(ctx context.Context, log logging.Logger, _ *advisor.CheckSpec, it any) ([]advisor.CheckReportFailure, error) {
	p, ok := it.(pluginstore.Plugin)
	if !ok {
		return nil, fmt.Errorf("invalid item type %T", it)
	}

	if !s.updateChecker.IsUpdatable(ctx, p) {
		return nil, nil
	}

	// Check if plugin is deprecated
	compatOpts := repo.NewCompatOpts(s.GrafanaVersion, sysruntime.GOOS, sysruntime.GOARCH)
	i, err := s.PluginRepo.PluginInfo(ctx, p.ID, compatOpts)
	if err != nil {
		// Unable to check deprecation status
		return nil, nil
	}
	if i.Status == "deprecated" {
		return []advisor.CheckReportFailure{checks.NewCheckReportFailure(
			advisor.CheckReportFailureSeverityHigh,
			s.ID(),
			p.Name,
			p.ID,
			[]advisor.CheckErrorLink{
				{
					Message: "Admin",
					Url:     fmt.Sprintf("/plugins/%s", p.ID),
				},
			},
		)}, nil
	}
	return nil, nil
}

type updateStep struct {
	PluginRepo     repo.Service
	GrafanaVersion string
	updateChecker  pluginchecker.PluginUpdateChecker
}

func (s *updateStep) Title() string {
	return "Update check"
}

func (s *updateStep) Description() string {
	return "Checks if an installed plugins has a newer version available."
}

func (s *updateStep) Resolution() string {
	return "Go to the plugin admin page and upgrade to the latest version."
}

func (s *updateStep) ID() string {
	return UpdateStepID
}

func (s *updateStep) Run(ctx context.Context, log logging.Logger, _ *advisor.CheckSpec, i any) ([]advisor.CheckReportFailure, error) {
	p, ok := i.(pluginstore.Plugin)
	if !ok {
		return nil, fmt.Errorf("invalid item type %T", i)
	}

	if !s.updateChecker.IsUpdatable(ctx, p) {
		return nil, nil
	}

	// Check if plugin has a newer version available
	compatOpts := repo.NewCompatOpts(s.GrafanaVersion, sysruntime.GOOS, sysruntime.GOARCH)
	info, err := s.PluginRepo.GetPluginArchiveInfo(ctx, p.ID, "", compatOpts)
	if err != nil {
		// Unable to check updates
		return nil, nil
	}
	if hasUpdate(p, info) {
		return []advisor.CheckReportFailure{checks.NewCheckReportFailure(
			advisor.CheckReportFailureSeverityLow,
			s.ID(),
			p.Name,
			p.ID,
			[]advisor.CheckErrorLink{
				{
					Message: "Upgrade",
					Url:     fmt.Sprintf("/plugins/%s?page=version-history", p.ID),
				},
			},
		)}, nil
	}

	return nil, nil
}

func hasUpdate(current pluginstore.Plugin, latest *repo.PluginArchiveInfo) bool {
	// If both versions are semver-valid, compare them
	v1, err1 := semver.NewVersion(current.Info.Version)
	v2, err2 := semver.NewVersion(latest.Version)
	if err1 == nil && err2 == nil {
		return v1.LessThan(v2)
	}
	// In other case, assume that a different latest version will always be newer
	return current.Info.Version != latest.Version
}
