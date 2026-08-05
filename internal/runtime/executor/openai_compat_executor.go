package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	openAICompatImageHandlerType            = "openai-image"
	openAICompatImagesGenerationsPath       = "/images/generations"
	openAICompatImagesEditsPath             = "/images/edits"
	openAICompatDefaultImageEndpoint        = openAICompatImagesGenerationsPath
	openAICompatMultipartMemory       int64 = 32 << 20
)

// OpenAICompatExecutor implements a stateless executor for OpenAI-compatible providers.
// It performs request/response translation and executes against the provider base URL
// using per-auth credentials (API key) and per-auth HTTP transport (proxy) from context.
type OpenAICompatExecutor struct {
	provider string
	cfg      *config.Config
}

// NewOpenAICompatExecutor creates an executor bound to a provider key (e.g., "openrouter").
func NewOpenAICompatExecutor(provider string, cfg *config.Config) *OpenAICompatExecutor {
	return &OpenAICompatExecutor{provider: provider, cfg: cfg}
}

// Identifier implements cliproxyauth.ProviderExecutor.
func (e *OpenAICompatExecutor) Identifier() string { return e.provider }

// PrepareRequest injects OpenAI-compatible credentials into the outgoing HTTP request.
func (e *OpenAICompatExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	_, apiKey := e.resolveCredentials(auth)
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(req, attrs)
	return nil
}

// HttpRequest injects OpenAI-compatible credentials into the request and executes it.
func (e *OpenAICompatExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("openai compat executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if err := e.PrepareRequest(httpReq, auth); err != nil {
		return nil, err
	}
	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

func (e *OpenAICompatExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	if endpointPath := openAICompatImageEndpointPath(opts); endpointPath != "" {
		return e.executeImages(ctx, auth, req, opts, endpointPath)
	}

	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := helps.NewExecutorUsageReporter(ctx, e, baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	baseURL, apiKey := e.resolveCredentials(auth)
	if baseURL == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "missing provider baseURL"}
		return
	}

	from := opts.SourceFormat
	responseFormat := cliproxyexecutor.ResponseFormatOrSource(opts)
	to := sdktranslator.FromString("openai")
	endpoint := "/chat/completions"
	if opts.Alt == "responses/compact" {
		to = sdktranslator.FromString("openai-response")
		endpoint = "/responses/compact"
	}
	originalPayloadSource := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayloadSource = opts.OriginalRequest
	}
	originalPayload := originalPayloadSource
	isCompat := helps.APIKeyModelIsCompat(req)
	originalTranslated := helps.TranslateRequestWithAPIKeyModelCompatibility(ctx, opts.Headers, e.cfg, from, to, baseModel, originalPayload, opts.Stream, isCompat)
	translated := helps.TranslateRequestWithAPIKeyModelCompatibility(ctx, opts.Headers, e.cfg, from, to, baseModel, req.Payload, opts.Stream, isCompat)

	translated, err = helps.ApplyRequestThinking(translated, req, opts, from.String(), to.String(), e.Identifier())
	if err != nil {
		return resp, err
	}

	requestedModel := helps.PayloadRequestedModel(opts, req.Model)
	requestPath := helps.PayloadRequestPath(opts)
	translated = helps.ApplyPayloadConfigWithRequest(e.cfg, baseModel, to.String(), from.String(), "", translated, originalTranslated, requestedModel, requestPath, opts.Headers)
	if helps.ShouldNormalizeOpenAIToolResultsForModel(e.resolveCompatConfig(auth), baseModel, requestedModel) {
		translated = helps.NormalizeOpenAIToolResultsTextOnly(translated)
	}
	translated, err = normalizeCompatTemperatureForUpstream(translated, baseModel, baseURL)
	if err != nil {
		return resp, err
	}
	if opts.Alt != "responses/compact" {
		translated, err = e.applyPromptCacheKey(ctx, auth, from, baseModel, req, opts, translated)
		if err != nil {
			return resp, err
		}
	}
	if opts.Alt == "responses/compact" {
		if updated, errDelete := sjson.DeleteBytes(translated, "stream"); errDelete == nil {
			translated = updated
		}
		translated = sanitizeOpenAIResponsesReasoningEncryptedContent(ctx, "openai compat executor", translated)
	}
	reporter.SetTranslatedReasoningEffort(translated, to.String())

	xunfeiOriginalPayload := make([]byte, len(translated))
	copy(xunfeiOriginalPayload, translated)
	translated = sanitizeXunfeiPayload(translated)

	url := strings.TrimSuffix(baseURL, "/") + endpoint
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
	if err != nil {
		return resp, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	httpReq.Header.Set("User-Agent", "cli-proxy-openai-compat")
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      translated,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpClient = reporter.TrackHTTPClient(httpClient)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		err = wrapTransientNetworkError(err)
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("openai compat executor: close response body error: %v", errClose)
		}
	}()
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		cls := classifyXunfeiError(b)
		if cls == xunfeiErrRetryable {
			waits := e.cfg.XunfeiRetry.WaitDurations()
			for i, wait := range waits {
				helps.LogWithRequestID(ctx).Debugf("xunfei retryable error, attempt %d/%d, waiting %dms", i+1, len(waits), wait)
				if errSleep := sleepWithContext(ctx, time.Duration(wait)*time.Millisecond); errSleep != nil {
					return resp, errSleep
				}
				httpReqRetry, errRetry := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
				if errRetry != nil {
					return resp, errRetry
				}
				httpReqRetry.Header.Set("Content-Type", "application/json")
				if apiKey != "" {
					httpReqRetry.Header.Set("Authorization", "Bearer "+apiKey)
				}
				httpReqRetry.Header.Set("User-Agent", "cli-proxy-openai-compat")
				util.ApplyCustomHeadersFromAttrs(httpReqRetry, attrs)
				httpRespRetry, errDo := httpClient.Do(httpReqRetry)
				if errDo != nil {
					helps.RecordAPIResponseError(ctx, e.cfg, errDo)
					return resp, errDo
				}
				helps.RecordAPIResponseMetadata(ctx, e.cfg, httpRespRetry.StatusCode, httpRespRetry.Header.Clone())
				if httpRespRetry.StatusCode >= 200 && httpRespRetry.StatusCode < 300 {
					body, errRead := io.ReadAll(httpRespRetry.Body)
					if errClose := httpRespRetry.Body.Close(); errClose != nil {
						log.Errorf("openai compat executor: close retry response body error: %v", errClose)
					}
					if errRead != nil {
						helps.RecordAPIResponseError(ctx, e.cfg, errRead)
						return resp, errRead
					}
					helps.AppendAPIResponseChunk(ctx, e.cfg, body)
					reporter.Publish(ctx, helps.ParseOpenAIUsage(body))
					reporter.EnsurePublished(ctx)
					var param any
					out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, opts.OriginalRequest, translated, body, &param)
					resp = cliproxyexecutor.Response{Payload: out, Headers: httpRespRetry.Header.Clone()}
					return resp, nil
				}
				bRetry, _ := io.ReadAll(httpRespRetry.Body)
				if errClose := httpRespRetry.Body.Close(); errClose != nil {
					log.Errorf("openai compat executor: close retry response body error: %v", errClose)
				}
				helps.AppendAPIResponseChunk(ctx, e.cfg, bRetry)
				helps.LogWithRequestID(ctx).Debugf("retry attempt %d failed, status: %d, body: %s", i+1, httpRespRetry.StatusCode, helps.SummarizeErrorBody(httpRespRetry.Header.Get("Content-Type"), bRetry))
				clsRetry := classifyXunfeiError(bRetry)
				if clsRetry != xunfeiErrRetryable {
					cls = clsRetry
					b = bRetry
					break
				}
			}
		}
		if cls != xunfeiErrNone {
			logXunfeiErrorDetail(ctx, xunfeiOriginalPayload, translated, b)
			err = xunfeiStatusErr(cls, string(b))
			return resp, err
		}
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return resp, err
	}
	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		err = wrapTransientNetworkError(err)
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	helps.AppendAPIResponseChunk(ctx, e.cfg, body)
	reporter.Publish(ctx, helps.ParseOpenAIUsage(body))
	// Ensure we at least record the request even if upstream doesn't return usage
	reporter.EnsurePublished(ctx)
	// Translate response back to source format when needed
	var param any
	out := sdktranslator.TranslateNonStream(ctx, to, responseFormat, req.Model, opts.OriginalRequest, translated, body, &param)
	resp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
	return resp, nil
}

