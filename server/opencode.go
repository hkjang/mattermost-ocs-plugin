package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const serviceLabel = "OpenCode"

type openCodePart struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type openCodeMessageRequest struct {
	MessageID string         `json:"messageID,omitempty"`
	Model     string         `json:"model,omitempty"`
	Agent     string         `json:"agent,omitempty"`
	NoReply   bool           `json:"noReply,omitempty"`
	System    string         `json:"system,omitempty"`
	Tools     any            `json:"tools,omitempty"`
	Parts     []openCodePart `json:"parts"`
}

type openCodeMessageEnvelope struct {
	Info  map[string]any   `json:"info"`
	Parts []map[string]any `json:"parts"`
}

type openCodeSession struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

type openCodeHealth struct {
	Healthy bool   `json:"healthy"`
	Version string `json:"version"`
}

type openCodeAgent struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

type openCodeProvider struct {
	ID     string `json:"id"`
	Name   string `json:"name,omitempty"`
	Models []any  `json:"models,omitempty"`
}

type openCodeProviderResponse struct {
	All       []openCodeProvider `json:"all"`
	Default   map[string]string  `json:"default"`
	Connected []string           `json:"connected"`
}

type openCodeAgentSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

type openCodeProviderSummary struct {
	ID           string   `json:"id"`
	Name         string   `json:"name,omitempty"`
	Connected    bool     `json:"connected"`
	DefaultModel string   `json:"default_model,omitempty"`
	Models       []string `json:"models,omitempty"`
}

type openCodeConnectionStatus struct {
	OK         bool                      `json:"ok"`
	URL        string                    `json:"url"`
	StatusCode int                       `json:"status_code"`
	Message    string                    `json:"message"`
	ErrorCode  string                    `json:"error_code,omitempty"`
	Detail     string                    `json:"detail,omitempty"`
	Hint       string                    `json:"hint,omitempty"`
	Retryable  bool                      `json:"retryable"`
	Healthy    bool                      `json:"healthy"`
	Version    string                    `json:"version,omitempty"`
	Agents     []openCodeAgentSummary    `json:"agents,omitempty"`
	Providers  []openCodeProviderSummary `json:"providers,omitempty"`
}

type openCodeStreamEvent struct {
	Type       string         `json:"type,omitempty"`
	Event      string         `json:"event,omitempty"`
	Data       any            `json:"data,omitempty"`
	Payload    any            `json:"payload,omitempty"`
	Properties map[string]any `json:"properties,omitempty"`
}

type openCodeStreamParser struct{}

type serviceCallError struct {
	Code       string
	Summary    string
	Detail     string
	Hint       string
	RequestURL string
	StatusCode int
	Retryable  bool
}

func (e *serviceCallError) Error() string {
	if e == nil {
		return ""
	}

	lines := []string{}
	if e.Summary != "" {
		lines = append(lines, e.Summary)
	}
	if e.Detail != "" {
		lines = append(lines, "Detail: "+e.Detail)
	}
	if e.Hint != "" {
		lines = append(lines, "Hint: "+e.Hint)
	}
	if e.StatusCode > 0 {
		lines = append(lines, fmt.Sprintf("HTTP status: %d", e.StatusCode))
	}
	if e.RequestURL != "" {
		lines = append(lines, "Request URL: "+e.RequestURL)
	}

	return strings.Join(lines, "\n")
}

func (e *serviceCallError) toConnectionStatus() *openCodeConnectionStatus {
	if e == nil {
		return &openCodeConnectionStatus{}
	}
	return &openCodeConnectionStatus{
		OK:         false,
		URL:        e.RequestURL,
		StatusCode: e.StatusCode,
		Message:    e.Summary,
		ErrorCode:  e.Code,
		Detail:     e.Detail,
		Hint:       e.Hint,
		Retryable:  e.Retryable,
	}
}

func (p *Plugin) invokeOpenCode(
	ctx context.Context,
	cfg *runtimeConfiguration,
	sessionID string,
	request openCodeMessageRequest,
) (string, int, error) {
	requestBody, err := json.Marshal(request)
	if err != nil {
		return "", 0, fmt.Errorf("failed to encode OpenCode message payload: %w", err)
	}

	httpRequest, err := p.newOpenCodeRequest(ctx, cfg, http.MethodPost, []string{"session", sessionID, "message"}, requestBody, "application/json")
	if err != nil {
		return "", 0, err
	}

	client := p.newOpenCodeClient(cfg.DefaultTimeout)
	response, err := client.Do(httpRequest)
	if err != nil {
		return "", 0, classifyRequestError(serviceLabel, httpRequest.URL.String(), err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, int64(cfg.MaxOutputLength*8)))
	if err != nil {
		return "", response.StatusCode, newServiceCallError(
			"response_read_failed",
			"OpenCode returned an unreadable response.",
			err.Error(),
			"Check the OpenCode server logs and reverse proxy settings.",
			httpRequest.URL.String(),
			response.StatusCode,
			true,
		)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return "", response.StatusCode, classifyHTTPError(serviceLabel, httpRequest.URL.String(), response.StatusCode, response.Header, responseBody)
	}
	if looksLikeHTMLResponse(response.Header.Get("Content-Type"), responseBody) {
		return "", response.StatusCode, newUnexpectedHTMLResponseError(serviceLabel, httpRequest.URL.String())
	}

	var envelope openCodeMessageEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return "", response.StatusCode, newServiceCallError(
			"decode_failed",
			"OpenCode returned a response that could not be decoded.",
			err.Error(),
			"Verify the OpenCode server version and response shape.",
			httpRequest.URL.String(),
			response.StatusCode,
			false,
		)
	}

	output := extractOpenCodeMessageText(envelope.Parts)
	if output == "" {
		output = extractStructuredTextFromValue(envelope)
	}
	output = truncateString(output, cfg.MaxOutputLength)
	if output == "" {
		return "", response.StatusCode, newServiceCallError(
			"empty_response",
			"OpenCode completed without returning assistant text.",
			"",
			"Verify the selected model or agent is producing text output.",
			httpRequest.URL.String(),
			response.StatusCode,
			false,
		)
	}

	return output, response.StatusCode, nil
}

