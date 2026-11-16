// SPDX-License-Identifier: MPL-2.0

package platform

// A project's environment variables (#408): set in its Add project dialog
// or its settings, they reach every command 1loop starts for its chats
// (the shell tool, background tasks, terminals, Claude Code) on the
// project's target. They travel in the context, never in a prompt or a
// log: the model reads them only by running a command that prints them.

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
)

type projectEnvKey struct{}

// envName is a variable name a shell accepts.
var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidEnvName says whether name can be a project variable's name.
func ValidEnvName(name string) bool { return envName.MatchString(name) }

// WithProjectEnv hands the commands started under ctx a project's
// variables. Invalid names are left out.
func WithProjectEnv(ctx context.Context, env map[string]string) context.Context {
	if len(env) == 0 {
		return ctx
	}
	return context.WithValue(ctx, projectEnvKey{}, EnvPairs(env))
}

// WithProjectEnvPairs is WithProjectEnv for pairs ProjectEnv returned, to
// hand them on to a context that outlives ctx.
func WithProjectEnvPairs(ctx context.Context, pairs []string) context.Context {
	if len(pairs) == 0 {
		return ctx
	}
	return context.WithValue(ctx, projectEnvKey{}, pairs)
}

// ProjectEnv is the project's variables under ctx, as sorted NAME=value
// pairs; nil when it has none.
func ProjectEnv(ctx context.Context) []string {
	if ctx == nil {
		return nil
	}
	pairs, _ := ctx.Value(projectEnvKey{}).([]string)
	return pairs
}

// EnvPairs are env's valid variables as sorted NAME=value pairs.
func EnvPairs(env map[string]string) []string {
	var pairs []string
	for name, value := range env {
		if ValidEnvName(name) {
			pairs = append(pairs, name+"="+value)
		}
	}
	slices.Sort(pairs)
	return pairs
}

// ApplyProjectEnv adds ctx's project variables to a command of this
// computer, over the inherited environment.
func ApplyProjectEnv(ctx context.Context, command *exec.Cmd) {
	pairs := ProjectEnv(ctx)
	if len(pairs) == 0 {
		return
	}
	if command.Env == nil {
		command.Env = os.Environ()
	}
	command.Env = append(command.Env, pairs...)
}

// ExportScript is a POSIX shell line that exports ctx's project variables,
// for a command run on another machine; "" when there are none.
func ExportScript(ctx context.Context) string {
	pairs := ProjectEnv(ctx)
	if len(pairs) == 0 {
		return ""
	}
	var script strings.Builder
	script.WriteString("export")
	for _, pair := range pairs {
		name, value, _ := strings.Cut(pair, "=")
		script.WriteString(" " + name + "='" + strings.ReplaceAll(value, "'", `'\''`) + "'")
	}
	script.WriteString("\n")
	return script.String()
}
