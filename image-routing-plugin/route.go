package main

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// decide implements the model.route decision for image routing.
// It returns Handled=true only when the requested model is declared as not
// supporting images, the request body contains image content, and the
// configured fallback provider is currently available.
func decide(req pluginapi.ModelRouteRequest, cfg routingConfig) pluginapi.ModelRouteResponse {
	notHandled := pluginapi.ModelRouteResponse{Handled: false}
	if !cfg.Enabled || cfg.Fallback == "" || cfg.FallbackProvider == "" || len(cfg.Models) == 0 {
		return notHandled
	}
	base := strings.TrimSpace(thinking.ParseSuffix(req.RequestedModel).ModelName)
	if base == "" {
		base = strings.TrimSpace(req.RequestedModel)
	}
	if !containsFold(cfg.Models, base) {
		return notHandled
	}
	if !detectImage(req.Body, req.SourceFormat) {
		return notHandled
	}
	if !containsFold(req.AvailableProviders, cfg.FallbackProvider) {
		return notHandled
	}
	return pluginapi.ModelRouteResponse{
		Handled:     true,
		TargetKind:  pluginapi.ModelRouteTargetProvider,
		Target:      cfg.FallbackProvider,
		TargetModel: cfg.Fallback,
		Reason:      "image request routed to configured fallback model",
	}
}

func containsFold(list []string, value string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), value) {
			return true
		}
	}
	return false
}