func (e *OpenAICompatExecutor) executeImages(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, endpointPath string) (resp cliproxyexecutor.Response, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := helps.NewExecutorUsageReporter(ctx, e, baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	baseURL, apiKey := e.resolveCredentials(auth)
	if baseURL == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "missing provider baseURL"}
		return resp, err
	}

	payload, contentType, errPrepare := prepareOpenAICompatImagesPayload(req.Payload, baseModel, opts.Headers.Get("Content-Type"), false)
	if errPrepare != nil {
		err = errPrepare
		return resp, err
	}
	if contentType == "" {
		contentType = "application/json"
	}
	reporter.SetTranslatedReasoningEffort(payload, "openai")

	url := strings.TrimSuffix(baseURL, "/") + endpointPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return resp, err
	}
	httpReq.Header.Set("Content-Type", contentType)
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	httpReq.Header.Set("User-Agent", "cli-proxy-openai-compat")
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      payload,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpClient = reporter.TrackHTTPClient(httpClient)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		err = wrapTransientNetworkError(err)
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("openai compat executor: close response body error: %v", errClose)
		}
	}()
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	body, errRead := io.ReadAll(httpResp.Body)
	if errRead != nil {
		errRead = wrapTransientNetworkError(errRead)
		helps.RecordAPIResponseError(ctx, e.cfg, errRead)
		err = errRead
		return resp, err
	}
	helps.AppendAPIResponseChunk(ctx, e.cfg, body)

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), body))
		err = statusErr{code: httpResp.StatusCode, msg: string(body)}
		return resp, err
	}

	reporter.Publish(ctx, helps.ParseOpenAIUsage(body))
	reporter.EnsurePublished(ctx)
	resp = cliproxyexecutor.Response{Payload: body, Headers: httpResp.Header.Clone()}
	return resp, nil
}

