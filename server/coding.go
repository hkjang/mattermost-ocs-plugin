package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	codingTaskKeyPrefix          = "coding_task:"
	defaultCodingSearchResults   = 20
	defaultCodingOutputPreview   = 4000
	defaultCodingPromptFileChars = 4000
)

type CodingWorkspaceSnapshot struct {
	Label           string   `json:"label,omitempty"`
	Root            string   `json:"root,omitempty"`
	RepoRoot        string   `json:"repo_root,omitempty"`
	Profile         string   `json:"profile,omitempty"`
	Branch          string   `json:"branch,omitempty"`
	DefaultBranch   string   `json:"default_branch,omitempty"`
	Dirty           bool     `json:"dirty"`
	ChangedFiles    []string `json:"changed_files,omitempty"`
	DiffStat        string   `json:"diff_stat,omitempty"`
	StatusSummary   string   `json:"status_summary,omitempty"`
	AllowedPaths    []string `json:"allowed_paths,omitempty"`
	AllowedCommands []string `json:"allowed_commands,omitempty"`
}

type CodingCommand struct {
	ID               string `json:"id"`
	Title            string `json:"title,omitempty"`
	Command          string `json:"command"`
	CWD              string `json:"cwd,omitempty"`
	Reason           string `json:"reason,omitempty"`
	RequiresApproval bool   `json:"requires_approval"`
	Status           string `json:"status"`
	ExitCode         int    `json:"exit_code,omitempty"`
	OutputPreview    string `json:"output_preview,omitempty"`
	TestSummary      string `json:"test_summary,omitempty"`
	DurationMS       int64  `json:"duration_ms,omitempty"`
	StartedAt        int64  `json:"started_at,omitempty"`
	CompletedAt      int64  `json:"completed_at,omitempty"`
	ErrorMessage     string `json:"error_message,omitempty"`
}

type CodingDiff struct {
	Path    string `json:"path"`
	Summary string `json:"summary,omitempty"`
}

type CodingTask struct {
	ID              string                  `json:"id"`
	SessionID       string                  `json:"session_id"`
	BotID           string                  `json:"bot_id"`
	BotUsername     string                  `json:"bot_username"`
	BotName         string                  `json:"bot_name"`
	UserID          string                  `json:"user_id"`
	ChannelID       string                  `json:"channel_id"`
	RootID          string                  `json:"root_id"`
	PostID          string                  `json:"post_id,omitempty"`
	Status          string                  `json:"status"`
	Summary         string                  `json:"summary,omitempty"`
	ResponseMessage string                  `json:"response_message,omitempty"`
	Workspace       CodingWorkspaceSnapshot `json:"workspace"`
	Diffs           []CodingDiff            `json:"diffs,omitempty"`
	Commands        []CodingCommand         `json:"commands,omitempty"`
	LastCommandID   string                  `json:"last_command_id,omitempty"`
	CreatedAt       int64                   `json:"created_at"`
	UpdatedAt       int64                   `json:"updated_at"`
}

type codingSearchResult struct {
	Kind    string `json:"kind,omitempty"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Preview string `json:"preview"`
}

type codingCommandActionRequest struct {
	TaskID    string `json:"task_id"`
	CommandID string `json:"command_id"`
}

type codingTaskEnvelope struct {
	Summary  string `json:"summary"`
	Commands []struct {
		Title            string `json:"title"`
		Command          string `json:"command"`
		CWD              string `json:"cwd"`
		Reason           string `json:"reason"`
		RequiresApproval *bool  `json:"requires_approval"`
	} `json:"commands"`
}

func (p *Plugin) buildCodingPromptContext(ctx context.Context, cfg *runtimeConfiguration, bot BotDefinition, prompt string) string {
	if !bot.isCodingBot() {
		return ""
	}

	sections := []string{}
	if bot.Coding.IncludeWorkspaceSnapshot {
		if snapshot, err := p.inspectCodingWorkspace(ctx, cfg, bot); err == nil {
			sections = append(sections, renderWorkspaceSnapshotForPrompt(snapshot))
		}
	}
	if bot.Coding.IncludeReferencedFiles {
		if files := p.collectReferencedFilesForPrompt(ctx, cfg, bot, prompt); len(files) > 0 {
			sections = append(sections, strings.Join(files, "\n\n"))
		}
	}

	instructions := []string{
		fmt.Sprintf("Coding mode profile: %s", defaultIfEmpty(bot.Coding.Profile, defaultCodingProfile)),
		"Assume all file inspection and command execution must happen through the current OpenCode project and session APIs.",
		"If you need shell commands or tests to be run, append a fenced ```ocs-task JSON block with summary and commands.",
		"Each command in the ocs-task block should include title, command, cwd, reason, and requires_approval.",
		"Do not include destructive commands unless absolutely necessary.",
	}
	sections = append(sections, "Coding workflow instructions:\n- "+strings.Join(instructions, "\n- "))
	return strings.Join(filterNonEmptyStrings(sections), "\n\n")
}

