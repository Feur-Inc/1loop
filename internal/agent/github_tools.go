// SPDX-License-Identifier: MPL-2.0

package agent

// The GitHub connector's server has over a hundred tools (about 35000
// tokens of schemas). A chat gets four of 1loop's own instead, one per
// area of coding work: issues, pull requests, code and Actions. Each takes
// an operation and runs the server's tool behind it, approved as that tool
// is. The server's other tools (Copilot, notifications, gists, security
// alerts, automations, stars, teams…) are not offered: the gh command or
// the user covers them (#447).

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"1loop/internal/core"
	"1loop/internal/extensions"
)

const (
	githubIssuesTool  = "github_issues"
	githubPullsTool   = "github_pull_requests"
	githubCodeTool    = "github_code"
	githubActionsTool = "github_actions"
)

// githubOperation is one operation of a grouped tool: the server's tool it
// runs, the arguments it fixes, and how the grouped tool's own parameters
// are named for that tool. upload and view are 1loop's attachment tools.
type githubOperation struct {
	tool   string            // the server's tool; "" for the attachment tools
	fixed  map[string]any    // e.g. method: get
	rename map[string]string // grouped name → the server's
	params []string          // the grouped parameters it reads
}

var githubIssueOps = map[string]githubOperation{
	"read":     {tool: "issue_read", fixed: map[string]any{"method": "get"}, rename: map[string]string{"number": "issue_number"}, params: []string{"number"}},
	"comments": {tool: "issue_read", fixed: map[string]any{"method": "get_comments"}, rename: map[string]string{"number": "issue_number"}, params: []string{"number", "page", "per_page"}},
	"create":   {tool: "issue_write", fixed: map[string]any{"method": "create"}, params: []string{"title", "body", "labels", "assignees"}},
	"update":   {tool: "issue_write", fixed: map[string]any{"method": "update"}, rename: map[string]string{"number": "issue_number"}, params: []string{"number", "title", "body", "state", "state_reason", "labels", "assignees"}},
	"comment":  {tool: "add_issue_comment", rename: map[string]string{"number": "issue_number"}, params: []string{"number", "body"}},
	"search":   {tool: "search_issues", params: []string{"query", "page", "per_page"}},
}

var githubPullOps = map[string]githubOperation{
	"read":            pullRead("get"),
	"diff":            pullRead("get_diff"),
	"files":           pullRead("get_files"),
	"commits":         pullRead("get_commits"),
	"checks":          pullRead("get_check_runs"),
	"status":          pullRead("get_status"),
	"reviews":         pullRead("get_reviews"),
	"review_comments": pullRead("get_review_comments"),
	"comments":        pullRead("get_comments"),
	"create":          {tool: "create_pull_request", params: []string{"title", "body", "head", "base", "draft"}},
	"update":          {tool: "update_pull_request", rename: map[string]string{"number": "pullNumber"}, params: []string{"number", "title", "body", "state", "base", "draft"}},
	"search":          {tool: "search_pull_requests", params: []string{"query", "page", "per_page"}},
	"merge":           {tool: "merge_pull_request", rename: map[string]string{"number": "pullNumber"}, params: []string{"number", "merge_method"}},
	"review":          {tool: "pull_request_review_write", fixed: map[string]any{"method": "create"}, rename: map[string]string{"number": "pullNumber"}, params: []string{"number", "event", "body"}},
	"submit_review":   {tool: "pull_request_review_write", fixed: map[string]any{"method": "submit_pending"}, rename: map[string]string{"number": "pullNumber"}, params: []string{"number", "event", "body"}},
	"review_comment":  {tool: "add_comment_to_pending_review", fixed: map[string]any{"subjectType": "LINE"}, rename: map[string]string{"number": "pullNumber"}, params: []string{"number", "path", "line", "side", "body"}},
	"reply":           {tool: "add_reply_to_pull_request_comment", rename: map[string]string{"number": "pullNumber", "comment_id": "commentId"}, params: []string{"number", "comment_id", "body"}},
}

func pullRead(method string) githubOperation {
	return githubOperation{tool: "pull_request_read", fixed: map[string]any{"method": method}, rename: map[string]string{"number": "pullNumber"}, params: []string{"number", "page", "per_page"}}
}