func (e *OpenAICompatExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	if endpointPath := openAICompatImageEndpointPath(opts); endpointPath != "" {
		return e.executeImagesStream(ctx, auth, req, opts, endpointPath)
	}

	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := helps.NewExecutorUsageReporter(ctx, e, baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	baseURL, apiKey := e.resolveCredentials(auth)
	if baseURL == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "missing provider baseURL"}
		return nil, err
	}

	from := opts.SourceFormat
	responseFormat := cliproxyexecutor.ResponseFormatOrSource(opts)
	to := sdktranslator.FromString("openai")
	originalPayloadSource := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayloadSource = opts.OriginalRequest
	}
	originalPayload := originalPayloadSource
	isCompat := helps.APIKeyModelIsCompat(req)
	originalTranslated := helps.TranslateRequestWithAPIKeyModelCompatibility(ctx, opts.Headers, e.cfg, from, to, baseModel, originalPayload, true, isCompat)
	translated := helps.TranslateRequestWithAPIKeyModelCompatibility(ctx, opts.Headers, e.cfg, from, to, baseModel, req.Payload, true, isCompat)

	translated, err = helps.ApplyRequestThinking(translated, req, opts, from.String(), to.String(), e.Identifier())
	if err != nil {
		return nil, err
	}

	requestedModel := helps.PayloadRequestedModel(opts, req.Model)
	requestPath := helps.PayloadRequestPath(opts)
	translated = helps.ApplyPayloadConfigWithRequest(e.cfg, baseModel, to.String(), from.String(), "", translated, originalTranslated, requestedModel, requestPath, opts.Headers)
	if helps.ShouldNormalizeOpenAIToolResultsForModel(e.resolveCompatConfig(auth), baseModel, requestedModel) {
		translated = helps.NormalizeOpenAIToolResultsTextOnly(translated)
	}
	translated, err = normalizeCompatTemperatureForUpstream(translated, baseModel, baseURL)
	if err != nil {
		return nil, err
	}
	if opts.Alt != "responses/compact" {
		translated, err = e.applyPromptCacheKey(ctx, auth, from, baseModel, req, opts, translated)
		if err != nil {
			return nil, err
		}
	}

	// Request usage data in the final streaming chunk so that token statistics
	// are captured even when the upstream is an OpenAI-compatible provider.
	translated = helps.SetBoolIfDifferent(translated, "stream_options.include_usage", true)
	reporter.SetTranslatedReasoningEffort(translated, to.String())

	xunfeiOriginalPayload := make([]byte, len(translated))
	copy(xunfeiOriginalPayload, translated)
	translated = sanitizeXunfeiPayload(translated)

	url := strings.TrimSuffix(baseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	httpReq.Header.Set("User-Agent", "cli-proxy-openai-compat")
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      translated,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpClient = reporter.TrackHTTPClient(httpClient)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		err = wrapTransientNetworkError(err)
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("openai compat executor: close response body error: %v", errClose)
		}
		cls := classifyXunfeiError(b)
		if cls == xunfeiErrRetryable {
			waits := e.cfg.XunfeiRetry.WaitDurations()
			for i, wait := range waits {
				helps.LogWithRequestID(ctx).Debugf("xunfei stream retryable error, attempt %d/%d, waiting %dms", i+1, len(waits), wait)
				if errSleep := sleepWithContext(ctx, time.Duration(wait)*time.Millisecond); errSleep != nil {
					return nil, errSleep
				}
				httpReqRetry, errRetry := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(translated))
				if errRetry != nil {
					return nil, errRetry
				}
				httpReqRetry.Header.Set("Content-Type", "application/json")
				if apiKey != "" {
					httpReqRetry.Header.Set("Authorization", "Bearer "+apiKey)
				}
				httpReqRetry.Header.Set("User-Agent", "cli-proxy-openai-compat")
				util.ApplyCustomHeadersFromAttrs(httpReqRetry, attrs)
				httpReqRetry.Header.Set("Accept", "text/event-stream")
				httpReqRetry.Header.Set("Cache-Control", "no-cache")
				httpRespRetry, errDo := httpClient.Do(httpReqRetry)
				if errDo != nil {
					helps.RecordAPIResponseError(ctx, e.cfg, errDo)
					return nil, errDo
				}
				helps.RecordAPIResponseMetadata(ctx, e.cfg, httpRespRetry.StatusCode, httpRespRetry.Header.Clone())
				if httpRespRetry.StatusCode >= 200 && httpRespRetry.StatusCode < 300 {
					// Success on retry — fall through to stream processing below
					httpResp = httpRespRetry
					goto streamSuccess
				}
				bRetry, _ := io.ReadAll(httpRespRetry.Body)
				if errClose := httpRespRetry.Body.Close(); errClose != nil {
					log.Errorf("openai compat executor: close retry response body error: %v", errClose)
				}
				helps.AppendAPIResponseChunk(ctx, e.cfg, bRetry)
				helps.LogWithRequestID(ctx).Debugf("stream retry attempt %d failed, status: %d, body: %s", i+1, httpRespRetry.StatusCode, helps.SummarizeErrorBody(httpRespRetry.Header.Get("Content-Type"), bRetry))
				clsRetry := classifyXunfeiError(bRetry)
				if clsRetry != xunfeiErrRetryable {
					cls = clsRetry
					b = bRetry
					break
				}
			}
		}
		if cls != xunfeiErrNone {
			logXunfeiErrorDetail(ctx, xunfeiOriginalPayload, translated, b)
			err = xunfeiStatusErr(cls, string(b))
			return nil, err
		}
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return nil, err
	}