func (p *Plugin) invokeOpenCodeStream(
	ctx context.Context,
	cfg *runtimeConfiguration,
	sessionID string,
	request openCodeMessageRequest,
	onUpdate func(string, bool),
) (string, int, error) {
	streamRequest, err := p.newOpenCodeRequest(ctx, cfg, http.MethodGet, []string{"event"}, nil, "text/event-stream")
	if err != nil {
		return "", 0, err
	}

	streamClient := p.newOpenCodeClient(0)
	streamResponse, err := streamClient.Do(streamRequest)
	if err != nil {
		return "", 0, classifyRequestError(serviceLabel, streamRequest.URL.String(), err)
	}
	if streamResponse.StatusCode >= http.StatusBadRequest {
		defer streamResponse.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(streamResponse.Body, 8192))
		return "", streamResponse.StatusCode, classifyHTTPError(serviceLabel, streamRequest.URL.String(), streamResponse.StatusCode, streamResponse.Header, body)
	}

	asyncBody, err := json.Marshal(request)
	if err != nil {
		streamResponse.Body.Close()
		return "", 0, fmt.Errorf("failed to encode OpenCode async message payload: %w", err)
	}

	asyncRequest, err := p.newOpenCodeRequest(ctx, cfg, http.MethodPost, []string{"session", sessionID, "prompt_async"}, asyncBody, "application/json")
	if err != nil {
		streamResponse.Body.Close()
		return "", 0, err
	}

	asyncClient := p.newOpenCodeClient(cfg.DefaultTimeout)
	asyncResponse, err := asyncClient.Do(asyncRequest)
	if err != nil {
		streamResponse.Body.Close()
		return "", 0, classifyRequestError(serviceLabel, asyncRequest.URL.String(), err)
	}
	statusCode := asyncResponse.StatusCode
	if asyncResponse.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(asyncResponse.Body, 8192))
		asyncResponse.Body.Close()
		streamResponse.Body.Close()
		return "", asyncResponse.StatusCode, classifyHTTPError(serviceLabel, asyncRequest.URL.String(), asyncResponse.StatusCode, asyncResponse.Header, body)
	}
	io.Copy(io.Discard, asyncResponse.Body)
	asyncResponse.Body.Close()

	type streamItem struct {
		event *openCodeStreamEvent
		raw   string
		err   error
	}

	items := make(chan streamItem)
	go func() {
		defer close(items)
		defer streamResponse.Body.Close()

		parser := openCodeStreamParser{}
		reader := bufio.NewReader(streamResponse.Body)
		for {
			event, rawEvent, parseErr := parser.readEvent(reader)
			if parseErr != nil {
				if errors.Is(parseErr, io.EOF) {
					return
				}
				items <- streamItem{err: parseErr}
				return
			}
			items <- streamItem{event: event, raw: rawEvent}
		}
	}()

	idleWindow := maxDuration(2*time.Second, minDuration(8*time.Second, maxDuration(cfg.StreamingUpdateInterval*4, 2*time.Second)))
	timer := time.NewTimer(idleWindow)
	defer timer.Stop()

	currentOutput := ""
	for {
		select {
		case item, ok := <-items:
			if !ok {
				if currentOutput != "" {
					if latest, latestErr := p.getLatestOpenCodeReply(ctx, cfg, sessionID); latestErr == nil && latest != "" {
						currentOutput = mergeOpenCodeStreamOutput(currentOutput, latest)
					}
					currentOutput = truncateString(currentOutput, cfg.MaxOutputLength)
					if onUpdate != nil {
						onUpdate(currentOutput, true)
					}
					return currentOutput, statusCode, nil
				}
				return "", statusCode, newServiceCallError(
					"stream_closed",
					"OpenCode closed the event stream before any text was received.",
					"",
					"Check the OpenCode server logs and retry the request.",
					streamRequest.URL.String(),
					statusCode,
					true,
				)
			}
			if item.err != nil {
				if currentOutput != "" {
					currentOutput = truncateString(currentOutput, cfg.MaxOutputLength)
					if onUpdate != nil {
						onUpdate(currentOutput, true)
					}
					return currentOutput, statusCode, nil
				}
				return "", statusCode, newServiceCallError(
					"stream_parse_failed",
					"OpenCode returned a stream event that could not be parsed.",
					item.err.Error(),
					"Verify the OpenCode event stream is reachable and returns valid SSE data.",
					streamRequest.URL.String(),
					statusCode,
					true,
				)
			}
			if item.event == nil || !eventBelongsToSession(item.raw, item.event, sessionID) {
				continue
			}

			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idleWindow)

			update, completed := extractOpenCodeStreamText(*item.event)
			if update == "" {
				continue
			}

			currentOutput = truncateString(mergeOpenCodeStreamOutput(currentOutput, update), cfg.MaxOutputLength)
			if onUpdate != nil && currentOutput != "" {
				onUpdate(currentOutput, false)
			}
			if completed {
				if latest, latestErr := p.getLatestOpenCodeReply(ctx, cfg, sessionID); latestErr == nil && latest != "" {
					currentOutput = truncateString(mergeOpenCodeStreamOutput(currentOutput, latest), cfg.MaxOutputLength)
				}
				if onUpdate != nil && currentOutput != "" {
					onUpdate(currentOutput, true)
				}
				return currentOutput, statusCode, nil
			}
		case <-timer.C:
			if currentOutput != "" {
				if latest, latestErr := p.getLatestOpenCodeReply(ctx, cfg, sessionID); latestErr == nil && latest != "" {
					currentOutput = truncateString(mergeOpenCodeStreamOutput(currentOutput, latest), cfg.MaxOutputLength)
				}
				if onUpdate != nil {
					onUpdate(currentOutput, true)
				}
				return currentOutput, statusCode, nil
			}
			timer.Reset(idleWindow)
		case <-ctx.Done():
			if currentOutput != "" {
				currentOutput = truncateString(currentOutput, cfg.MaxOutputLength)
				if onUpdate != nil {
					onUpdate(currentOutput, true)
				}
				return currentOutput, statusCode, nil
			}
			return "", statusCode, classifyRequestError(serviceLabel, asyncRequest.URL.String(), ctx.Err())
		}
	}
}

