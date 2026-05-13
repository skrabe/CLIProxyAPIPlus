package usagelog

import (
	"context"
	"fmt"
	"strings"

	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

func init() {
	coreusage.RegisterPlugin(&plugin{})
}

type plugin struct{}

func (p *plugin) HandleUsage(_ context.Context, r coreusage.Record) {
	d := r.Detail
	if r.Failed && d == (coreusage.Detail{}) {
		return
	}
	cacheRead := d.CacheReadTokens
	if cacheRead == 0 {
		cacheRead = d.CachedTokens
	}
	denom := d.InputTokens + d.CacheCreationTokens
	if cacheRead > 0 && d.InputTokens < cacheRead {
		denom += cacheRead
	}
	hitPct := "n/a"
	if denom > 0 {
		hitPct = fmt.Sprintf("%.1f%%", float64(cacheRead)*100.0/float64(denom))
	}
	parts := []string{
		fmt.Sprintf("in=%d", d.InputTokens),
		fmt.Sprintf("out=%d", d.OutputTokens),
		fmt.Sprintf("cache_read=%d", cacheRead),
		fmt.Sprintf("cache_create=%d", d.CacheCreationTokens),
		fmt.Sprintf("reasoning=%d", d.ReasoningTokens),
		fmt.Sprintf("total=%d", d.TotalTokens),
		fmt.Sprintf("hit=%s", hitPct),
		fmt.Sprintf("latency=%dms", r.Latency.Milliseconds()),
	}
	if r.Failed {
		parts = append(parts, fmt.Sprintf("failed=%d", r.Fail.StatusCode))
	}
	msg := "usage " + strings.Join(parts, " ")
	log.WithFields(log.Fields{
		"provider": r.Provider,
		"model":    r.Model,
	}).Info(msg)
}