func (p *Plugin) createCodingTask(
	ctx context.Context,
	cfg *runtimeConfiguration,
	bot BotDefinition,
	request BotRunRequest,
	account botAccount,
	sessionID, output string,
) (CodingTask, string, error) {
	visibleOutput, commands, summary := extractCodingTaskPlan(output)
	snapshot, _ := p.inspectCodingWorkspace(ctx, cfg, bot)
	diffs, _ := p.loadCodingDiffs(ctx, cfg, sessionID)
	now := time.Now().UnixMilli()
	task := CodingTask{
		ID:              uuid.NewString(),
		SessionID:       sessionID,
		BotID:           bot.ID,
		BotUsername:     account.Definition.Username,
		BotName:         account.Definition.DisplayName,
		UserID:          request.UserID,
		ChannelID:       request.ChannelID,
		RootID:          request.RootID,
		Status:          "planned",
		Summary:         summary,
		ResponseMessage: visibleOutput,
		Workspace:       snapshot,
		Diffs:           diffs,
		Commands:        normalizeCodingCommands(bot, commands),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if task.Summary == "" {
		task.Summary = deriveCodingSummary(visibleOutput)
	}
	if len(task.Commands) == 0 {
		task.Status = "completed"
	}
	if err := p.saveCodingTask(task); err != nil {
		return CodingTask{}, visibleOutput, err
	}
	return task, visibleOutput, nil
}

func normalizeCodingCommands(bot BotDefinition, commands []CodingCommand) []CodingCommand {
	normalized := make([]CodingCommand, 0, len(commands))
	for _, command := range commands {
		if strings.TrimSpace(command.Command) == "" {
			continue
		}
		command.ID = defaultIfEmpty(strings.TrimSpace(command.ID), uuid.NewString())
		command.Command = strings.TrimSpace(command.Command)
		command.CWD = strings.TrimSpace(command.CWD)
		command.Title = strings.TrimSpace(command.Title)
		command.Reason = strings.TrimSpace(command.Reason)
		if command.Title == "" {
			command.Title = command.Command
		}
		if command.Status == "" {
			command.Status = "pending"
		}
		command.RequiresApproval = bot.Coding.RequireCommandApproval || command.RequiresApproval
		normalized = append(normalized, command)
	}
	return normalized
}

func extractCodingTaskPlan(output string) (string, []CodingCommand, string) {
	trimmedOutput := strings.TrimSpace(output)
	if trimmedOutput == "" {
		return "", nil, ""
	}

	re := regexp.MustCompile("(?s)```ocs-task\\s*(\\{.*?\\})\\s*```")
	match := re.FindStringSubmatch(trimmedOutput)
	if len(match) < 2 {
		return trimmedOutput, nil, deriveCodingSummary(trimmedOutput)
	}

	var envelope codingTaskEnvelope
	if err := json.Unmarshal([]byte(match[1]), &envelope); err != nil {
		return strings.TrimSpace(re.ReplaceAllString(trimmedOutput, "")), nil, deriveCodingSummary(trimmedOutput)
	}

	commands := make([]CodingCommand, 0, len(envelope.Commands))
	for _, item := range envelope.Commands {
		command := CodingCommand{
			ID:      uuid.NewString(),
			Title:   strings.TrimSpace(item.Title),
			Command: strings.TrimSpace(item.Command),
			CWD:     strings.TrimSpace(item.CWD),
			Reason:  strings.TrimSpace(item.Reason),
			Status:  "pending",
		}
		if item.RequiresApproval != nil {
			command.RequiresApproval = *item.RequiresApproval
		}
		commands = append(commands, command)
	}

	cleaned := strings.TrimSpace(re.ReplaceAllString(trimmedOutput, ""))
	return cleaned, commands, strings.TrimSpace(envelope.Summary)
}

func deriveCodingSummary(message string) string {
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		if line != "" {
			return truncateString(line, 160)
		}
	}
	return ""
}