func (p *Plugin) createOpenCodeSession(ctx context.Context, cfg *runtimeConfiguration, title string) (*openCodeSession, error) {
	body, err := json.Marshal(map[string]any{"title": title})
	if err != nil {
		return nil, fmt.Errorf("failed to encode OpenCode session payload: %w", err)
	}

	request, err := p.newOpenCodeRequest(ctx, cfg, http.MethodPost, []string{"session"}, body, "application/json")
	if err != nil {
		return nil, err
	}

	client := p.newOpenCodeClient(cfg.DefaultTimeout)
	response, err := client.Do(request)
	if err != nil {
		return nil, classifyRequestError(serviceLabel, request.URL.String(), err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenCode session response: %w", err)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return nil, classifyHTTPError(serviceLabel, request.URL.String(), response.StatusCode, response.Header, responseBody)
	}

	var session openCodeSession
	if err := json.Unmarshal(responseBody, &session); err != nil {
		return nil, fmt.Errorf("failed to decode OpenCode session response: %w", err)
	}
	if session.ID == "" {
		return nil, fmt.Errorf("OpenCode session response did not include an id")
	}
	return &session, nil
}

func (p *Plugin) deleteOpenCodeSession(ctx context.Context, cfg *runtimeConfiguration, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}

	request, err := p.newOpenCodeRequest(ctx, cfg, http.MethodDelete, []string{"session", sessionID}, nil, "application/json")
	if err != nil {
		return err
	}

	client := p.newOpenCodeClient(minDuration(cfg.DefaultTimeout, 10*time.Second))
	response, err := client.Do(request)
	if err != nil {
		return classifyRequestError(serviceLabel, request.URL.String(), err)
	}
	defer response.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
	if response.StatusCode >= http.StatusBadRequest {
		return classifyHTTPError(serviceLabel, request.URL.String(), response.StatusCode, response.Header, body)
	}
	return nil
}

func (p *Plugin) abortOpenCodeSession(ctx context.Context, cfg *runtimeConfiguration, sessionID string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false, nil
	}

	request, err := p.newOpenCodeRequest(ctx, cfg, http.MethodPost, []string{"session", sessionID, "abort"}, []byte(`{}`), "application/json")
	if err != nil {
		return false, err
	}

	client := p.newOpenCodeClient(minDuration(cfg.DefaultTimeout, 10*time.Second))
	response, err := client.Do(request)
	if err != nil {
		return false, classifyRequestError(serviceLabel, request.URL.String(), err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4096))
	if err != nil {
		return false, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return false, classifyHTTPError(serviceLabel, request.URL.String(), response.StatusCode, response.Header, responseBody)
	}

	if len(bytes.TrimSpace(responseBody)) == 0 {
		return true, nil
	}

	var ok bool
	if err := json.Unmarshal(responseBody, &ok); err == nil {
		return ok, nil
	}
	return true, nil
}

func (p *Plugin) getLatestOpenCodeReply(ctx context.Context, cfg *runtimeConfiguration, sessionID string) (string, error) {
	request, err := p.newOpenCodeRequest(ctx, cfg, http.MethodGet, []string{"session", sessionID, "message"}, nil, "application/json")
	if err != nil {
		return "", err
	}

	query := request.URL.Query()
	query.Set("limit", "20")
	request.URL.RawQuery = query.Encode()

	client := p.newOpenCodeClient(minDuration(cfg.DefaultTimeout, 10*time.Second))
	response, err := client.Do(request)
	if err != nil {
		return "", classifyRequestError(serviceLabel, request.URL.String(), err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, int64(cfg.MaxOutputLength*8)))
	if err != nil {
		return "", err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return "", classifyHTTPError(serviceLabel, request.URL.String(), response.StatusCode, response.Header, responseBody)
	}

	var envelopes []openCodeMessageEnvelope
	if err := json.Unmarshal(responseBody, &envelopes); err != nil {
		return "", err
	}
	return extractLatestAssistantText(envelopes), nil
}

func (p *Plugin) testOpenCodeConnection(ctx context.Context, cfg *runtimeConfiguration) (*openCodeConnectionStatus, error) {
	if cfg.ParsedBaseURL == nil {
		return &openCodeConnectionStatus{OK: false, Message: "OpenCode base URL is not configured."}, nil
	}
	if !hostAllowed(cfg.ParsedBaseURL.Hostname(), cfg.AllowHosts) {
		return &openCodeConnectionStatus{
			OK:        false,
			URL:       cfg.ParsedBaseURL.String(),
			Message:   "The configured host is blocked by the allow list.",
			Detail:    fmt.Sprintf("Host %q is not included in allow_hosts.", cfg.ParsedBaseURL.Hostname()),
			Hint:      "Update allow_hosts or correct the base URL in the plugin settings.",
			Retryable: false,
		}, nil
	}

	healthRequest, err := p.newOpenCodeRequest(ctx, cfg, http.MethodGet, []string{"global", "health"}, nil, "application/json")
	if err != nil {
		return nil, err
	}

	client := p.newOpenCodeClient(minDuration(cfg.DefaultTimeout, 10*time.Second))
	healthResponse, err := client.Do(healthRequest)
	if err != nil {
		return classifyRequestError(serviceLabel, healthRequest.URL.String(), err).toConnectionStatus(), nil
	}
	defer healthResponse.Body.Close()

	healthBody, _ := io.ReadAll(io.LimitReader(healthResponse.Body, 4096))
	if healthResponse.StatusCode >= http.StatusBadRequest {
		return classifyHTTPError(serviceLabel, healthRequest.URL.String(), healthResponse.StatusCode, healthResponse.Header, healthBody).toConnectionStatus(), nil
	}

	var health openCodeHealth
	if err := json.Unmarshal(healthBody, &health); err != nil {
		return &openCodeConnectionStatus{
			OK:         false,
			URL:        healthRequest.URL.String(),
			StatusCode: healthResponse.StatusCode,
			Message:    "OpenCode health returned an unreadable payload.",
			Detail:     err.Error(),
			Retryable:  false,
		}, nil
	}

	agents, agentsErr := p.listOpenCodeAgents(ctx, cfg)
	providers, providersErr := p.listOpenCodeProviders(ctx, cfg)

	status := &openCodeConnectionStatus{
		OK:         health.Healthy,
		URL:        cfg.OpenCodeBaseURL,
		StatusCode: healthResponse.StatusCode,
		Message:    "OpenCode is reachable.",
		Healthy:    health.Healthy,
		Version:    health.Version,
	}
	if !health.Healthy {
		status.Message = "OpenCode responded, but reported itself as unhealthy."
	}
	if agentsErr == nil {
		status.Agents = agents
	}
	if providersErr == nil {
		status.Providers = providers
	}
	if agentsErr != nil || providersErr != nil {
		details := []string{}
		if agentsErr != nil {
			details = append(details, "agent list: "+agentsErr.Error())
		}
		if providersErr != nil {
			details = append(details, "provider list: "+providersErr.Error())
		}
		status.Detail = strings.Join(details, " | ")
		status.Hint = "Health succeeded, but one or more catalog endpoints could not be loaded."
	}
	return status, nil
}