streamSuccess:
	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("openai compat executor: close response body error: %v", errClose)
			}
		}()
		peekReader := bufio.NewReader(httpResp.Body)
		peekBytes, errPeek := peekReader.Peek(1024)
		if errPeek == nil {
			cls := classifyXunfeiError(peekBytes)
			if cls != xunfeiErrNone {
				body, _ := io.ReadAll(peekReader)
				helps.LogWithRequestID(ctx).Debugf("xunfei stream peek error: class=%d, body=%s", cls, helps.SummarizeErrorBody("application/json", body))
				logXunfeiErrorDetail(ctx, xunfeiOriginalPayload, translated, body)
				if cls == xunfeiErrRetryable {
					retryAfter := time.Duration(e.cfg.XunfeiRetry.EffectiveInitialWait()) * time.Millisecond
					select {
					case out <- cliproxyexecutor.StreamChunk{Err: xunfeiStatusErrWithRetryAfter(cls, retryAfter)}:
					case <-ctx.Done():
					}
				} else {
					select {
					case out <- cliproxyexecutor.StreamChunk{Err: xunfeiStatusErr(cls, string(body))}:
					case <-ctx.Done():
					}
				}
				return
			}
		}
		scanner := bufio.NewScanner(peekReader)
		scanner.Buffer(nil, 52_428_800) // 50MB
		claudeInputTokens := helps.NewClaudeInputTokenState(from, to, responseFormat, originalPayload)
		var param any
		var streamUsage helps.StreamUsageBuffer
		var seenDone bool
		var streamFailed bool
		var streamAborted bool
		var upstreamEvent string
		var frameData [][]byte
		defer streamUsage.Publish(ctx, reporter)

		publishStreamError := func(streamErr statusErr, containsPayload bool) {
			loggedErr := streamErr
			if containsPayload {
				loggedErr = statusErr{code: streamErr.code, msg: "upstream stream returned an error payload"}
			}
			helps.RecordAPIResponseError(ctx, e.cfg, loggedErr)
			reporter.PublishFailure(ctx, loggedErr)
			select {
			case out <- cliproxyexecutor.StreamChunk{Err: streamErr}:
			case <-ctx.Done():
			}
			streamFailed = true
		}

		processFrame := func() bool {
			eventName := upstreamEvent
			upstreamEvent = ""
			dataLines := frameData
			frameData = nil
			if len(dataLines) == 0 {
				if openAICompatErrorEvent(eventName) {
					publishStreamError(statusErr{code: http.StatusBadGateway, msg: "upstream error event ended without data"}, false)
					return true
				}
				return false
			}

			if len(dataLines) > 1 {
				for _, dataLine := range dataLines {
					if bytes.Equal(bytes.TrimSpace(dataLine), []byte("[DONE]")) {
						publishStreamError(statusErr{code: http.StatusBadGateway, msg: "upstream stream ended with incomplete data before [DONE]"}, false)
						return true
					}
				}
			}
			dataPayload := bytes.TrimSpace(bytes.Join(dataLines, []byte("\n")))
			isDone := bytes.Equal(dataPayload, []byte("[DONE]"))
			if isDone && openAICompatErrorEvent(eventName) {
				publishStreamError(statusErr{code: http.StatusBadGateway, msg: "upstream error event ended before [DONE]"}, false)
				return true
			}
			if !isDone && !json.Valid(dataPayload) {
				publishStreamError(statusErr{code: http.StatusBadGateway, msg: "upstream stream ended with incomplete SSE data frame"}, false)
				return true
			}
			if !isDone {
				if streamErr, isError := openAICompatStreamDataError(dataPayload, eventName); isError {
					publishStreamError(streamErr, true)
					return true
				}
			}

			streamLine := append([]byte("data: "), dataPayload...)
			chunks := helps.TranslateStreamWithClaudeInputTokens(ctx, to, responseFormat, req.Model, opts.OriginalRequest, translated, streamLine, &param, claudeInputTokens)
			for i := range chunks {
				select {
				case out <- cliproxyexecutor.StreamChunk{Payload: chunks[i]}:
				case <-ctx.Done():
					streamAborted = true
					return true
				}
			}
			if isDone {
				seenDone = true
				return true
			}
			return false
		}

	scanLoop:
		for scanner.Scan() {
			line := scanner.Bytes()
			helps.AppendAPIResponseChunk(ctx, e.cfg, line)
			streamUsage.ObserveOpenAIStream(line)
			trimmedLine := bytes.TrimSpace(line)
			if len(trimmedLine) == 0 {
				if processFrame() {
					break scanLoop
				}
				continue
			}

			if bytes.Contains(trimmedLine, []byte(`"code"`)) && (bytes.Contains(trimmedLine, []byte(`10010`)) || bytes.Contains(trimmedLine, []byte(`10012`)) || bytes.Contains(trimmedLine, []byte(`11210`))) {
				jsonBody := trimmedLine
				if bytes.HasPrefix(trimmedLine, []byte("data: ")) {
					jsonBody = trimmedLine[6:]
				}
				cls := classifyXunfeiError(jsonBody)
				if cls != xunfeiErrNone {
					helps.LogWithRequestID(ctx).Debugf("xunfei stream inline error: class=%d, body=%s", cls, helps.SummarizeErrorBody("application/json", jsonBody))
					logXunfeiErrorDetail(ctx, xunfeiOriginalPayload, translated, jsonBody)
					if cls == xunfeiErrRetryable {
						retryAfter := time.Duration(e.cfg.XunfeiRetry.EffectiveInitialWait()) * time.Millisecond
						select {
						case out <- cliproxyexecutor.StreamChunk{Err: xunfeiStatusErrWithRetryAfter(cls, retryAfter)}:
						case <-ctx.Done():
						}
					} else {
						select {
						case out <- cliproxyexecutor.StreamChunk{Err: xunfeiStatusErr(cls, string(jsonBody))}:
						case <-ctx.Done():
						}
					}
					return
				}
			}
			if bytes.HasPrefix(trimmedLine, []byte("data:")) {
				frameData = append(frameData, bytes.Clone(bytes.TrimSpace(trimmedLine[len("data:"):])))
				continue
			}
			if bytes.HasPrefix(trimmedLine, []byte("event:")) {
				upstreamEvent = strings.TrimSpace(string(trimmedLine[len("event:"):]))
				continue
			}
			if bytes.HasPrefix(trimmedLine, []byte(":")) || bytes.HasPrefix(trimmedLine, []byte("id:")) || bytes.HasPrefix(trimmedLine, []byte("retry:")) {
				continue
			}
			if bytes.HasPrefix(trimmedLine, []byte("{")) || bytes.HasPrefix(trimmedLine, []byte("[")) {
				publishStreamError(statusErr{code: http.StatusBadGateway, msg: string(trimmedLine)}, true)
				break
			}
		}
		errScan := scanner.Err()
		if errScan == nil && !seenDone && !streamFailed && !streamAborted && len(frameData) > 0 {
			_ = processFrame()
		}
		if streamFailed || streamAborted {
			return
		}
		if errScan != nil {
			streamErr := wrapTransientNetworkError(errScan)
			helps.RecordAPIResponseError(ctx, e.cfg, streamErr)
			reporter.PublishFailure(ctx, streamErr)
			select {
			case out <- cliproxyexecutor.StreamChunk{Err: streamErr}:
			case <-ctx.Done():
			}
		} else if !seenDone {
			// Responses clients require an explicit terminal event. Treat a clean
			// upstream EOF without [DONE] as a failed stream instead of completing it.
			if responseFormat == sdktranslator.FormatOpenAIResponse {
				streamErr := statusErr{code: http.StatusBadGateway, msg: "upstream stream closed before [DONE]"}
				helps.RecordAPIResponseError(ctx, e.cfg, streamErr)
				reporter.PublishFailure(ctx, streamErr)
				select {
				case out <- cliproxyexecutor.StreamChunk{Err: streamErr}:
				case <-ctx.Done():
				}
				return
			}

			// Other protocols retain compatibility with providers that omit [DONE].
			chunks := helps.TranslateStreamWithClaudeInputTokens(ctx, to, responseFormat, req.Model, opts.OriginalRequest, translated, []byte("data: [DONE]"), &param, claudeInputTokens)
			for i := range chunks {
				select {
				case out <- cliproxyexecutor.StreamChunk{Payload: chunks[i]}:
				case <-ctx.Done():
					return
				}
			}
		}
		// Ensure we record the request if no usage chunk was ever seen.
		streamUsage.Publish(ctx, reporter)
		reporter.EnsurePublished(ctx)
	}()
	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}