var githubCodeOps = map[string]githubOperation{
	"file":     {tool: "get_file_contents", params: []string{"path", "ref"}},
	"search":   {tool: "search_code", params: []string{"query", "page", "per_page"}},
	"commits":  {tool: "list_commits", rename: map[string]string{"ref": "sha"}, params: []string{"ref", "path", "page", "per_page"}},
	"commit":   {tool: "get_commit", params: []string{"sha"}},
	"branches": {tool: "list_branches", params: []string{"page", "per_page"}},
}

var githubActionOps = map[string]githubOperation{
	"workflows":    {tool: "actions_list", fixed: map[string]any{"method": "list_workflows"}, params: []string{"page", "per_page"}},
	"runs":         {tool: "actions_list", fixed: map[string]any{"method": "list_workflow_runs"}, rename: map[string]string{"workflow_id": "resource_id"}, params: []string{"workflow_id", "page", "per_page"}},
	"jobs":         {tool: "actions_list", fixed: map[string]any{"method": "list_workflow_jobs"}, rename: map[string]string{"run_id": "resource_id"}, params: []string{"run_id", "page", "per_page"}},
	"run":          {tool: "actions_get", fixed: map[string]any{"method": "get_workflow_run"}, rename: map[string]string{"run_id": "resource_id"}, params: []string{"run_id"}},
	"job":          {tool: "actions_get", fixed: map[string]any{"method": "get_workflow_job"}, rename: map[string]string{"job_id": "resource_id"}, params: []string{"job_id"}},
	"logs":         {tool: "get_job_logs", fixed: map[string]any{"return_content": true}, params: []string{"job_id", "run_id", "failed_only", "tail_lines"}},
	"rerun":        {tool: "actions_run_trigger", fixed: map[string]any{"method": "rerun_workflow_run"}, params: []string{"run_id"}},
	"rerun_failed": {tool: "actions_run_trigger", fixed: map[string]any{"method": "rerun_failed_jobs"}, params: []string{"run_id"}},
	"cancel":       {tool: "actions_run_trigger", fixed: map[string]any{"method": "cancel_workflow_run"}, params: []string{"run_id"}},
	"start":        {tool: "actions_run_trigger", fixed: map[string]any{"method": "run_workflow"}, params: []string{"workflow_id", "ref", "inputs"}},
}

func githubAttachmentOps(ops map[string]githubOperation) map[string]githubOperation {
	ops["upload"] = githubOperation{params: []string{"files"}}
	ops["view_attachment"] = githubOperation{params: []string{"url"}}
	return ops
}

func init() {
	githubAttachmentOps(githubIssueOps)
	githubAttachmentOps(githubPullOps)
}

func githubOps(tool string) map[string]githubOperation {
	switch tool {
	case githubIssuesTool:
		return githubIssueOps
	case githubPullsTool:
		return githubPullOps
	case githubCodeTool:
		return githubCodeOps
	case githubActionsTool:
		return githubActionOps
	}
	return nil
}

// githubCoreTools are the server's tools the grouped tools run. A chat from
// before the grouping may still call them by their github_ name.
var githubCoreTools = func() map[string]bool {
	core := map[string]bool{}
	for _, tool := range []string{githubIssuesTool, githubPullsTool, githubCodeTool, githubActionsTool} {
		for _, operation := range githubOps(tool) {
			if operation.tool != "" {
				core[operation.tool] = true
			}
		}
	}
	return core
}()

func githubSchema(ops map[string]githubOperation, properties map[string]any) map[string]any {
	names := make([]string, 0, len(ops))
	for name := range ops {
		names = append(names, name)
	}
	sort.Strings(names)
	all := map[string]any{
		"operation":  map[string]any{"type": "string", "enum": names},
		"repository": map[string]any{"type": "string", "description": "owner/name."},
		"page":       map[string]any{"type": "integer"},
		"per_page":   map[string]any{"type": "integer"},
	}
	for name, property := range properties {
		all[name] = property
	}
	return map[string]any{"type": "object", "properties": all, "required": []string{"operation"}}
}

