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
	MessageID string                 `json:"messageID,omitempty"`
	Model     *openCodeModelSelector `json:"model,omitempty"`
	Agent     string                 `json:"agent,omitempty"`
	NoReply   bool                   `json:"noReply,omitempty"`
	System    string                 `json:"system,omitempty"`
	Tools     map[string]bool        `json:"tools,omitempty"`
	Parts     []openCodePart         `json:"parts"`
}

type openCodeModelSelector struct {
	ProviderID string `json:"providerID"`
	ModelID    string `json:"modelID"`
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

type openCodeStreamView struct {
	Text       string
	Reasoning  string
	ToolStatus string
}

type openCodeStreamState struct {
	rawText     string
	reasoning   string
	toolStatus  string
	completed   bool
	messageID   string
	partKinds   map[string]string
	acceptedAny bool
}

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
	onUpdate func(openCodeStreamView, bool),
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

	idleWindow := maxDuration(cfg.DefaultTimeout, 2*time.Minute)
	timer := time.NewTimer(idleWindow)
	defer timer.Stop()

	streamState := &openCodeStreamState{}
	for {
		select {
		case item, ok := <-items:
			if !ok {
				finalView := streamState.view()
				if finalView.Text != "" {
					if latest, latestErr := p.getLatestOpenCodeReply(ctx, cfg, sessionID); latestErr == nil && latest != "" {
						finalView.Text = mergeOpenCodeStreamOutput(finalView.Text, latest)
					}
					finalView.Text = truncateString(finalView.Text, cfg.MaxOutputLength)
					if onUpdate != nil {
						onUpdate(finalView, true)
					}
					return finalView.Text, statusCode, nil
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
				finalView := streamState.view()
				if finalView.Text != "" {
					finalView.Text = truncateString(finalView.Text, cfg.MaxOutputLength)
					if onUpdate != nil {
						onUpdate(finalView, true)
					}
					return finalView.Text, statusCode, nil
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

			if !streamState.apply(*item.event) {
				continue
			}

			view := streamState.view()
			view.Text = truncateString(view.Text, cfg.MaxOutputLength)
			if onUpdate != nil && (view.Text != "" || view.Reasoning != "" || view.ToolStatus != "") {
				onUpdate(view, false)
			}
			if streamState.completed {
				if latest, latestErr := p.getLatestOpenCodeReply(ctx, cfg, sessionID); latestErr == nil && latest != "" {
					view.Text = truncateString(mergeOpenCodeStreamOutput(view.Text, latest), cfg.MaxOutputLength)
				}
				if onUpdate != nil && view.Text != "" {
					onUpdate(view, true)
				}
				return view.Text, statusCode, nil
			}
		case <-timer.C:
			finalView := streamState.view()
			if finalView.Text != "" {
				if latest, latestErr := p.getLatestOpenCodeReply(ctx, cfg, sessionID); latestErr == nil && latest != "" {
					finalView.Text = truncateString(mergeOpenCodeStreamOutput(finalView.Text, latest), cfg.MaxOutputLength)
				}
				if onUpdate != nil {
					onUpdate(finalView, true)
				}
				return finalView.Text, statusCode, nil
			}
			timer.Reset(idleWindow)
		case <-ctx.Done():
			finalView := streamState.view()
			if finalView.Text != "" {
				finalView.Text = truncateString(finalView.Text, cfg.MaxOutputLength)
				if onUpdate != nil {
					onUpdate(finalView, true)
				}
				return finalView.Text, statusCode, nil
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
	state := &openCodeStreamState{}
	state.apply(event)
	view := state.view()
	return view.Text, state.completed
}

func (s *openCodeStreamState) apply(event openCodeStreamEvent) bool {
	if s == nil {
		return false
	}
	if nested, ok := nestedOpenCodeEvent(event); ok {
		return s.apply(nested)
	}

	eventType := strings.ToLower(strings.TrimSpace(defaultIfEmpty(event.Type, event.Event)))
	changed := false

	switch eventType {
	case "server.connected":
		return false
	case "message_start":
		s.acceptedAny = true
		return true
	case "message_delta":
		if text, reasoning := extractSimpleMessageDelta(event.Data); text != "" || reasoning != "" {
			changed = s.mergeTextDelta(text) || changed
			changed = s.mergeReasoningDelta(reasoning) || changed
		}
	case "message_end", "message_stop":
		if content := firstStringFromMap(event.Data, "content", "text", "message", "output"); content != "" {
			changed = s.mergeTextSnapshot(content) || changed
		}
		s.completed = true
		changed = true
	case "content_block_delta":
		text, reasoning, tool := extractContentBlockDelta(event.Data)
		changed = s.mergeTextDelta(text) || changed
		changed = s.mergeReasoningDelta(reasoning) || changed
		changed = s.mergeToolStatus(tool) || changed
	case "content_block_start":
		if tool := summarizeContentBlockStart(event.Data); tool != "" {
			changed = s.mergeToolStatus(tool) || changed
		}
	case "content_block_stop":
		return false
	}

	if strings.Contains(eventType, "finish") || strings.Contains(eventType, "completed") || strings.Contains(eventType, "text-end") {
		s.completed = true
		changed = true
	}

	properties := mapFromAny(event.Properties)
	if len(properties) == 0 {
		properties = mapFromAny(event.Data)
	}

	if part := mapFromAny(properties["part"]); len(part) > 0 {
		changed = s.applyPart(part, rawStringifyValue(properties["delta"])) || changed
	}

	if eventType == "message.part.delta" {
		changed = s.applyPartDelta(properties) || changed
	}

	if info := mapFromAny(properties["info"]); len(info) > 0 {
		if messageID := stringifyValue(info["id"]); messageID != "" {
			s.messageID = messageID
		}
		if strings.EqualFold(stringifyValue(info["role"]), "assistant") && messageInfoIndicatesCompletion(info) {
			s.completed = true
			changed = true
		}
	}

	if strings.EqualFold(eventType, "session.idle") || strings.EqualFold(eventType, "session.error") || strings.EqualFold(eventType, "session.turn.close") {
		s.completed = true
		changed = true
	}

	for _, part := range collectParts(event.Data) {
		changed = s.applyPart(part, "") || changed
	}
	for _, part := range collectParts(event.Payload) {
		changed = s.applyPart(part, "") || changed
	}

	if changed {
		s.acceptedAny = true
	}
	return changed
}

func nestedOpenCodeEvent(event openCodeStreamEvent) (openCodeStreamEvent, bool) {
	if strings.TrimSpace(event.Type) != "" || strings.TrimSpace(event.Event) != "" {
		return openCodeStreamEvent{}, false
	}
	for _, candidate := range []any{event.Payload, event.Data} {
		mapped := mapFromAny(candidate)
		if len(mapped) == 0 {
			continue
		}
		nestedType := stringifyValue(mapped["type"])
		nestedEvent := stringifyValue(mapped["event"])
		if nestedType == "" && nestedEvent == "" {
			continue
		}
		return openCodeStreamEvent{
			Type:       nestedType,
			Event:      nestedEvent,
			Data:       mapped["data"],
			Payload:    mapped["payload"],
			Properties: mapFromAny(mapped["properties"]),
		}, true
	}
	return openCodeStreamEvent{}, false
}

func (s *openCodeStreamState) applyPart(part map[string]any, explicitDelta string) bool {
	if s == nil || len(part) == 0 {
		return false
	}

	partType := strings.ToLower(stringifyValue(part["type"]))
	if messageID := stringifyValue(part["messageID"]); messageID != "" {
		s.messageID = messageID
	}
	if partID := firstNonEmptyString(stringifyValue(part["id"]), stringifyValue(part["partID"])); partID != "" && partType != "" {
		if s.partKinds == nil {
			s.partKinds = map[string]string{}
		}
		s.partKinds[partID] = partType
	}

	switch {
	case partType == "text" || strings.Contains(partType, "text-delta"):
		snapshot := firstNonEmptyRawString(rawStringifyValue(part["text"]), rawStringifyValue(part["content"]))
		if snapshot != "" {
			return s.mergeTextSnapshot(snapshot)
		}
		return s.mergeTextDelta(firstNonEmptyRawString(explicitDelta, rawStringifyValue(part["delta"])))
	case partType == "reasoning" || strings.Contains(partType, "thinking") || strings.Contains(partType, "reasoning"):
		snapshot := firstNonEmptyRawString(rawStringifyValue(part["text"]), rawStringifyValue(part["content"]), rawStringifyValue(part["summary"]))
		if snapshot != "" {
			return s.mergeReasoningSnapshot(snapshot)
		}
		return s.mergeReasoningDelta(firstNonEmptyRawString(explicitDelta, rawStringifyValue(part["delta"])))
	case partType == "tool":
		return s.mergeToolStatus(summarizeToolPart(part))
	case strings.Contains(partType, "tool") || strings.Contains(partType, "permission"):
		return s.mergeToolStatus(summarizeToolPart(part))
	case partType == "step-finish" || partType == "step-start" || partType == "snapshot" || partType == "patch" || partType == "agent":
		return false
	default:
		return false
	}
}

func (s *openCodeStreamState) applyPartDelta(properties map[string]any) bool {
	if s == nil || len(properties) == 0 {
		return false
	}

	delta := firstNonEmptyRawString(rawStringifyValue(properties["delta"]), rawStringifyValue(properties["text"]))
	if strings.TrimSpace(delta) == "" {
		return false
	}

	partType := strings.ToLower(firstNonEmptyString(stringifyValue(properties["partType"]), stringifyValue(properties["type"])))
	if partType == "" && s.partKinds != nil {
		partType = s.partKinds[firstNonEmptyString(stringifyValue(properties["partID"]), stringifyValue(properties["id"]))]
	}

	switch {
	case strings.Contains(partType, "tool") || strings.Contains(partType, "permission"):
		return false
	case strings.Contains(partType, "reasoning") || strings.Contains(partType, "thinking"):
		return s.mergeReasoningDelta(delta)
	default:
		return s.mergeTextDelta(delta)
	}
}

func (s *openCodeStreamState) view() openCodeStreamView {
	if s == nil {
		return openCodeStreamView{}
	}

	assistantText, thinkReasoning, _ := splitThinkTaggedText(s.rawText)
	reasoning := strings.TrimSpace(strings.Join(nonEmptyStrings(s.reasoning, thinkReasoning), "\n\n"))
	return openCodeStreamView{
		Text:       strings.TrimSpace(assistantText),
		Reasoning:  reasoning,
		ToolStatus: strings.TrimSpace(s.toolStatus),
	}
}

func (s *openCodeStreamState) mergeTextDelta(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	next := mergeOpenCodeStreamOutput(s.rawText, text)
	if next == s.rawText {
		return false
	}
	s.rawText = next
	return true
}

func (s *openCodeStreamState) mergeTextSnapshot(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || text == s.rawText {
		return false
	}
	s.rawText = text
	return true
}

func (s *openCodeStreamState) mergeReasoningDelta(text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	next := mergeOpenCodeStreamOutput(s.reasoning, text)
	if next == s.reasoning {
		return false
	}
	s.reasoning = next
	return true
}

func (s *openCodeStreamState) mergeReasoningSnapshot(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" || text == s.reasoning {
		return false
	}
	s.reasoning = text
	return true
}

func (s *openCodeStreamState) mergeToolStatus(status string) bool {
	status = strings.TrimSpace(status)
	if status == "" || status == s.toolStatus {
		return false
	}
	s.toolStatus = status
	return true
}

func extractSimpleMessageDelta(value any) (string, string) {
	mapped := mapFromAny(value)
	if len(mapped) == 0 {
		return stringifyValue(value), ""
	}

	delta := mapped["delta"]
	switch typed := delta.(type) {
	case map[string]any:
		deltaType := strings.ToLower(stringifyValue(typed["type"]))
		text := firstNonEmptyRawString(
			rawStringifyValue(typed["text"]),
			rawStringifyValue(typed["thinking"]),
			rawStringifyValue(typed["content"]),
			rawStringifyValue(typed["partial_json"]),
		)
		if strings.Contains(deltaType, "thinking") || strings.Contains(deltaType, "reasoning") {
			return "", text
		}
		if strings.Contains(deltaType, "input_json") || strings.Contains(deltaType, "tool") {
			return "", ""
		}
		return text, ""
	case string:
		return typed, ""
	default:
		return firstStringFromMap(mapped, "text", "content", "message"), ""
	}
}

func extractContentBlockDelta(value any) (string, string, string) {
	mapped := mapFromAny(value)
	if len(mapped) == 0 {
		return "", "", ""
	}
	delta := mapFromAny(mapped["delta"])
	deltaType := strings.ToLower(stringifyValue(delta["type"]))
	switch {
	case strings.Contains(deltaType, "text"):
		return rawStringifyValue(delta["text"]), "", ""
	case strings.Contains(deltaType, "thinking") || strings.Contains(deltaType, "reasoning"):
		return "", firstNonEmptyRawString(rawStringifyValue(delta["thinking"]), rawStringifyValue(delta["text"])), ""
	case strings.Contains(deltaType, "input_json") || strings.Contains(deltaType, "tool"):
		return "", "", "Using tool..."
	default:
		return "", "", ""
	}
}

func summarizeContentBlockStart(value any) string {
	mapped := mapFromAny(value)
	block := mapFromAny(mapped["content_block"])
	if len(block) == 0 {
		return ""
	}
	if !strings.Contains(strings.ToLower(stringifyValue(block["type"])), "tool") {
		return ""
	}
	name := firstNonEmptyString(stringifyValue(block["name"]), stringifyValue(block["tool"]))
	if name == "" {
		return "Using tool..."
	}
	return "Using tool: " + name
}

func summarizeToolPart(part map[string]any) string {
	if len(part) == 0 {
		return ""
	}
	toolName := firstNonEmptyString(stringifyValue(part["tool"]), stringifyValue(part["name"]), stringifyValue(part["title"]))
	state := mapFromAny(part["state"])
	status := strings.ToLower(firstNonEmptyString(stringifyValue(state["status"]), stringifyValue(part["status"])))
	title := firstNonEmptyString(stringifyValue(state["title"]), stringifyValue(part["title"]))
	if title != "" {
		toolName = title
	}
	if toolName == "" {
		toolName = "tool"
	}

	switch status {
	case "pending":
		return "Waiting to run " + toolName
	case "running":
		return "Running " + toolName
	case "completed":
		return "Completed " + toolName
	case "error":
		return "Tool failed: " + toolName
	default:
		return "Using " + toolName
	}
}

func messageInfoIndicatesCompletion(info map[string]any) bool {
	if len(info) == 0 {
		return false
	}
	if stringifyValue(info["finish"]) != "" || stringifyValue(info["error"]) != "" {
		return true
	}
	if timeInfo := mapFromAny(info["time"]); stringifyValue(timeInfo["completed"]) != "" {
		return true
	}
	return false
}

func mapFromAny(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	case map[string]string:
		mapped := make(map[string]any, len(typed))
		for key, value := range typed {
			mapped[key] = value
		}
		return mapped
	default:
		return nil
	}
}

func firstStringFromMap(value any, keys ...string) string {
	mapped := mapFromAny(value)
	if len(mapped) == 0 {
		return ""
	}
	for _, key := range keys {
		if text := stringifyValue(mapped[key]); text != "" {
			return text
		}
	}
	return ""
}

func nonEmptyStrings(values ...string) []string {
	filtered := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			filtered = append(filtered, value)
		}
	}
	return filtered
}

func collectParts(value any) []map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		if rawParts, ok := typed["parts"]; ok {
			return collectParts(rawParts)
		}
		parts := []map[string]any{}
		for _, nested := range typed {
			parts = append(parts, collectParts(nested)...)
		}
		return parts
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
		if !isAssistantTextPart(partType, part) {
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
		assistantText, _, _ := splitThinkTaggedText(snapshot)
		return strings.TrimSpace(assistantText)
	case builder.Len() > 0:
		assistantText, _, _ := splitThinkTaggedText(builder.String())
		return strings.TrimSpace(assistantText)
	default:
		return ""
	}
}

func isAssistantTextPart(partType string, part map[string]any) bool {
	partType = strings.ToLower(strings.TrimSpace(partType))
	if strings.Contains(partType, "tool") ||
		strings.Contains(partType, "permission") ||
		strings.Contains(partType, "step") ||
		strings.Contains(partType, "reasoning") ||
		strings.Contains(partType, "thinking") ||
		strings.Contains(partType, "snapshot") ||
		strings.Contains(partType, "patch") ||
		strings.Contains(partType, "agent") ||
		strings.Contains(partType, "retry") ||
		strings.Contains(partType, "compaction") {
		return false
	}
	if partType == "" {
		return firstNonEmptyString(stringifyValue(part["text"]), stringifyValue(part["content"])) != ""
	}
	return partType == "text" || strings.Contains(partType, "text-delta")
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

func splitThinkTaggedText(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", "", false
	}

	lower := strings.ToLower(value)
	cursor := 0
	thinkOpen := false
	responseParts := []string{}
	reasoningParts := []string{}

	for cursor < len(value) {
		openIndex := strings.Index(lower[cursor:], "<think>")
		closeIndex := strings.Index(lower[cursor:], "</think>")

		nextIndex := -1
		tagLength := 0
		openTag := false
		switch {
		case openIndex >= 0 && (closeIndex < 0 || openIndex < closeIndex):
			nextIndex = cursor + openIndex
			tagLength = len("<think>")
			openTag = true
		case closeIndex >= 0:
			nextIndex = cursor + closeIndex
			tagLength = len("</think>")
		default:
			chunk := value[cursor:]
			if thinkOpen {
				reasoningParts = append(reasoningParts, chunk)
			} else {
				responseParts = append(responseParts, chunk)
			}
			cursor = len(value)
			continue
		}

		chunk := value[cursor:nextIndex]
		if thinkOpen {
			reasoningParts = append(reasoningParts, chunk)
		} else {
			responseParts = append(responseParts, chunk)
		}
		thinkOpen = openTag
		cursor = nextIndex + tagLength
	}

	return normalizeOpenCodeVisibleText(strings.Join(responseParts, "")),
		normalizeOpenCodeVisibleText(strings.Join(reasoningParts, "\n\n")),
		thinkOpen
}

func normalizeOpenCodeVisibleText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	for strings.Contains(value, "\n\n\n") {
		value = strings.ReplaceAll(value, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(value)
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

func firstNonEmptyRawString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
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

func rawStringifyValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
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