func (p *Plugin) saveCodingTask(task CodingTask) error {
	if strings.TrimSpace(task.ID) == "" {
		return nil
	}
	task.UpdatedAt = time.Now().UnixMilli()
	data, err := json.Marshal(task)
	if err != nil {
		return fmt.Errorf("failed to encode coding task: %w", err)
	}
	if appErr := p.API.KVSet(codingTaskKeyPrefix+task.ID, data); appErr != nil {
		return fmt.Errorf("failed to persist coding task: %w", appErr)
	}
	return nil
}

func (p *Plugin) getCodingTask(taskID string) (CodingTask, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return CodingTask{}, nil
	}
	data, appErr := p.API.KVGet(codingTaskKeyPrefix + taskID)
	if appErr != nil {
		return CodingTask{}, fmt.Errorf("failed to load coding task: %w", appErr)
	}
	if len(data) == 0 {
		return CodingTask{}, nil
	}
	var task CodingTask
	if err := json.Unmarshal(data, &task); err != nil {
		return CodingTask{}, fmt.Errorf("failed to decode coding task: %w", err)
	}
	return task, nil
}

func (p *Plugin) inspectCodingWorkspace(ctx context.Context, cfg *runtimeConfiguration, bot BotDefinition) (CodingWorkspaceSnapshot, error) {
	scope := normalizeCodingScope(bot.Coding.WorkspaceRoot)
	snapshot := CodingWorkspaceSnapshot{
		Label:           strings.TrimSpace(bot.Coding.WorkspaceLabel),
		Root:            scope,
		Profile:         defaultIfEmpty(bot.Coding.Profile, defaultCodingProfile),
		DefaultBranch:   strings.TrimSpace(bot.Coding.DefaultBranch),
		AllowedPaths:    append([]string{}, bot.Coding.AllowedPaths...),
		AllowedCommands: append([]string{}, bot.Coding.CommandAllowlist...),
	}

	pathValue, _ := p.loadOpenCodePath(ctx, cfg)
	if pathValue != "" {
		snapshot.Root = pathValue
		if snapshot.Label == "" {
			snapshot.Label = path.Base(strings.TrimRight(pathValue, "/"))
		}
	}
	if snapshot.Label == "" && scope != "" {
		snapshot.Label = path.Base(strings.TrimRight(scope, "/"))
	}
	if snapshot.Label == "" {
		snapshot.Label = "current project"
	}

	if vcsPayload, err := p.loadOpenCodeVCS(ctx, cfg); err == nil {
		snapshot.RepoRoot = firstNonEmptyString(
			lookupStringField(vcsPayload, "root", "repoRoot", "repo", "workspaceRoot"),
			pathValue,
		)
		snapshot.Branch = firstNonEmptyString(
			lookupStringField(vcsPayload, "branch", "currentBranch", "head", "ref"),
			snapshot.Branch,
		)
		if branch := lookupStringField(vcsPayload, "defaultBranch", "mainBranch", "trunk"); branch != "" {
			snapshot.DefaultBranch = branch
		}
		snapshot.Dirty = lookupBoolField(vcsPayload, "dirty", "isDirty", "modified")
	}

	if fileStatus, err := p.loadOpenCodeFileStatus(ctx, cfg); err == nil {
		changedFiles := make([]string, 0, len(fileStatus))
		statusNotes := make([]string, 0, len(fileStatus))
		for _, item := range fileStatus {
			filePath := normalizeCodingScope(firstNonEmptyString(
				lookupStringField(item, "path", "file", "name"),
				extractStructuredTextFromValue(item),
			))
			if filePath == "" || !pathAllowedForCodingBot(bot, filePath) {
				continue
			}
			changedFiles = append(changedFiles, filePath)
			statusLabel := firstNonEmptyString(lookupStringField(item, "status", "type", "state"), "changed")
			statusNotes = append(statusNotes, fmt.Sprintf("%s (%s)", filePath, statusLabel))
		}
		if len(changedFiles) > 0 {
			snapshot.Dirty = true
			snapshot.ChangedFiles = limitUniqueStrings(changedFiles, 12)
			snapshot.DiffStat = strings.Join(limitUniqueStrings(statusNotes, 6), "\n")
		}
	}

	switch {
	case snapshot.Dirty && len(snapshot.ChangedFiles) > 0:
		snapshot.StatusSummary = fmt.Sprintf("modified files: %d", len(snapshot.ChangedFiles))
	case snapshot.Dirty:
		snapshot.StatusSummary = "dirty"
	default:
		snapshot.StatusSummary = "clean"
	}

	return snapshot, nil
}