func str(description string) map[string]any {
	return map[string]any{"type": "string", "description": description}
}

func integer(description string) map[string]any {
	return map[string]any{"type": "integer", "description": description}
}

func githubUploadFiles() map[string]any {
	return map[string]any{"type": "array", "description": "upload: up to 10 files to show.", "items": map[string]any{"type": "object", "properties": map[string]any{
		"path":        str("A workspace file: png, jpg, gif, webp, svg up to 10 MiB; mp4, mov, webm up to 100 MiB."),
		"browser_tab": str("Or a Browser Use tab to capture (\"selected\" for the selected one)."),
		"full_page":   map[string]any{"type": "boolean", "description": "Capture the whole page."},
		"alt":         str("Alt text."),
	}}}
}

const githubAttachmentNote = " upload puts workspace files or browser screenshots on GitHub and returns the Markdown to paste in a body (uploads are public to readers of the repository and cannot be deleted). view_attachment opens an image attached to an issue or pull request."

func githubDefinitionsList() []map[string]any {
	function := func(name, description string, parameters map[string]any) map[string]any {
		return map[string]any{"type": "function", "function": map[string]any{"name": name, "description": description, "parameters": parameters, "strict": false}}
	}
	number := integer("The issue or pull request number.")
	body := str("Markdown text.")
	return []map[string]any{
		function(githubIssuesTool, "GitHub issues of a repository: read one or its comments, create, update (title, body, state, labels, assignees), comment, or search (GitHub search syntax)."+githubAttachmentNote,
			githubSchema(githubIssueOps, map[string]any{
				"number": number, "title": str("create, update."), "body": body,
				"state":        map[string]any{"type": "string", "enum": []string{"open", "closed"}},
				"state_reason": map[string]any{"type": "string", "enum": []string{"completed", "not_planned", "duplicate"}},
				"labels":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"assignees":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"query":        str("search: GitHub search syntax, e.g. is:open label:bug."),
				"files":        githubUploadFiles(), "url": str("view_attachment: the image's address."),
			})),
		function(githubPullsTool, "GitHub pull requests: read one (details, diff, files, commits, checks, status, reviews, comments), create, update, search, merge, review (event APPROVE, REQUEST_CHANGES or COMMENT; without event it starts a pending review), add a line comment to the pending review, submit it, or reply to a review comment."+githubAttachmentNote,
			githubSchema(githubPullOps, map[string]any{
				"number": number, "title": str("create, update."), "body": body,
				"head":         str("create: the branch with the changes."),
				"base":         str("create, update: the branch to merge into."),
				"draft":        map[string]any{"type": "boolean"},
				"state":        map[string]any{"type": "string", "enum": []string{"open", "closed"}},
				"merge_method": map[string]any{"type": "string", "enum": []string{"merge", "squash", "rebase"}},
				"event":        map[string]any{"type": "string", "enum": []string{"APPROVE", "REQUEST_CHANGES", "COMMENT"}},
				"path":         str("review_comment: the file."),
				"line":         integer("review_comment: the line."),
				"side":         map[string]any{"type": "string", "enum": []string{"LEFT", "RIGHT"}},
				"comment_id":   integer("reply: the review comment's id."),
				"query":        str("search: GitHub search syntax."),
				"files":        githubUploadFiles(), "url": str("view_attachment: the image's address."),
			})),
		function(githubCodeTool, "Code of a GitHub repository without cloning it: a file or directory, code search (GitHub search syntax), commits, one commit with its changes, branches.",
			githubSchema(githubCodeOps, map[string]any{
				"path":  str("file: the path; commits: only those touching it."),
				"ref":   str("file, commits: a branch, tag or commit; null for the default branch."),
				"sha":   str("commit: its SHA."),
				"query": str("search: e.g. repo:owner/name func main."),
			})),
		function(githubActionsTool, "GitHub Actions: list workflows, runs (of one workflow or all) and a run's jobs, read a run or a job, read job logs (failed_only, tail_lines), rerun a run or its failed jobs, cancel a run, start a workflow on a ref.",
			githubSchema(githubActionOps, map[string]any{
				"workflow_id": str("runs, start: the workflow's id or file name (ci.yml)."),
				"run_id":      integer("A workflow run's id."),
				"job_id":      integer("A job's id."),
				"ref":         str("start: the branch or tag."),
				"inputs":      map[string]any{"type": "object", "description": "start: the workflow's inputs."},
				"failed_only": map[string]any{"type": "boolean", "description": "logs: only failed jobs of run_id."},
				"tail_lines":  integer("logs: the last lines only."),
			})),
	}
}

