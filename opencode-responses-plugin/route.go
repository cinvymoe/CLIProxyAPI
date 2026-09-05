package main

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// openAIResponseFormat is the source format of /v1/responses requests
// (see sdk/translator FormatOpenAIResponse).
const openAIResponseFormat = "openai-response"

// openAIChatFormat is the source format of /v1/chat/completions requests
// (see sdk/translator FormatOpenAI).
const openAIChatFormat = "openai"

// decide implements the model.route decision for the opencode Responses fix.
// It returns Handled=true only when the request is an openai or
// openai-response request for one of the configured models, routing it to
// this plugin's executor so the payload reaches the upstream /responses
// endpoint (directly or after chat->responses translation) instead of being
// translated to chat/completions by the built-in openai-compat executor.
func decide(req pluginapi.ModelRouteRequest, cfg pluginConfig) pluginapi.ModelRouteResponse {
	notHandled := pluginapi.ModelRouteResponse{Handled: false}
	if !cfg.Enabled || len(cfg.Models) == 0 {
		return notHandled
	}
	format := strings.TrimSpace(req.SourceFormat)
	if !strings.EqualFold(format, openAIResponseFormat) && !strings.EqualFold(format, openAIChatFormat) {
		return notHandled
	}
	base := normalizeModelName(req.RequestedModel)
	if base == "" || !modelListContains(cfg.Models, base) {
		return notHandled
	}
	return pluginapi.ModelRouteResponse{
		Handled:     true,
		TargetKind:  pluginapi.ModelRouteTargetExecutor,
		Target:      pluginID,
		TargetModel: strings.TrimSpace(req.RequestedModel),
		Reason:      "openai/openai-response request for responses-only model routed to opencode-responses executor",
	}
}

// normalizeModelName strips any thinking suffix (e.g. "model(high)") and
// surrounding whitespace from a model name.
func normalizeModelName(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return ""
	}
	base := strings.TrimSpace(thinking.ParseSuffix(model).ModelName)
	if base == "" {
		return model
	}
	return base
}

// modelListContains reports whether base matches any configured model,
// case-insensitively and ignoring thinking suffixes on the configured entries.
func modelListContains(models []string, base string) bool {
	for _, item := range models {
		if strings.EqualFold(normalizeModelName(item), base) {
			return true
		}
	}
	return false
}