func renderWorkspaceSnapshotForPrompt(snapshot CodingWorkspaceSnapshot) string {
	lines := []string{
		"Coding workspace snapshot:",
		fmt.Sprintf("- workspace: %s", defaultIfEmpty(snapshot.Label, snapshot.Root)),
	}
	if snapshot.Root != "" {
		lines = append(lines, fmt.Sprintf("- root: %s", snapshot.Root))
	}
	if snapshot.Branch != "" {
		lines = append(lines, fmt.Sprintf("- branch: %s", snapshot.Branch))
	}
	if snapshot.DefaultBranch != "" {
		lines = append(lines, fmt.Sprintf("- default branch: %s", snapshot.DefaultBranch))
	}
	if snapshot.StatusSummary != "" {
		lines = append(lines, fmt.Sprintf("- status: %s", snapshot.StatusSummary))
	}
	if len(snapshot.ChangedFiles) > 0 {
		lines = append(lines, fmt.Sprintf("- changed files: %s", strings.Join(snapshot.ChangedFiles, ", ")))
	}
	if snapshot.DiffStat != "" {
		lines = append(lines, "- diff stat:\n"+snapshot.DiffStat)
	}
	if len(snapshot.AllowedPaths) > 0 {
		lines = append(lines, fmt.Sprintf("- allowed paths: %s", strings.Join(snapshot.AllowedPaths, ", ")))
	}
	if len(snapshot.AllowedCommands) > 0 {
		lines = append(lines, fmt.Sprintf("- allowed commands: %s", strings.Join(snapshot.AllowedCommands, " | ")))
	}
	return strings.Join(lines, "\n")
}

func (p *Plugin) collectReferencedFilesForPrompt(ctx context.Context, cfg *runtimeConfiguration, bot BotDefinition, prompt string) []string {
	paths := extractReferencedPaths(prompt)
	if len(paths) == 0 {
		return nil
	}
	maxFiles := positiveOrDefault(bot.Coding.MaxReferencedFiles, defaultCodingMaxFiles)
	blocks := make([]string, 0, maxFiles)
	for _, itemPath := range paths {
		if len(blocks) >= maxFiles {
			break
		}
		resolvedPath := scopedCodingPath(bot, itemPath)
		if resolvedPath == "" || !pathAllowedForCodingBot(bot, resolvedPath) {
			continue
		}
		content, err := p.loadOpenCodeFileContent(ctx, cfg, resolvedPath)
		if err != nil || strings.TrimSpace(content) == "" {
			continue
		}
		blocks = append(blocks, fmt.Sprintf("Referenced file: %s\n```text\n%s\n```", resolvedPath, truncateString(content, minInt(cfg.MaxInputLength, defaultCodingPromptFileChars))))
	}
	return blocks
}

func extractReferencedPaths(prompt string) []string {
	candidates := map[string]struct{}{}
	add := func(value string) {
		value = strings.Trim(value, " `\"'()[]{}<>:,")
		if value == "" {
			return
		}
		if strings.Contains(value, "..") {
			return
		}
		if !strings.Contains(value, "/") && !strings.Contains(value, "\\") && !strings.Contains(value, ".") {
			return
		}
		candidates[value] = struct{}{}
	}

	backticks := regexp.MustCompile("`([^`]+)`")
	for _, match := range backticks.FindAllStringSubmatch(prompt, -1) {
		if len(match) > 1 {
			add(match[1])
		}
	}

	barePaths := regexp.MustCompile(`[\w./\\-]+\.[A-Za-z0-9]+`)
	for _, match := range barePaths.FindAllString(prompt, -1) {
		add(match)
	}

	items := make([]string, 0, len(candidates))
	for item := range candidates {
		items = append(items, item)
	}
	sort.Strings(items)
	return items
}

func normalizeCodingScope(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Trim(value, " ")
	if value == "" || value == "." {
		return ""
	}
	cleaned := path.Clean("/" + strings.TrimLeft(value, "/"))
	return strings.TrimPrefix(cleaned, "/")
}

func scopedCodingPath(bot BotDefinition, candidate string) string {
	candidate = normalizeCodingScope(candidate)
	if candidate == "" {
		return normalizeCodingScope(bot.Coding.WorkspaceRoot)
	}
	scope := normalizeCodingScope(bot.Coding.WorkspaceRoot)
	if scope == "" {
		return candidate
	}
	if candidate == scope || strings.HasPrefix(candidate, scope+"/") {
		return candidate
	}
	return normalizeCodingScope(path.Join(scope, candidate))
}