func (p *Plugin) listOpenCodeAgents(ctx context.Context, cfg *runtimeConfiguration) ([]openCodeAgentSummary, error) {
	request, err := p.newOpenCodeRequest(ctx, cfg, http.MethodGet, []string{"agent"}, nil, "application/json")
	if err != nil {
		return nil, err
	}

	client := p.newOpenCodeClient(minDuration(cfg.DefaultTimeout, 10*time.Second))
	response, err := client.Do(request)
	if err != nil {
		return nil, classifyRequestError(serviceLabel, request.URL.String(), err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 8192))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return nil, classifyHTTPError(serviceLabel, request.URL.String(), response.StatusCode, response.Header, responseBody)
	}

	var agents []openCodeAgent
	if err := json.Unmarshal(responseBody, &agents); err != nil {
		return nil, err
	}

	summaries := make([]openCodeAgentSummary, 0, len(agents))
	for _, agent := range agents {
		summaries = append(summaries, openCodeAgentSummary{
			ID:          strings.TrimSpace(agent.ID),
			Name:        strings.TrimSpace(agent.Name),
			Description: strings.TrimSpace(agent.Description),
		})
	}
	return summaries, nil
}

func (p *Plugin) listOpenCodeProviders(ctx context.Context, cfg *runtimeConfiguration) ([]openCodeProviderSummary, error) {
	request, err := p.newOpenCodeRequest(ctx, cfg, http.MethodGet, []string{"provider"}, nil, "application/json")
	if err != nil {
		return nil, err
	}

	client := p.newOpenCodeClient(minDuration(cfg.DefaultTimeout, 10*time.Second))
	response, err := client.Do(request)
	if err != nil {
		return nil, classifyRequestError(serviceLabel, request.URL.String(), err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 16384))
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= http.StatusBadRequest {
		return nil, classifyHTTPError(serviceLabel, request.URL.String(), response.StatusCode, response.Header, responseBody)
	}

	var payload openCodeProviderResponse
	if err := json.Unmarshal(responseBody, &payload); err != nil {
		return nil, err
	}

	connected := map[string]struct{}{}
	for _, providerID := range payload.Connected {
		connected[strings.TrimSpace(providerID)] = struct{}{}
	}

	summaries := make([]openCodeProviderSummary, 0, len(payload.All))
	for _, provider := range payload.All {
		providerID := strings.TrimSpace(provider.ID)
		_, isConnected := connected[providerID]
		summaries = append(summaries, openCodeProviderSummary{
			ID:           providerID,
			Name:         strings.TrimSpace(provider.Name),
			Connected:    isConnected,
			DefaultModel: payload.Default[providerID],
			Models:       extractProviderModelIDs(providerID, provider.Models),
		})
	}
	return summaries, nil
}

func (p *Plugin) doOpenCodeJSONRequest(
	ctx context.Context,
	cfg *runtimeConfiguration,
	method string,
	segments []string,
	query map[string]string,
	payload any,
	maxBytes int,
) ([]byte, int, error) {
	var requestBody []byte
	var err error
	if payload != nil {
		requestBody, err = json.Marshal(payload)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to encode OpenCode request payload: %w", err)
		}
	}

	request, err := p.newOpenCodeRequest(ctx, cfg, method, segments, requestBody, "application/json")
	if err != nil {
		return nil, 0, err
	}

	if len(query) > 0 {
		values := request.URL.Query()
		for key, value := range query {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			values.Set(key, value)
		}
		request.URL.RawQuery = values.Encode()
	}

	client := p.newOpenCodeClient(0)
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, classifyRequestError(serviceLabel, request.URL.String(), err)
	}
	defer response.Body.Close()

	if maxBytes <= 0 {
		maxBytes = 16384
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, int64(maxBytes)))
	if err != nil {
		return nil, response.StatusCode, newServiceCallError(
			"response_read_failed",
			"OpenCode returned an unreadable response.",
			err.Error(),
			"Check the OpenCode server logs and retry the request.",
			request.URL.String(),
			response.StatusCode,
			true,
		)
	}
	if response.StatusCode >= http.StatusBadRequest {
		return nil, response.StatusCode, classifyHTTPError(serviceLabel, request.URL.String(), response.StatusCode, response.Header, responseBody)
	}
	if looksLikeHTMLResponse(response.Header.Get("Content-Type"), responseBody) {
		return nil, response.StatusCode, newUnexpectedHTMLResponseError(serviceLabel, request.URL.String())
	}
	return responseBody, response.StatusCode, nil
}

func (p *Plugin) invokeOpenCodeShellCommand(
	ctx context.Context,
	cfg *runtimeConfiguration,
	sessionID, agentID, modelID, command string,
) (string, int, error) {
	responseBody, statusCode, err := p.doOpenCodeJSONRequest(ctx, cfg, http.MethodPost, []string{"session", sessionID, "shell"}, nil, map[string]any{
		"agent":   strings.TrimSpace(agentID),
		"model":   strings.TrimSpace(modelID),
		"command": strings.TrimSpace(command),
	}, int(cfg.MaxOutputLength*8))
	if err != nil {
		return "", statusCode, err
	}

	var envelope openCodeMessageEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return "", statusCode, newServiceCallError(
			"decode_failed",
			"OpenCode returned a shell response that could not be decoded.",
			err.Error(),
			"Verify the OpenCode server version and shell response shape.",
			buildOpenCodeURL(cfg.ParsedBaseURL, "session", sessionID, "shell").String(),
			statusCode,
			false,
		)
	}

	output := extractOpenCodeMessageText(envelope.Parts)
	if output == "" {
		output = extractStructuredTextFromValue(envelope)
	}
	return truncateString(output, cfg.MaxOutputLength), statusCode, nil
}