func (e *OpenAICompatExecutor) executeImagesStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, endpointPath string) (_ *cliproxyexecutor.StreamResult, err error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	reporter := helps.NewExecutorUsageReporter(ctx, e, baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	baseURL, apiKey := e.resolveCredentials(auth)
	if baseURL == "" {
		err = statusErr{code: http.StatusUnauthorized, msg: "missing provider baseURL"}
		return nil, err
	}

	payload, contentType, errPrepare := prepareOpenAICompatImagesPayload(req.Payload, baseModel, opts.Headers.Get("Content-Type"), true)
	if errPrepare != nil {
		err = errPrepare
		return nil, err
	}
	if contentType == "" {
		contentType = "application/json"
	}
	reporter.SetTranslatedReasoningEffort(payload, "openai")

	url := strings.TrimSuffix(baseURL, "/") + endpointPath
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", contentType)
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Cache-Control", "no-cache")
	if apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	httpReq.Header.Set("User-Agent", "cli-proxy-openai-compat")
	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)
	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      payload,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpClient = reporter.TrackHTTPClient(httpClient)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		err = wrapTransientNetworkError(err)
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		body, errRead := io.ReadAll(httpResp.Body)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("openai compat executor: close response body error: %v", errClose)
		}
		if errRead != nil {
			helps.RecordAPIResponseError(ctx, e.cfg, errRead)
			return nil, errRead
		}
		helps.AppendAPIResponseChunk(ctx, e.cfg, body)
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), body))
		return nil, statusErr{code: httpResp.StatusCode, msg: string(body)}
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("openai compat executor: close response body error: %v", errClose)
			}
			reporter.EnsurePublished(ctx)
		}()
		buffer := make([]byte, 32*1024)
		for {
			n, errRead := httpResp.Body.Read(buffer)
			if n > 0 {
				chunk := bytes.Clone(buffer[:n])
				helps.AppendAPIResponseChunk(ctx, e.cfg, chunk)
				select {
				case out <- cliproxyexecutor.StreamChunk{Payload: chunk}:
				case <-ctx.Done():
					return
				}
			}
			if errRead != nil {
				if errRead != io.EOF {
					helps.RecordAPIResponseError(ctx, e.cfg, errRead)
					reporter.PublishFailure(ctx, errRead)
					select {
					case out <- cliproxyexecutor.StreamChunk{Err: errRead}:
					case <-ctx.Done():
					}
				}
				return
			}
		}
	}()
	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}

func (e *OpenAICompatExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	baseModel := thinking.ParseSuffix(req.Model).ModelName

	from := opts.SourceFormat
	responseFormat := cliproxyexecutor.ResponseFormatOrSource(opts)
	to := sdktranslator.FromString("openai")
	isCompat := helps.APIKeyModelIsCompat(req)
	translated := helps.TranslateRequestWithAPIKeyModelCompatibility(ctx, opts.Headers, e.cfg, from, to, baseModel, req.Payload, false, isCompat)

	modelForCounting := baseModel

	translated, err := helps.ApplyRequestThinking(translated, req, opts, from.String(), to.String(), e.Identifier())
	if err != nil {
		return cliproxyexecutor.Response{}, err
	}

	enc, err := helps.TokenizerForModel(modelForCounting)
	if err != nil {
		return cliproxyexecutor.Response{}, fmt.Errorf("openai compat executor: tokenizer init failed: %w", err)
	}

	count, err := helps.CountOpenAIChatTokens(enc, translated)
	if err != nil {
		return cliproxyexecutor.Response{}, fmt.Errorf("openai compat executor: token counting failed: %w", err)
	}

	usageJSON := helps.BuildOpenAIUsageJSON(count)
	translatedUsage := sdktranslator.TranslateTokenCount(ctx, to, responseFormat, count, usageJSON)
	return cliproxyexecutor.Response{Payload: translatedUsage}, nil
}

// Refresh is a no-op for API-key based compatibility providers.
// OAuth-style credentials with a refresh token cannot be rotated here; callers
// that need plugin/Home refresh must bind a refresh-capable executor instead.
func (e *OpenAICompatExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	log.Debugf("openai compat executor: refresh called")
	if refreshed, handled, err := helps.RefreshAuthViaHome(ctx, e.cfg, auth); handled {
		return refreshed, err
	}
	if openAICompatAuthHasRefreshToken(auth) {
		provider := ""
		if e != nil {
			provider = e.Identifier()
		}
		if provider == "" && auth != nil {
			provider = strings.TrimSpace(auth.Provider)
		}
		return nil, fmt.Errorf("openai compat executor cannot refresh oauth credentials for provider %s", provider)
	}
	return auth, nil
}

func openAICompatAuthHasRefreshToken(auth *cliproxyauth.Auth) bool {
	if auth == nil || auth.Metadata == nil {
		return false
	}
	if token, _ := auth.Metadata["refresh_token"].(string); strings.TrimSpace(token) != "" {
		return true
	}
	if token, _ := auth.Metadata["refreshToken"].(string); strings.TrimSpace(token) != "" {
		return true
	}
	return false
}

func openAICompatImageEndpointPath(opts cliproxyexecutor.Options) string {
	if opts.SourceFormat.String() != openAICompatImageHandlerType {
		return ""
	}
	path := helps.PayloadRequestPath(opts)
	if strings.HasSuffix(path, "/images/edits") {
		return openAICompatImagesEditsPath
	}
	if strings.HasSuffix(path, "/images/generations") {
		return openAICompatImagesGenerationsPath
	}
	return openAICompatDefaultImageEndpoint
}

func prepareOpenAICompatImagesPayload(payload []byte, model string, contentType string, stream bool) ([]byte, string, error) {
	model = strings.TrimSpace(model)
	contentType = strings.TrimSpace(contentType)
	if json.Valid(payload) {
		if model != "" {
			payload = helps.SetStringIfDifferent(payload, "model", model)
		}
		if stream {
			payload = helps.SetBoolIfDifferent(payload, "stream", true)
		} else {
			payload, _ = sjson.DeleteBytes(payload, "stream")
		}
		return payload, "application/json", nil
	}

	mediaType, params, errParse := mime.ParseMediaType(contentType)
	if errParse != nil || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "multipart/") {
		return payload, contentType, nil
	}
	boundary := strings.TrimSpace(params["boundary"])
	if boundary == "" {
		return nil, "", fmt.Errorf("multipart boundary is missing")
	}
	return rewriteOpenAICompatImagesMultipartPayload(payload, model, boundary, stream)
}