func pathAllowedForCodingBot(bot BotDefinition, itemPath string) bool {
	itemPath = normalizeCodingScope(itemPath)
	if itemPath == "" {
		return false
	}
	scope := normalizeCodingScope(bot.Coding.WorkspaceRoot)
	if scope != "" && itemPath != scope && !strings.HasPrefix(itemPath, scope+"/") {
		return false
	}
	if len(bot.Coding.AllowedPaths) == 0 {
		return true
	}
	for _, allowed := range bot.Coding.AllowedPaths {
		candidate := scopedCodingPath(bot, allowed)
		if itemPath == candidate || strings.HasPrefix(itemPath, candidate+"/") {
			return true
		}
	}
	return false
}

func limitUniqueStrings(items []string, limit int) []string {
	seen := map[string]struct{}{}
	normalized := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		normalized = append(normalized, item)
		if limit > 0 && len(normalized) >= limit {
			break
		}
	}
	return normalized
}

func lookupStringField(value any, keys ...string) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if direct, ok := typed[key]; ok {
				if text := stringifyValue(direct); text != "" {
					return text
				}
			}
		}
		for _, nested := range typed {
			if text := lookupStringField(nested, keys...); text != "" {
				return text
			}
		}
	case []any:
		for _, item := range typed {
			if text := lookupStringField(item, keys...); text != "" {
				return text
			}
		}
	}
	return ""
}

func lookupBoolField(value any, keys ...string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if direct, ok := typed[key]; ok {
				switch converted := direct.(type) {
				case bool:
					return converted
				case string:
					if parsed, err := strconv.ParseBool(strings.TrimSpace(converted)); err == nil {
						return parsed
					}
				}
			}
		}
		for _, nested := range typed {
			if lookupBoolField(nested, keys...) {
				return true
			}
		}
	case []any:
		for _, item := range typed {
			if lookupBoolField(item, keys...) {
				return true
			}
		}
	}
	return false
}

func parseJSONAny(raw []byte) (any, error) {
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func arrayPayload(value any, keys ...string) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case map[string]any:
		for _, key := range keys {
			if nested, ok := typed[key]; ok {
				if items, ok := nested.([]any); ok {
					return items
				}
			}
		}
	}
	return nil
}

func (p *Plugin) loadOpenCodePath(ctx context.Context, cfg *runtimeConfiguration) (string, error) {
	body, _, err := p.doOpenCodeJSONRequest(ctx, cfg, "GET", []string{"path"}, nil, nil, 8192)
	if err != nil {
		return "", err
	}
	payload, err := parseJSONAny(body)
	if err != nil {
		return "", err
	}
	switch typed := payload.(type) {
	case string:
		return strings.TrimSpace(typed), nil
	default:
		return firstNonEmptyString(
			lookupStringField(payload, "path", "root", "cwd", "dir", "directory"),
			extractStructuredTextFromValue(typed),
		), nil
	}
}

func (p *Plugin) loadOpenCodeVCS(ctx context.Context, cfg *runtimeConfiguration) (any, error) {
	body, _, err := p.doOpenCodeJSONRequest(ctx, cfg, "GET", []string{"vcs"}, nil, nil, 16384)
	if err != nil {
		return nil, err
	}
	return parseJSONAny(body)
}

func (p *Plugin) loadOpenCodeFileStatus(ctx context.Context, cfg *runtimeConfiguration) ([]any, error) {
	body, _, err := p.doOpenCodeJSONRequest(ctx, cfg, "GET", []string{"file", "status"}, nil, nil, 32768)
	if err != nil {
		return nil, err
	}
	payload, err := parseJSONAny(body)
	if err != nil {
		return nil, err
	}
	items := arrayPayload(payload, "items", "files", "status")
	if items == nil {
		return nil, nil
	}
	return items, nil
}

func (p *Plugin) loadOpenCodeFileContent(ctx context.Context, cfg *runtimeConfiguration, filePath string) (string, error) {
	body, _, err := p.doOpenCodeJSONRequest(ctx, cfg, "GET", []string{"file", "content"}, map[string]string{"path": filePath}, nil, int(cfg.MaxInputLength*4))
	if err != nil {
		return "", err
	}
	payload, err := parseJSONAny(body)
	if err != nil {
		return "", err
	}
	switch typed := payload.(type) {
	case string:
		return strings.TrimSpace(typed), nil
	default:
		return firstNonEmptyString(
			lookupStringField(payload, "content", "text", "body", "value"),
			extractStructuredTextFromValue(typed),
		), nil
	}
}