// githubDefinitions are the connector's tools for a chat: the four grouped
// tools, offered when the server has the tools they run.
func githubDefinitions(server extensions.MCPServer, tools []extensions.MCPTool) []map[string]any {
	_ = server
	var definitions []map[string]any
	for _, definition := range githubDefinitionsList() {
		name := definition["function"].(map[string]any)["name"].(string)
		for _, operation := range githubOps(name) {
			if operation.tool != "" && slices.ContainsFunc(tools, func(tool extensions.MCPTool) bool { return tool.Name == operation.tool }) {
				definitions = append(definitions, definition)
				break
			}
		}
	}
	return definitions
}

// githubArguments are the server tool's arguments for a grouped call.
func githubArguments(operation githubOperation, args map[string]any) (map[string]any, error) {
	out := map[string]any{}
	if repository, _ := args["repository"].(string); repository != "" {
		owner, repo, ok := strings.Cut(repository, "/")
		if !ok || owner == "" || repo == "" {
			return nil, fmt.Errorf("repository is owner/name, not %q", repository)
		}
		out["owner"], out["repo"] = owner, repo
	}
	for _, name := range operation.params {
		value, ok := args[name]
		if !ok || value == nil {
			continue
		}
		target := name
		if renamed, ok := operation.rename[name]; ok {
			target = renamed
		} else if name == "per_page" {
			target = "perPage"
		}
		out[target] = value
	}
	for name, value := range operation.fixed {
		out[name] = value
	}
	return out, nil
}

// callGitHubGroup runs an operation of a grouped tool.
func (backend *App) callGitHubGroup(ctx context.Context, cfg Config, server extensions.MCPServer, tools []extensions.MCPTool, call ToolCall, begin func(), emit emitter) (string, []core.ImagePart, error) {
	ops := githubOps(call.Function.Name)
	var args map[string]any
	if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
		return "", nil, err
	}
	name, _ := args["operation"].(string)
	operation, ok := ops[name]
	if !ok {
		names := make([]string, 0, len(ops))
		for name := range ops {
			names = append(names, name)
		}
		sort.Strings(names)
		return "", nil, fmt.Errorf("operation is one of %s", strings.Join(names, ", "))
	}
	if operation.tool == "" {
		return "", nil, fmt.Errorf("operation %s runs on its own", name)
	}
	arguments, err := githubArguments(operation, args)
	if err != nil {
		return "", nil, err
	}
	for _, tool := range tools {
		if tool.Name != operation.tool {
			continue
		}
		data, _ := json.Marshal(arguments)
		inner := ToolCall{ID: call.ID}
		inner.Function.Name, inner.Function.Arguments = call.Function.Name, string(data)
		text, images, err := backend.callMCPTool(ctx, cfg, server, tool, inner, begin, emit)
		return text, images, githubAuthorizationError(ctx, server, err)
	}
	return "", nil, fmt.Errorf("The connected GitHub account's server has no %s tool", operation.tool)
}

// githubAttachmentCall is the attachment tool a grouped call names, with its
// arguments, or ok false.
func githubAttachmentCall(call ToolCall) (ToolCall, bool) {
	if githubOps(call.Function.Name) == nil {
		return call, false
	}
	var args map[string]any
	if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil {
		return call, false
	}
	inner := ToolCall{ID: call.ID}
	switch args["operation"] {
	case "upload":
		data, _ := json.Marshal(map[string]any{"repository": args["repository"], "files": args["files"]})
		inner.Function.Name, inner.Function.Arguments = githubUploadTool, string(data)
	case "view_attachment":
		data, _ := json.Marshal(map[string]any{"url": args["url"]})
		inner.Function.Name, inner.Function.Arguments = githubViewTool, string(data)
	default:
		return call, false
	}
	return inner, true
}