func cloneOpenAICompatMIMEHeader(src textproto.MIMEHeader) textproto.MIMEHeader {
	dst := make(textproto.MIMEHeader, len(src))
	for key, values := range src {
		dst[key] = append([]string(nil), values...)
	}
	return dst
}

func rewriteOpenAICompatImagesMultipartPayload(payload []byte, model string, boundary string, stream bool) ([]byte, string, error) {
	reader := multipart.NewReader(bytes.NewReader(payload), boundary)
	form, errRead := reader.ReadForm(openAICompatMultipartMemory)
	if errRead != nil {
		return nil, "", fmt.Errorf("read multipart form failed: %w", errRead)
	}
	defer func() {
		if errRemove := form.RemoveAll(); errRemove != nil {
			log.Errorf("openai compat executor: remove multipart form files error: %v", errRemove)
		}
	}()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if model != "" {
		if errWrite := writer.WriteField("model", model); errWrite != nil {
			return nil, "", fmt.Errorf("write model field failed: %w", errWrite)
		}
	}
	if stream {
		if errWrite := writer.WriteField("stream", "true"); errWrite != nil {
			return nil, "", fmt.Errorf("write stream field failed: %w", errWrite)
		}
	}
	for key, values := range form.Value {
		if key == "model" || key == "stream" {
			continue
		}
		for _, value := range values {
			if errWrite := writer.WriteField(key, value); errWrite != nil {
				return nil, "", fmt.Errorf("write form field %s failed: %w", key, errWrite)
			}
		}
	}
	for key, files := range form.File {
		for _, fileHeader := range files {
			if fileHeader == nil {
				continue
			}
			header := cloneOpenAICompatMIMEHeader(fileHeader.Header)
			header.Set("Content-Disposition", multipart.FileContentDisposition(key, fileHeader.Filename))
			if header.Get("Content-Type") == "" {
				header.Set("Content-Type", "application/octet-stream")
			}
			part, errCreate := writer.CreatePart(header)
			if errCreate != nil {
				return nil, "", fmt.Errorf("create file field %s failed: %w", key, errCreate)
			}
			src, errOpen := fileHeader.Open()
			if errOpen != nil {
				return nil, "", fmt.Errorf("open upload file failed: %w", errOpen)
			}
			_, errCopy := io.Copy(part, src)
			if errClose := src.Close(); errClose != nil {
				log.Errorf("openai compat executor: close upload file error: %v", errClose)
				if errCopy == nil {
					errCopy = errClose
				}
			}
			if errCopy != nil {
				return nil, "", fmt.Errorf("copy upload file failed: %w", errCopy)
			}
		}
	}
	if errClose := writer.Close(); errClose != nil {
		return nil, "", fmt.Errorf("close multipart writer failed: %w", errClose)
	}
	return body.Bytes(), writer.FormDataContentType(), nil
}

func (e *OpenAICompatExecutor) applyPromptCacheKey(ctx context.Context, auth *cliproxyauth.Auth, from sdktranslator.Format, baseModel string, req cliproxyexecutor.Request, opts cliproxyexecutor.Options, translated []byte) ([]byte, error) {
	compat := e.resolveCompatConfig(auth)
	if compat == nil || !compat.SupportPromptCacheKey {
		return translated, nil
	}

	for _, payload := range [][]byte{req.Payload, opts.OriginalRequest, translated} {
		if promptCacheKey := strings.TrimSpace(gjson.GetBytes(payload, "prompt_cache_key").String()); promptCacheKey != "" {
			return helps.SetStringIfDifferent(translated, "prompt_cache_key", promptCacheKey), nil
		}
	}

	modelName := strings.TrimSpace(gjson.GetBytes(translated, "model").String())
	if modelName == "" {
		modelName = baseModel
	}
	if sourceFormatEqual(from, sdktranslator.FormatClaude) {
		cached, ok, errCache := helps.ClaudeCodePromptCache(ctx, modelName, req.Payload, opts.Headers)
		if errCache != nil {
			return translated, errCache
		}
		if ok {
			return helps.SetStringIfDifferent(translated, "prompt_cache_key", cached.ID), nil
		}
	}

	sessionID := helps.ProviderSessionUUID(e.provider, opts.Metadata, req.Metadata)
	if sessionID == "" {
		return translated, nil
	}
	provider := strings.TrimSpace(e.provider)
	if provider == "" {
		provider = strings.TrimSpace(compat.Name)
	}
	identity := strings.Join([]string{
		"cli-proxy-api:openai-compat:prompt-cache",
		strings.ToLower(provider),
		strings.ToLower(modelName),
		strings.ToLower(strings.TrimSpace(from.String())),
		sessionID,
	}, "\x00")
	promptCacheKey := uuid.NewSHA1(uuid.NameSpaceOID, []byte(identity)).String()
	return helps.SetStringIfDifferent(translated, "prompt_cache_key", promptCacheKey), nil
}

func (e *OpenAICompatExecutor) resolveCredentials(auth *cliproxyauth.Auth) (baseURL, apiKey string) {
	if auth == nil {
		return "", ""
	}
	if auth.Attributes != nil {
		baseURL = strings.TrimSpace(auth.Attributes["base_url"])
		apiKey = strings.TrimSpace(auth.Attributes["api_key"])
	}
	return
}