func (p *Plugin) loadCodingDiffs(ctx context.Context, cfg *runtimeConfiguration, sessionID string) ([]CodingDiff, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, nil
	}
	body, _, err := p.doOpenCodeJSONRequest(ctx, cfg, "GET", []string{"session", sessionID, "diff"}, nil, nil, 65536)
	if err != nil {
		return nil, err
	}
	payload, err := parseJSONAny(body)
	if err != nil {
		return nil, err
	}
	items := arrayPayload(payload, "items", "diffs", "files")
	if items == nil {
		return nil, nil
	}
	diffs := make([]CodingDiff, 0, len(items))
	for _, item := range items {
		diffPath := normalizeCodingScope(firstNonEmptyString(
			lookupStringField(item, "path", "file", "name"),
			extractStructuredTextFromValue(item),
		))
		if diffPath == "" {
			continue
		}
		summary := firstNonEmptyString(
			lookupStringField(item, "summary", "status", "type"),
			truncateString(extractStructuredTextFromValue(item), 180),
		)
		diffs = append(diffs, CodingDiff{Path: diffPath, Summary: summary})
	}
	return diffs, nil
}

func (p *Plugin) searchCodingWorkspace(ctx context.Context, cfg *runtimeConfiguration, bot BotDefinition, query string, limit int) ([]codingSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []codingSearchResult{}, nil
	}

	limit = positiveOrDefault(limit, defaultCodingSearchResults)
	results := make([]codingSearchResult, 0, limit)
	seen := map[string]struct{}{}
	appendResult := func(item codingSearchResult) {
		if len(results) >= limit {
			return
		}
		item.Path = normalizeCodingScope(item.Path)
		if item.Path == "" || !pathAllowedForCodingBot(bot, item.Path) {
			return
		}
		key := fmt.Sprintf("%s:%s:%d:%s", item.Kind, item.Path, item.Line, item.Preview)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		results = append(results, item)
	}

	searchScope := normalizeCodingScope(bot.Coding.WorkspaceRoot)

	if body, _, err := p.doOpenCodeJSONRequest(ctx, cfg, "GET", []string{"find", "file"}, map[string]string{
		"query":     query,
		"directory": searchScope,
		"limit":     strconv.Itoa(limit),
	}, nil, 32768); err == nil {
		var matches []string
		if json.Unmarshal(body, &matches) == nil {
			for _, match := range matches {
				appendResult(codingSearchResult{
					Kind:    "file",
					Path:    match,
					Preview: "File match",
				})
			}
		}
	}

	if body, _, err := p.doOpenCodeJSONRequest(ctx, cfg, "GET", []string{"find", "symbol"}, map[string]string{
		"query": query,
		"limit": strconv.Itoa(limit),
	}, nil, 32768); err == nil {
		if payload, parseErr := parseJSONAny(body); parseErr == nil {
			if items := arrayPayload(payload, "items", "symbols", "results"); items != nil {
				for _, item := range items {
					appendResult(codingSearchResult{
						Kind: "symbol",
						Path: firstNonEmptyString(
							lookupStringField(item, "path", "file", "uri"),
							lookupStringField(item, "name"),
						),
						Line: parseCodingLine(item),
						Preview: firstNonEmptyString(
							lookupStringField(item, "name"),
							lookupStringField(item, "kind"),
							truncateString(extractStructuredTextFromValue(item), 240),
						),
					})
				}
			}
		}
	}

	if body, _, err := p.doOpenCodeJSONRequest(ctx, cfg, "GET", []string{"find"}, map[string]string{"pattern": query}, nil, 65536); err == nil {
		if payload, parseErr := parseJSONAny(body); parseErr == nil {
			if items := arrayPayload(payload, "items", "matches", "results"); items != nil {
				for _, item := range items {
					appendResult(codingSearchResult{
						Kind: "text",
						Path: lookupStringField(item, "path", "file", "name"),
						Line: parseCodingLine(item),
						Preview: firstNonEmptyString(
							lookupStringField(item, "lines", "line", "text"),
							truncateString(extractStructuredTextFromValue(item), 240),
						),
					})
				}
			}
		}
	}

	return results, nil
}

func parseCodingLine(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"line_number", "line", "startLine"} {
			switch lineValue := typed[key].(type) {
			case float64:
				return int(lineValue)
			case int:
				return lineValue
			case string:
				if parsed, err := strconv.Atoi(strings.TrimSpace(lineValue)); err == nil {
					return parsed
				}
			}
		}
		if location, ok := typed["location"]; ok {
			return parseCodingLine(location)
		}
		if rangeValue, ok := typed["range"]; ok {
			return parseCodingLine(rangeValue)
		}
	case []any:
		for _, item := range typed {
			if line := parseCodingLine(item); line > 0 {
				return line
			}
		}
	}
	return 0
}

