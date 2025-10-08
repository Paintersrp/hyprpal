package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/hyprpal/hyprpal/internal/config"
	"github.com/hyprpal/hyprpal/internal/engine"
	"github.com/hyprpal/hyprpal/internal/layout"
	"github.com/hyprpal/hyprpal/internal/metrics"
	"github.com/hyprpal/hyprpal/internal/rules"
	"github.com/hyprpal/hyprpal/internal/util"
)

type configReloader struct {
	path           string
	logger         *util.Logger
	engine         *engine.Engine
	metrics        *metrics.Collector
	lastConfig     *config.Config
	lastSerialized []byte
}

func newConfigReloader(path string, logger *util.Logger, eng *engine.Engine, metrics *metrics.Collector, cfg *config.Config, serialized []byte) *configReloader {
	return &configReloader{
		path:           path,
		logger:         logger,
		engine:         eng,
		metrics:        metrics,
		lastConfig:     cfg,
		lastSerialized: append([]byte(nil), serialized...),
	}
}

func (r *configReloader) Reload(ctx context.Context, reason string) error {
	r.logger.Infof("%s, reloading config", reason)
	raw, err := os.ReadFile(r.path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}
	cfg, err := config.Parse(raw)
	if err != nil {
		r.logDiff(raw)
		return err
	}
	if lintErrs := cfg.Lint(); len(lintErrs) > 0 {
		r.logLintErrors(lintErrs)
		r.logDiff(raw)
		return fmt.Errorf(lintErrs[0].Error())
	}
	modes, err := rules.BuildModes(cfg)
	if err != nil {
		r.logDiff(raw)
		return fmt.Errorf("compile rules: %w", err)
	}

	r.engine.ReloadModes(modes)
	r.engine.SetRedactTitles(cfg.RedactTitles)
	r.engine.SetLayoutParameters(layout.Gaps{
		Inner: cfg.Gaps.Inner,
		Outer: cfg.Gaps.Outer,
	}, cfg.TolerancePx)
	r.engine.SetManualReserved(cfg.ManualReserved)
	if r.metrics != nil {
		r.metrics.SetEnabled(cfg.Telemetry.Enabled)
	}
	if err := r.engine.Reconcile(ctx); err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("reconcile after reload: %w", err)
	}

	r.lastConfig = cfg
	r.lastSerialized = append([]byte(nil), raw...)
	return nil
}

func (r *configReloader) logDiff(current []byte) {
	diff := config.DiffSerialized(r.lastSerialized, current)
	if diff == "" {
		r.logger.Warnf("config change rejected; unable to compute diff vs last valid config")
		return
	}
	r.logger.Warnf("config change rejected; diff vs last valid config:\n%s", diff)
}

func (r *configReloader) logLintErrors(errs []config.LintError) {
	r.logger.Warnf("config validation failed with %d issue(s):", len(errs))
	for _, lintErr := range errs {
		if lintErr.Path != "" {
			r.logger.Warnf(" - %s: %s", lintErr.Path, lintErr.Message)
			continue
		}
		r.logger.Warnf(" - %s", lintErr.Message)
	}
}