// normalizeCompatTemperatureForUpstream forces temperature to 1 for Kimi K3
// models reached through OpenAI-compatible providers. The Kimi coding API only
// accepts temperature=1 for the K3 family ("invalid temperature: only 1 is
// allowed for this model"), while OpenAI-format clients commonly send other
// values such as 0.7. The upstream model name (resolved model or request body)
// and the provider base URL are both checked so only Kimi K3 requests change.
func normalizeCompatTemperatureForUpstream(body []byte, upstreamModel, baseURL string) ([]byte, error) {
	if !strings.Contains(strings.ToLower(baseURL), "kimi.com") {
		return body, nil
	}
	if !isKimiK3UpstreamModel(upstreamModel) && !isKimiK3UpstreamModel(gjson.GetBytes(body, "model").String()) {
		return body, nil
	}
	out, err := sjson.SetBytes(body, "temperature", 1)
	if err != nil {
		return body, fmt.Errorf("openai compat executor: failed to force temperature for k3: %w", err)
	}
	return out, nil
}

func (e *OpenAICompatExecutor) resolveCompatConfig(auth *cliproxyauth.Auth) *config.OpenAICompatibility {
	if auth == nil || e.cfg == nil {
		return nil
	}
	if auth.AuthSourceKind() == cliproxyauth.AuthSourceConfig && auth.Attributes != nil {
		if rawIndex := strings.TrimSpace(auth.Attributes["config_index"]); rawIndex != "" {
			configIndex, errIndex := strconv.Atoi(rawIndex)
			if errIndex == nil && configIndex >= 0 && configIndex < len(e.cfg.OpenAICompatibility) {
				compat := &e.cfg.OpenAICompatibility[configIndex]
				if !compat.Disabled {
					return compat
				}
			}
		}
	}
	candidates := make([]string, 0, 3)
	if auth.Attributes != nil {
		if v := strings.TrimSpace(auth.Attributes["compat_name"]); v != "" {
			candidates = append(candidates, v)
		}
		if v := strings.TrimSpace(auth.Attributes["provider_key"]); v != "" {
			candidates = append(candidates, v)
		}
	}
	if v := strings.TrimSpace(auth.Provider); v != "" {
		candidates = append(candidates, v)
	}
	for i := range e.cfg.OpenAICompatibility {
		compat := &e.cfg.OpenAICompatibility[i]
		if compat.Disabled {
			continue
		}
		for _, candidate := range candidates {
			if candidate != "" && strings.EqualFold(strings.TrimSpace(candidate), compat.Name) {
				return compat
			}
		}
	}
	return nil
}

func (e *OpenAICompatExecutor) overrideModel(payload []byte, model string) []byte {
	if len(payload) == 0 || model == "" {
		return payload
	}
	return helps.SetStringIfDifferent(payload, "model", model)
}

func openAICompatErrorEvent(eventName string) bool {
	return strings.EqualFold(eventName, "error") || strings.EqualFold(eventName, "response.error") || strings.EqualFold(eventName, "response.failed")
}

func openAICompatStreamDataError(payload []byte, eventName string) (statusErr, bool) {
	if len(payload) == 0 || !json.Valid(payload) {
		return statusErr{}, false
	}
	payloadType := gjson.GetBytes(payload, "type").String()
	hasError := false
	for _, path := range []string{"error", "response.error"} {
		errorNode := gjson.GetBytes(payload, path)
		if errorNode.Exists() && errorNode.Raw != "null" {
			hasError = true
			break
		}
	}
	hasTopLevelErrorFields := gjson.GetBytes(payload, "code").Exists() && gjson.GetBytes(payload, "message").Exists()
	if !hasError && !strings.EqualFold(payloadType, "error") && !strings.EqualFold(payloadType, "response.error") && !strings.EqualFold(payloadType, "response.failed") &&
		!openAICompatErrorEvent(eventName) && !hasTopLevelErrorFields {
		return statusErr{}, false
	}

	status := 0
	for _, path := range []string{"status", "status_code", "error.status", "error.status_code", "response.error.status", "response.error.status_code"} {
		status = int(gjson.GetBytes(payload, path).Int())
		if status >= http.StatusBadRequest && status <= 599 {
			break
		}
	}
	if status < http.StatusBadRequest || status > 599 {
		status = http.StatusBadGateway
	}
	return statusErr{code: status, msg: string(payload)}, true
}

type statusErr struct {
	code       int
	msg        string
	retryAfter *time.Duration
}

func (e statusErr) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return fmt.Sprintf("status %d", e.code)
}
func (e statusErr) StatusCode() int            { return e.code }
func (e statusErr) RetryAfter() *time.Duration { return e.retryAfter }

// wrapTransientNetworkError maps transient network failures (e.g. unexpected
// EOF, connection resets) to a retryable 502 status error so the auth manager
// can cooldown the model and retry the request. Non-transient errors such as
// oversized stream lines or context cancellation pass through unchanged.
func wrapTransientNetworkError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return statusErr{code: http.StatusBadGateway, msg: err.Error()}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr != nil {
		return statusErr{code: http.StatusBadGateway, msg: err.Error()}
	}
	return err
}

func logXunfeiErrorDetail(ctx context.Context, originalPayload, payload []byte, responseBody []byte) {
	if originalPayload != nil {
		helps.LogWithRequestID(ctx).Debugf("xunfei error detail: request payload (original)=%s", helps.SummarizeErrorBody("application/json", originalPayload))
	}
	helps.LogWithRequestID(ctx).Debugf("xunfei error detail: request payload (sanitized)=%s", helps.SummarizeErrorBody("application/json", payload))
	msgs := gjson.GetBytes(payload, "messages").Array()
	for i, msg := range msgs {
		if msg.Get("role").String() == "assistant" {
			helps.LogWithRequestID(ctx).Debugf("xunfei error detail: message[%d] role=assistant content=%s tool_calls=%s", i, msg.Get("content").Raw, msg.Get("tool_calls").Raw)
		}
	}
	helps.LogWithRequestID(ctx).Debugf("xunfei error detail: xunfei response=%s", string(responseBody))
}