func (p *Plugin) executeCodingCommand(ctx context.Context, cfg *runtimeConfiguration, bot BotDefinition, task CodingTask, commandID string) (CodingTask, error) {
	commandIndex := -1
	for index, item := range task.Commands {
		if item.ID == commandID {
			commandIndex = index
			break
		}
	}
	if commandIndex < 0 {
		return task, fmt.Errorf("command was not found")
	}

	command := task.Commands[commandIndex]
	if !commandAllowed(bot, command.Command) {
		command.Status = "rejected"
		command.ErrorMessage = "command is not allowed by this bot policy"
		task.Commands[commandIndex] = command
		task.Status = "blocked"
		_ = p.saveCodingTask(task)
		return task, fmt.Errorf("command %q is not allowed", command.Command)
	}

	startedAt := time.Now()
	command.Status = "running"
	command.StartedAt = startedAt.UnixMilli()
	task.Commands[commandIndex] = command
	task.Status = "running_command"
	task.LastCommandID = command.ID
	_ = p.saveCodingTask(task)

	timeout := time.Duration(positiveOrDefault(bot.Coding.CommandTimeoutSeconds, defaultCodingCommandTimeout)) * time.Second
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	agentID := resolveAgentID(cfg, bot)
	modelID := resolveModelID(cfg, bot)
	output, _, runErr := p.executeCodingCommandViaOpenCode(runCtx, cfg, bot, task.SessionID, agentID, modelID, command)
	command.CompletedAt = time.Now().UnixMilli()
	command.DurationMS = time.Since(startedAt).Milliseconds()
	command.OutputPreview = truncateString(strings.TrimSpace(output), defaultCodingOutputPreview)
	command.TestSummary = summarizeCommandOutput(command.Command, output, 0)

	if runErr != nil {
		command.Status = "failed"
		command.ErrorMessage = runErr.Error()
		task.Status = "failed"
	} else {
		command.Status = "completed"
		task.Status = "completed"
	}

	task.Commands[commandIndex] = command
	task.LastCommandID = command.ID
	refreshCtx, refreshCancel := context.WithTimeout(context.Background(), minDuration(cfg.DefaultTimeout, 10*time.Second))
	defer refreshCancel()
	if snapshot, err := p.inspectCodingWorkspace(refreshCtx, cfg, bot); err == nil {
		task.Workspace = snapshot
	}
	if diffs, err := p.loadCodingDiffs(refreshCtx, cfg, task.SessionID); err == nil {
		task.Diffs = diffs
	}
	task.Status = deriveCodingTaskStatus(task.Commands)
	task.UpdatedAt = time.Now().UnixMilli()
	if err := p.saveCodingTask(task); err != nil {
		return task, err
	}
	return task, nil
}

func (p *Plugin) executeCodingCommandViaOpenCode(
	ctx context.Context,
	cfg *runtimeConfiguration,
	bot BotDefinition,
	sessionID, agentID, modelID string,
	command CodingCommand,
) (string, int, error) {
	commandText := strings.TrimSpace(command.Command)
	if strings.HasPrefix(commandText, "/") {
		name, arguments := parseSlashCommand(commandText)
		if name == "" {
			return "", 0, fmt.Errorf("slash command is empty")
		}
		return p.invokeOpenCodeSessionCommand(ctx, cfg, sessionID, agentID, modelID, name, arguments)
	}

	return p.invokeOpenCodeShellCommand(ctx, cfg, sessionID, agentID, modelID, buildRemoteShellCommand(bot, command))
}

func buildRemoteShellCommand(bot BotDefinition, command CodingCommand) string {
	commandText := strings.TrimSpace(command.Command)
	cwd := normalizeCodingScope(firstNonEmptyString(command.CWD, bot.Coding.WorkspaceRoot))
	if cwd == "" {
		return commandText
	}
	if looksLikeWindowsDrivePath(cwd) {
		return fmt.Sprintf("cd /d %s && %s", quoteWindowsShellPath(cwd), commandText)
	}
	return fmt.Sprintf("cd %s && %s", quoteShellPath(cwd), commandText)
}

func quoteShellPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "."
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func quoteWindowsShellPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "/", "\\"))
	if value == "" {
		return "."
	}
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func looksLikeWindowsDrivePath(value string) bool {
	if len(value) < 2 {
		return false
	}
	return value[1] == ':'
}