func (p *Plugin) invokeOpenCodeSessionCommand(
	ctx context.Context,
	cfg *runtimeConfiguration,
	sessionID, agentID, modelID, command string,
	arguments []string,
) (string, int, error) {
	responseBody, statusCode, err := p.doOpenCodeJSONRequest(ctx, cfg, http.MethodPost, []string{"session", sessionID, "command"}, nil, map[string]any{
		"agent":     strings.TrimSpace(agentID),
		"model":     strings.TrimSpace(modelID),
		"command":   strings.TrimSpace(command),
		"arguments": arguments,
	}, int(cfg.MaxOutputLength*8))
	if err != nil {
		return "", statusCode, err
	}

	var envelope openCodeMessageEnvelope
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return "", statusCode, newServiceCallError(
			"decode_failed",
			"OpenCode returned a command response that could not be decoded.",
			err.Error(),
			"Verify the OpenCode server version and command response shape.",
			buildOpenCodeURL(cfg.ParsedBaseURL, "session", sessionID, "command").String(),
			statusCode,
			false,
		)
	}

	output := extractOpenCodeMessageText(envelope.Parts)
	if output == "" {
		output = extractStructuredTextFromValue(envelope)
	}
	return truncateString(output, cfg.MaxOutputLength), statusCode, nil
}

func extractProviderModelIDs(providerID string, models []any) []string {
	normalized := make([]string, 0, len(models))
	seen := map[string]struct{}{}
	appendModel := func(modelID string) {
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			return
		}
		if providerID != "" && !strings.Contains(modelID, "/") {
			modelID = providerID + "/" + modelID
		}
		if _, ok := seen[modelID]; ok {
			return
		}
		seen[modelID] = struct{}{}
		normalized = append(normalized, modelID)
	}

	for _, item := range models {
		switch typed := item.(type) {
		case string:
			appendModel(typed)
		case map[string]any:
			appendModel(firstNonEmptyString(
				stringifyValue(typed["id"]),
				stringifyValue(typed["modelID"]),
				stringifyValue(typed["modelId"]),
				stringifyValue(typed["name"]),
			))
		}
	}

	return normalized
}

func (p *Plugin) newOpenCodeClient(timeout time.Duration) *http.Client {
	client := &http.Client{}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}

func (p *Plugin) newOpenCodeRequest(
	ctx context.Context,
	cfg *runtimeConfiguration,
	method string,
	segments []string,
	body []byte,
	accept string,
) (*http.Request, error) {
	if cfg.ParsedBaseURL == nil {
		return nil, fmt.Errorf("OpenCode base URL is not configured")
	}
	if !hostAllowed(cfg.ParsedBaseURL.Hostname(), cfg.AllowHosts) {
		return nil, fmt.Errorf("OpenCode host %q is not allowed by configuration", cfg.ParsedBaseURL.Hostname())
	}

	targetURL := buildOpenCodeURL(cfg.ParsedBaseURL, segments...)
	var reader io.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	}

	request, err := http.NewRequestWithContext(ctx, method, targetURL.String(), reader)
	if err != nil {
		return nil, fmt.Errorf("failed to build OpenCode request: %w", err)
	}
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if accept != "" {
		request.Header.Set("Accept", accept)
	}
	p.applyBasicAuth(request, cfg)
	return request, nil
}

func (p *Plugin) applyBasicAuth(request *http.Request, cfg *runtimeConfiguration) {
	if request == nil || cfg == nil {
		return
	}
	if cfg.OpenCodeUsername == "" && cfg.OpenCodePassword == "" {
		return
	}
	request.SetBasicAuth(cfg.OpenCodeUsername, cfg.OpenCodePassword)
}

func buildOpenCodeURL(baseURL *url.URL, segments ...string) *url.URL {
	baseSegments := normalizeOpenCodeBasePathSegments(baseURL)
	targetSegments := append(baseSegments, segments...)
	return buildURLWithPathSegments(baseURL, targetSegments...)
}

func normalizeOpenCodeBasePathSegments(baseURL *url.URL) []string {
	if baseURL == nil {
		return nil
	}
	segments := splitURLPathSegments(baseURL.Path)
	count := len(segments)
	switch {
	case count >= 2 && strings.EqualFold(segments[count-2], "global") && strings.EqualFold(segments[count-1], "health"):
		return append([]string{}, segments[:count-2]...)
	case count >= 1 && (strings.EqualFold(segments[count-1], "doc") || strings.EqualFold(segments[count-1], "event")):
		return append([]string{}, segments[:count-1]...)
	default:
		return segments
	}
}

func eventBelongsToSession(raw string, event *openCodeStreamEvent, sessionID string) bool {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return false
	}
	if strings.Contains(raw, sessionID) {
		return true
	}
	if event == nil {
		return false
	}
	for _, candidate := range []any{event.Data, event.Payload, event.Properties, event} {
		if containsStringValue(candidate, sessionID) {
			return true
		}
	}
	return false
}

func containsStringValue(value any, needle string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, nested := range typed {
			if containsStringValue(nested, needle) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if containsStringValue(item, needle) {
				return true
			}
		}
	case string:
		return strings.Contains(typed, needle)
	case *openCodeStreamEvent:
		if typed == nil {
			return false
		}
		return containsStringValue(map[string]any{
			"type":       typed.Type,
			"event":      typed.Event,
			"data":       typed.Data,
			"payload":    typed.Payload,
			"properties": typed.Properties,
		}, needle)
	}
	return false
}