// sanitizeXunfeiPayload cleans the request payload for Xunfei ModelArts compatibility.
// Xunfei ModelArts rejects requests with error code 10012 in two cases:
//  1. Assistant messages with empty content ("") and no tool_calls → removed entirely.
//  2. Assistant messages with both non-empty content and tool_calls → content set to null
//     (matching OpenAI convention for tool-call-only turns).
func sanitizeXunfeiPayload(translated []byte) []byte {
	messages := gjson.GetBytes(translated, "messages").Array()

	type contentFix struct {
		index int
	}
	var contentFixes []contentFix
	var removeIndices []int

	for i, msg := range messages {
		if msg.Get("role").String() != "assistant" {
			continue
		}

		contentVal := msg.Get("content")
		toolCallsVal := msg.Get("tool_calls")

		hasContent := contentVal.Exists() && contentVal.Type != gjson.Null
		if contentVal.Type == gjson.String && contentVal.String() == "" {
			hasContent = false
		}

		hasToolCalls := toolCallsVal.Exists() && toolCallsVal.Type != gjson.Null &&
			toolCallsVal.IsArray() && len(toolCallsVal.Array()) > 0

		if !hasContent && !hasToolCalls {
			removeIndices = append(removeIndices, i)
			log.Debugf("xunfei sanitize: removing empty assistant message[%d]", i)
			continue
		}

		if hasContent && hasToolCalls {
			contentFixes = append(contentFixes, contentFix{index: i})
		}
	}

	for _, fix := range contentFixes {
		key := fmt.Sprintf("messages.%d.content", fix.index)
		updated, err := sjson.SetBytes(translated, key, nil)
		if err == nil {
			translated = updated
			log.Debugf("xunfei sanitize: set content=null on assistant message[%d] (had both content and tool_calls)", fix.index)
		}
	}

	for i := len(removeIndices) - 1; i >= 0; i-- {
		key := fmt.Sprintf("messages.%d", removeIndices[i])
		updated, err := sjson.DeleteBytes(translated, key)
		if err == nil {
			translated = updated
		}
	}

	if len(removeIndices) > 0 {
		log.Debugf("xunfei sanitize: removed %d empty assistant messages at original indices %v", len(removeIndices), removeIndices)
	}
	if len(contentFixes) > 0 {
		log.Debugf("xunfei sanitize: set content=null on %d assistant messages with both content and tool_calls", len(contentFixes))
	}

	return translated
}

type xunfeiErrorClass int

const (
	xunfeiErrNone xunfeiErrorClass = iota
	xunfeiErrRetryable
	xunfeiErrOverloaded
	xunfeiErrBadRequest
	xunfeiErrForbidden
)

var xunfeiRetryableCodes = map[int]struct{}{
	10010: {},
	10012: {},
	10222: {},
	11210: {},
}

func classifyXunfeiError(body []byte) xunfeiErrorClass {
	if len(body) == 0 {
		return xunfeiErrNone
	}
	if !bytes.Contains(body, []byte(`"code"`)) && !bytes.Contains(body, []byte(`"Code"`)) {
		return xunfeiErrNone
	}

	var nested struct {
		Error struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &nested); err == nil && nested.Error.Code != 0 {
		if _, ok := xunfeiRetryableCodes[nested.Error.Code]; ok {
			return classifyXunfeiSubType(nested.Error.Code, nested.Error.Message)
		}
	}

	var flat struct {
		Code      int    `json:"code"`
		Message   string `json:"message"`
		Msg       string `json:"msg"`
		Sid       string `json:"Sid"`
		TimeStamp string `json:"timeStamp"`
	}
	if err := json.Unmarshal(body, &flat); err == nil && flat.Code != 0 {
		msg := flat.Message
		if msg == "" {
			msg = flat.Msg
		}
		if _, ok := xunfeiRetryableCodes[flat.Code]; ok {
			return classifyXunfeiSubType(flat.Code, msg)
		}
	}

	return xunfeiErrNone
}

func classifyXunfeiSubType(code int, message string) xunfeiErrorClass {
	// 10010: Engine Busy → overloaded (503), not quota-related
	if code == 10010 {
		return xunfeiErrOverloaded
	}
	// 10222: AbnormalNetworkError (gRPC EOF/unavailable) → overloaded (503), transient infra error
	if code == 10222 {
		return xunfeiErrOverloaded
	}
	// 11210: Insufficient credits → retryable (429), quota-related
	if code == 11210 {
		return xunfeiErrRetryable
	}
	// 10012: multi-meaning code — classify by message content
	if strings.Contains(message, "status code: 400") || strings.Contains(message, "must have 'content' or 'tool_calls'") {
		return xunfeiErrBadRequest
	}
	if strings.Contains(message, "status code: 403") || strings.Contains(message, "has not activated") {
		return xunfeiErrForbidden
	}
	// 10012 with "busy" or "system busy" → overloaded (503), not quota-related
	if strings.Contains(strings.ToLower(message), "busy") {
		return xunfeiErrOverloaded
	}
	// 10012 fallback: treat as overloaded since the most common 10012 is system busy
	return xunfeiErrOverloaded
}

func xunfeiStatusErr(cls xunfeiErrorClass, rawBody string) statusErr {
	switch cls {
	case xunfeiErrBadRequest:
		return statusErr{code: http.StatusBadRequest, msg: "Xunfei API error (code 10012): Bad request - assistant message must have 'content' or 'tool_calls'"}
	case xunfeiErrForbidden:
		return statusErr{code: http.StatusForbidden, msg: "Xunfei API error (code 10012): Forbidden - model not activated or account lacks permission"}
	case xunfeiErrOverloaded:
		return statusErr{code: http.StatusServiceUnavailable, msg: "Xunfei API error (code 10010/10012): Engine busy or system overloaded, please try again later"}
	case xunfeiErrRetryable:
		retryAfter := time.Duration(0)
		return statusErr{code: http.StatusTooManyRequests, msg: "Xunfei API error (code 11210): Insufficient credits or rate limit exceeded, please try again later", retryAfter: &retryAfter}
	default:
		return statusErr{code: http.StatusInternalServerError, msg: rawBody}
	}
}

func xunfeiStatusErrWithRetryAfter(cls xunfeiErrorClass, retryAfter time.Duration) statusErr {
	err := xunfeiStatusErr(cls, "")
	err.retryAfter = &retryAfter
	return err
}

func isXunfeiRetryableOrOverloaded(body []byte) bool {
	cls := classifyXunfeiError(body)
	return cls == xunfeiErrRetryable || cls == xunfeiErrOverloaded
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isXunfeiRetryableError(body []byte) bool {
	return classifyXunfeiError(body) == xunfeiErrRetryable
}