func parseSlashCommand(commandText string) (string, []string) {
	fields := strings.Fields(strings.TrimSpace(commandText))
	if len(fields) == 0 {
		return "", nil
	}
	name := strings.TrimPrefix(fields[0], "/")
	if name == "" {
		return "", nil
	}
	return name, fields[1:]
}

func deriveCodingTaskStatus(commands []CodingCommand) string {
	if len(commands) == 0 {
		return "completed"
	}
	hasPending := false
	for _, command := range commands {
		switch command.Status {
		case "running":
			return "running_command"
		case "failed", "rejected":
			return "failed"
		case "pending", "":
			hasPending = true
		}
	}
	if hasPending {
		return "planned"
	}
	return "completed"
}

func commandAllowed(bot BotDefinition, commandText string) bool {
	commandText = strings.TrimSpace(commandText)
	if commandText == "" {
		return false
	}
	if !strings.HasPrefix(commandText, "/") && hasUnsafeShellOperators(commandText) {
		return false
	}
	commandText = strings.ToLower(commandText)
	for _, prefix := range bot.Coding.CommandAllowlist {
		prefix = strings.ToLower(strings.TrimSpace(prefix))
		if prefix != "" && strings.HasPrefix(commandText, prefix) {
			return true
		}
	}
	return false
}

func hasUnsafeShellOperators(commandText string) bool {
	if strings.Contains(commandText, "\n") || strings.Contains(commandText, "\r") {
		return true
	}
	for _, token := range []string{"&&", "||", ";", "|", ">", "<", "&", "`", "$("} {
		if strings.Contains(commandText, token) {
			return true
		}
	}
	return false
}

func summarizeCommandOutput(commandText, output string, exitCode int) string {
	commandText = strings.ToLower(strings.TrimSpace(commandText))
	output = strings.TrimSpace(output)
	switch {
	case strings.Contains(commandText, "test"):
		if exitCode == 0 && !strings.Contains(strings.ToLower(output), "fail") {
			return "Tests passed."
		}
		return "Tests failed."
	case strings.Contains(commandText, "build"):
		if exitCode == 0 && !strings.Contains(strings.ToLower(output), "error") {
			return "Build succeeded."
		}
		return "Build failed."
	default:
		if exitCode == 0 {
			return "Command completed successfully."
		}
		if output == "" {
			return fmt.Sprintf("Command failed with exit code %d.", exitCode)
		}
		return truncateString(output, 160)
	}
}

func buildCodingTaskProps(task CodingTask) map[string]any {
	props := map[string]any{
		"opencode_bot_mode":         botModeCoding,
		"opencode_task_id":          task.ID,
		"opencode_task_status":      task.Status,
		"opencode_task_summary":     task.Summary,
		"opencode_workspace_label":  task.Workspace.Label,
		"opencode_workspace_root":   task.Workspace.Root,
		"opencode_workspace_branch": task.Workspace.Branch,
		"opencode_workspace_dirty":  fmt.Sprintf("%t", task.Workspace.Dirty),
	}
	if data, err := json.Marshal(task); err == nil {
		props["opencode_coding_task"] = string(data)
	}
	return props
}

func (p *Plugin) attachCodingTaskToPost(post *model.Post, task CodingTask) (*model.Post, error) {
	if post == nil {
		return nil, fmt.Errorf("post is not available")
	}
	updated := *post
	updated.Props = clonePostProps(post.Props)
	if updated.Props == nil {
		updated.Props = map[string]any{}
	}
	for key, value := range buildCodingTaskProps(task) {
		updated.Props[key] = value
	}
	result, appErr := p.API.UpdatePost(&updated)
	if appErr != nil {
		return nil, fmt.Errorf("failed to update coding task post: %w", appErr)
	}
	return result, nil
}

func renderCodingTaskSystemNote(task CodingTask) string {
	lines := []string{}
	if task.Summary != "" {
		lines = append(lines, task.Summary)
	}
	lines = append(lines, fmt.Sprintf("Workspace: %s", defaultIfEmpty(task.Workspace.Label, task.Workspace.Root)))
	if task.Workspace.Branch != "" {
		lines = append(lines, fmt.Sprintf("Branch: %s", task.Workspace.Branch))
	}
	if len(task.Commands) > 0 {
		lines = append(lines, fmt.Sprintf("Pending commands: %d", countCommandsWithStatus(task.Commands, "pending")))
	}
	return strings.Join(lines, "\n")
}

func countCommandsWithStatus(commands []CodingCommand, status string) int {
	count := 0
	for _, command := range commands {
		if command.Status == status {
			count++
		}
	}
	return count
}

func filterNonEmptyStrings(items []string) []string {
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item) != "" {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