func extractOpenCodeStreamText(event openCodeStreamEvent) (string, bool) {
	eventType := strings.ToLower(strings.TrimSpace(defaultIfEmpty(event.Type, event.Event)))
	completed := strings.Contains(eventType, "finish") || strings.Contains(eventType, "completed")

	for _, candidate := range []any{event.Data, event.Payload, event.Properties} {
		if parts := collectParts(candidate); len(parts) > 0 {
			if text := extractOpenCodeMessageText(parts); text != "" {
				if partsIndicateCompletion(parts) {
					completed = true
				}
				return text, completed
			}
		}
	}

	partLike := collectPartLikeMaps(event.Data)
	partLike = append(partLike, collectPartLikeMaps(event.Payload)...)
	partLike = append(partLike, collectPartLikeMaps(event.Properties)...)

	snapshot := ""
	delta := ""
	for _, part := range partLike {
		partType := strings.ToLower(stringifyValue(part["type"]))
		if partType == "" {
			continue
		}
		if strings.Contains(partType, "tool") || strings.Contains(partType, "permission") || strings.Contains(partType, "step") {
			continue
		}
		if strings.Contains(partType, "finish") || strings.Contains(partType, "text-end") {
			completed = true
		}
		text := firstNonEmptyString(
			stringifyValue(part["text"]),
			stringifyValue(part["delta"]),
			stringifyValue(part["content"]),
			stringifyValue(part["message"]),
			stringifyValue(part["output"]),
		)
		if text == "" {
			text = extractStructuredTextFromValue(part)
		}
		if text == "" {
			continue
		}
		if strings.Contains(partType, "delta") {
			delta += text
			continue
		}
		if len(text) > len(snapshot) {
			snapshot = text
		}
	}

	switch {
	case snapshot != "":
		return snapshot, completed
	case delta != "":
		return delta, completed
	default:
		return "", completed
	}
}

func collectParts(value any) []map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		if rawParts, ok := typed["parts"]; ok {
			return collectParts(rawParts)
		}
	case []any:
		parts := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			mapped, ok := item.(map[string]any)
			if !ok {
				continue
			}
			parts = append(parts, mapped)
		}
		return parts
	}
	return nil
}

func collectPartLikeMaps(value any) []map[string]any {
	collected := []map[string]any{}
	collectPartLikeMapsInto(value, &collected)
	return collected
}

func collectPartLikeMapsInto(value any, collected *[]map[string]any) {
	switch typed := value.(type) {
	case map[string]any:
		if _, hasType := typed["type"]; hasType {
			*collected = append(*collected, typed)
		}
		for _, nested := range typed {
			collectPartLikeMapsInto(nested, collected)
		}
	case []any:
		for _, item := range typed {
			collectPartLikeMapsInto(item, collected)
		}
	}
}

func partsIndicateCompletion(parts []map[string]any) bool {
	for _, part := range parts {
		partType := strings.ToLower(stringifyValue(part["type"]))
		if strings.Contains(partType, "finish") || strings.Contains(partType, "text-end") {
			return true
		}
	}
	return false
}

func extractOpenCodeMessageText(parts []map[string]any) string {
	snapshot := ""
	var builder strings.Builder

	for _, part := range parts {
		partType := strings.ToLower(stringifyValue(part["type"]))
		if strings.Contains(partType, "tool") || strings.Contains(partType, "permission") || strings.Contains(partType, "step") {
			continue
		}

		text := firstNonEmptyString(
			stringifyValue(part["text"]),
			stringifyValue(part["content"]),
			stringifyValue(part["message"]),
			stringifyValue(part["output"]),
		)
		if text == "" {
			text = extractStructuredTextFromValue(part)
		}
		if text == "" {
			continue
		}

		switch {
		case strings.Contains(partType, "delta"):
			builder.WriteString(text)
		case len(text) > len(snapshot):
			snapshot = text
		}
	}

	switch {
	case snapshot != "":
		return strings.TrimSpace(snapshot)
	case builder.Len() > 0:
		return strings.TrimSpace(builder.String())
	default:
		return ""
	}
}

func extractLatestAssistantText(messages []openCodeMessageEnvelope) string {
	best := ""
	for index := len(messages) - 1; index >= 0; index-- {
		item := messages[index]
		role := strings.ToLower(stringifyValue(item.Info["role"]))
		text := extractOpenCodeMessageText(item.Parts)
		if text == "" {
			continue
		}
		if role == "assistant" {
			return text
		}
		if best == "" {
			best = text
		}
	}
	return best
}

func mergeOpenCodeStreamOutput(current, next string) string {
	switch {
	case next == "":
		return current
	case current == "":
		return next
	case strings.HasPrefix(next, current):
		return next
	case strings.HasPrefix(current, next):
		return current
	default:
		return current + next
	}
}

func extractStructuredTextFromValue(value any) string {
	candidates := make([]string, 0, 8)
	collectTextCandidates(value, &candidates)

	best := ""
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if len(candidate) > len(best) {
			best = candidate
		}
	}

	return best
}

func collectTextCandidates(value any, candidates *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			lowerKey := strings.ToLower(key)
			if isLikelyTextKey(lowerKey) {
				switch nestedValue := nested.(type) {
				case string:
					*candidates = append(*candidates, nestedValue)
				case map[string]any, []any:
					collectTextCandidates(nestedValue, candidates)
				}
				continue
			}
			collectTextCandidates(nested, candidates)
		}
	case []any:
		for _, item := range typed {
			collectTextCandidates(item, candidates)
		}
	case openCodeMessageEnvelope:
		collectTextCandidates(map[string]any{
			"info":  typed.Info,
			"parts": typed.Parts,
		}, candidates)
	case string:
		if strings.TrimSpace(typed) != "" {
			*candidates = append(*candidates, typed)
		}
	}
}

func isLikelyTextKey(key string) bool {
	return strings.Contains(key, "text") ||
		strings.Contains(key, "message") ||
		strings.Contains(key, "output") ||
		strings.Contains(key, "result") ||
		strings.Contains(key, "content") ||
		strings.Contains(key, "response") ||
		strings.Contains(key, "delta")
}

func (p *openCodeStreamParser) readEvent(reader *bufio.Reader) (*openCodeStreamEvent, string, error) {
	if reader == nil {
		return nil, "", io.EOF
	}

	var eventName string
	dataLines := make([]string, 0, 4)
	rawLines := make([]string, 0, 4)

	for {
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, "", err
		}

		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine != "" {
			rawLines = append(rawLines, trimmedLine)
		}

		if trimmedLine == "" {
			event, parseErr := p.parsePayload(strings.Join(dataLines, "\n"), eventName)
			if parseErr != nil {
				return nil, strings.Join(rawLines, "\n"), parseErr
			}
			if event != nil {
				return event, strings.Join(rawLines, "\n"), nil
			}
			if errors.Is(err, io.EOF) {
				return nil, "", io.EOF
			}
			continue
		}

		if strings.HasPrefix(trimmedLine, ":") {
			if errors.Is(err, io.EOF) {
				return nil, "", io.EOF
			}
			continue
		}

		field, value, hasColon := strings.Cut(line, ":")
		if hasColon {
			value = strings.TrimPrefix(value, " ")
		}

		switch {
		case hasColon && field == "event":
			eventName = strings.TrimSpace(value)
		case hasColon && field == "data":
			dataLines = append(dataLines, value)
		case strings.HasPrefix(trimmedLine, "{") || strings.HasPrefix(trimmedLine, "["):
			dataLines = append(dataLines, trimmedLine)
		default:
			dataLines = append(dataLines, line)
		}

		if errors.Is(err, io.EOF) {
			event, parseErr := p.parsePayload(strings.Join(dataLines, "\n"), eventName)
			if parseErr != nil {
				return nil, strings.Join(rawLines, "\n"), parseErr
			}
			if event != nil {
				return event, strings.Join(rawLines, "\n"), nil
			}
			return nil, "", io.EOF
		}
	}
}

func (p *openCodeStreamParser) parsePayload(payload, eventName string) (*openCodeStreamEvent, error) {
	payload = strings.TrimSpace(payload)
	eventName = strings.TrimSpace(eventName)
	if payload == "" {
		return nil, nil
	}

	var event openCodeStreamEvent
	if err := json.Unmarshal([]byte(payload), &event); err == nil && (event.Type != "" || event.Event != "" || event.Data != nil || event.Payload != nil || len(event.Properties) > 0) {
		if event.Event == "" {
			event.Event = eventName
		}
		return &event, nil
	}

	var decoded any
	if err := json.Unmarshal([]byte(payload), &decoded); err == nil {
		return &openCodeStreamEvent{
			Event: defaultIfEmpty(eventName, "message"),
			Data:  decoded,
		}, nil
	}

	return &openCodeStreamEvent{
		Event: defaultIfEmpty(eventName, "message"),
		Data:  map[string]any{"text": payload},
	}, nil
}

func truncateString(value string, maxLength int) string {
	value = strings.TrimSpace(value)
	if maxLength <= 0 || len(value) <= maxLength {
		return value
	}
	if maxLength <= 3 {
		return value[:maxLength]
	}
	return value[:maxLength-3] + "..."
}

func minDuration(values ...time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}

func maxDuration(values ...time.Duration) time.Duration {
	if len(values) == 0 {
		return 0
	}
	maximum := values[0]
	for _, value := range values[1:] {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func stringifyValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func splitURLPathSegments(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}

	parts := strings.Split(trimmed, "/")
	segments := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		segments = append(segments, part)
	}
	return segments
}

func buildURLWithPathSegments(baseURL *url.URL, segments ...string) *url.URL {
	target := *baseURL
	target.RawQuery = ""
	target.Fragment = ""
	target.Path = joinPathSegments(false, segments...)
	target.RawPath = joinPathSegments(true, segments...)
	return &target
}

func joinPathSegments(escape bool, segments ...string) string {
	filtered := make([]string, 0, len(segments))
	for _, segment := range segments {
		segment = strings.Trim(segment, "/")
		if segment == "" {
			continue
		}
		if escape {
			filtered = append(filtered, url.PathEscape(segment))
			continue
		}
		filtered = append(filtered, segment)
	}
	if len(filtered) == 0 {
		return "/"
	}
	return "/" + strings.Join(filtered, "/")
}

func hostAllowed(host string, allowHosts []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if len(allowHosts) == 0 {
		return true
	}
	for _, pattern := range allowHosts {
		if pattern == host {
			return true
		}
		if strings.HasPrefix(pattern, "*.") {
			suffix := strings.TrimPrefix(pattern, "*")
			if strings.HasSuffix(host, suffix) {
				return true
			}
		}
	}
	return false
}

func looksLikeHTMLResponse(contentType string, body []byte) bool {
	if looksLikeHTMLContentType(contentType) {
		return true
	}
	sample := strings.ToLower(strings.TrimSpace(string(body)))
	if sample == "" {
		return false
	}
	if strings.HasPrefix(sample, "<!doctype html") || strings.HasPrefix(sample, "<html") {
		return true
	}
	return strings.Contains(sample, "enable javascript to run this app") ||
		(strings.Contains(sample, "<body") && strings.Contains(sample, "</html>"))
}

func looksLikeHTMLContentType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return strings.Contains(contentType, "text/html") || strings.Contains(contentType, "application/xhtml+xml")
}

func summarizeErrorBody(body []byte) string {
	text := extractStructuredTextFromValue(map[string]any{"body": strings.TrimSpace(string(body))})
	if text != "" {
		return truncateString(text, 280)
	}
	return truncateString(strings.TrimSpace(string(body)), 280)
}

func newServiceCallError(code, summary, detail, hint, requestURL string, statusCode int, retryable bool) *serviceCallError {
	return &serviceCallError{
		Code:       strings.TrimSpace(code),
		Summary:    strings.TrimSpace(summary),
		Detail:     strings.TrimSpace(detail),
		Hint:       strings.TrimSpace(hint),
		RequestURL: strings.TrimSpace(requestURL),
		StatusCode: statusCode,
		Retryable:  retryable,
	}
}

func newUnexpectedHTMLResponseError(label, requestURL string) *serviceCallError {
	return newServiceCallError(
		"unexpected_html",
		fmt.Sprintf("%s returned HTML instead of a JSON or SSE response.", label),
		"The plugin expected an API response but received an HTML document.",
		"Verify the base URL and any reverse proxy routing in front of the OpenCode server.",
		requestURL,
		http.StatusOK,
		false,
	)
}

func classifyHTTPError(label, requestURL string, statusCode int, headers http.Header, body []byte) *serviceCallError {
	if looksLikeHTMLResponse(headers.Get("Content-Type"), body) {
		return newUnexpectedHTMLResponseError(label, requestURL)
	}

	bodySummary := summarizeErrorBody(body)
	requestID := firstHeaderValue(headers, "X-Request-Id", "X-Request-ID", "X-Correlation-ID")
	if requestID != "" {
		bodySummary = strings.TrimSpace(bodySummary + " (request id: " + requestID + ")")
	}

	switch statusCode {
	case http.StatusBadRequest:
		return newServiceCallError(
			"bad_request",
			fmt.Sprintf("%s rejected the request.", label),
			defaultIfEmpty(bodySummary, "The request payload or selected execution target is invalid."),
			"Review the bot defaults, message payload, and server-side validation errors.",
			requestURL,
			statusCode,
			false,
		)
	case http.StatusUnauthorized, http.StatusForbidden:
		return newServiceCallError(
			"auth_failed",
			fmt.Sprintf("%s authentication failed.", label),
			defaultIfEmpty(bodySummary, "The configured username or password was rejected."),
			"Verify the Basic Auth credentials configured for the OpenCode server.",
			requestURL,
			statusCode,
			false,
		)
	case http.StatusNotFound:
		return newServiceCallError(
			"not_found",
			fmt.Sprintf("%s could not find the requested resource.", label),
			defaultIfEmpty(bodySummary, "The session, agent, or endpoint does not exist."),
			"Verify the base URL, session lifecycle, and selected agent or model identifiers.",
			requestURL,
			statusCode,
			false,
		)
	case http.StatusTooManyRequests:
		retryAfter := strings.TrimSpace(headers.Get("Retry-After"))
		hint := "Retry the request later or reduce the request rate."
		if retryAfter != "" {
			hint = fmt.Sprintf("%s asked clients to wait Retry-After=%s before retrying.", label, retryAfter)
		}
		return newServiceCallError(
			"rate_limited",
			fmt.Sprintf("%s is rate limiting requests.", label),
			defaultIfEmpty(bodySummary, "The server accepted the request but deferred execution due to request limits."),
			hint,
			requestURL,
			statusCode,
			true,
		)
	default:
		if statusCode >= http.StatusInternalServerError {
			return newServiceCallError(
				"server_error",
				fmt.Sprintf("%s returned a server error.", label),
				defaultIfEmpty(bodySummary, "The upstream service failed while processing the request."),
				"Check the OpenCode server logs and retry the request.",
				requestURL,
				statusCode,
				true,
			)
		}
		return newServiceCallError(
			"unexpected_status",
			fmt.Sprintf("%s returned HTTP %d.", label, statusCode),
			bodySummary,
			"Check the upstream response payload and OpenCode server logs.",
			requestURL,
			statusCode,
			statusCode >= 500,
		)
	}
}

func classifyRequestError(label, requestURL string, err error) *serviceCallError {
	detail := strings.TrimSpace(err.Error())

	var timeoutError interface{ Timeout() bool }
	if errors.As(err, &timeoutError) && timeoutError.Timeout() {
		return newServiceCallError(
			"network_timeout",
			fmt.Sprintf("%s did not respond before the timeout.", label),
			detail,
			"Check the upstream service status or raise the plugin timeout setting.",
			requestURL,
			0,
			true,
		)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return newServiceCallError(
			"network_timeout",
			fmt.Sprintf("%s did not respond before the timeout.", label),
			detail,
			"Check the upstream service status or raise the plugin timeout setting.",
			requestURL,
			0,
			true,
		)
	}

	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return newServiceCallError(
			"dns_error",
			fmt.Sprintf("The %s host name could not be resolved.", label),
			detail,
			"Check the configured base URL and DNS resolution from the Mattermost host.",
			requestURL,
			0,
			false,
		)
	}

	var hostnameErr x509.HostnameError
	if errors.As(err, &hostnameErr) {
		return newServiceCallError(
			"tls_hostname_error",
			fmt.Sprintf("The TLS certificate host name does not match the %s URL.", label),
			detail,
			"Verify the certificate SAN or CN values and the configured HTTPS endpoint.",
			requestURL,
			0,
			false,
		)
	}

	var unknownAuthorityErr x509.UnknownAuthorityError
	if errors.As(err, &unknownAuthorityErr) {
		return newServiceCallError(
			"tls_unknown_authority",
			fmt.Sprintf("The %s TLS certificate authority is not trusted.", label),
			detail,
			"Install the required CA certificate on the Mattermost host or use a trusted certificate chain.",
			requestURL,
			0,
			false,
		)
	}

	lower := strings.ToLower(detail)
	switch {
	case strings.Contains(lower, "connection refused"):
		return newServiceCallError(
			"connection_refused",
			fmt.Sprintf("%s refused the connection.", label),
			detail,
			"Check that the OpenCode server is running and reachable from the Mattermost host.",
			requestURL,
			0,
			true,
		)
	case strings.Contains(lower, "no such host"):
		return newServiceCallError(
			"dns_error",
			fmt.Sprintf("The %s host name could not be resolved.", label),
			detail,
			"Check the configured base URL and DNS resolution from the Mattermost host.",
			requestURL,
			0,
			false,
		)
	case strings.Contains(lower, "certificate"), strings.Contains(lower, "tls"):
		return newServiceCallError(
			"tls_error",
			fmt.Sprintf("A TLS error prevented the %s request from completing.", label),
			detail,
			"Check the upstream certificate chain and HTTPS configuration.",
			requestURL,
			0,
			false,
		)
	default:
		return newServiceCallError(
			"network_error",
			fmt.Sprintf("The plugin could not reach %s.", label),
			detail,
			"Check the base URL, network path, firewall, and proxy configuration.",
			requestURL,
			0,
			true,
		)
	}
}

func firstHeaderValue(headers http.Header, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(headers.Get(key)); value != "" {
			return value
		}
	}
	return ""
}
