// Package ghci is the deploy.CI adapter for GitHub Actions, through the gh CLI.
package ghci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/execx"
)

// CI reads the runs of the repo checked out at Dir.
type CI struct {
	Dir string
	// Run runs gh and git; nil is execx.Run.
	Run execx.Runner
}

var _ deploy.CI = (*CI)(nil)

func (c *CI) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	run := c.Run
	if run == nil {
		run = execx.Run
	}
	return run(ctx, execx.Cmd{Name: name, Args: args, Dir: c.Dir})
}

// Commits are HEAD and its 50 most recent ancestors.
func (c *CI) Commits(ctx context.Context) ([]string, error) {
	out, err := c.run(ctx, "git", "rev-list", "-n", "50", "HEAD")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

// Runs are the repo's last 100 workflow runs. gh failing (missing, signed
// out, Actions off) is an error, never an empty list: no answer is not green.
func (c *CI) Runs(ctx context.Context) ([]deploy.CIRun, error) {
	out, err := c.run(ctx, "gh", "run", "list", "--limit", "100", "--json", "headSha,status,conclusion,workflowName,url")
	if err != nil {
		var e *execx.Error
		if errors.As(err, &e) && strings.Contains(strings.ToLower(e.Stderr), "auth") {
			return nil, errors.New("gh is not signed in (gh auth login)")
		}
		return nil, err
	}
	var runs []deploy.CIRun
	if err := json.Unmarshal(out, &runs); err != nil {
		return nil, fmt.Errorf("gh run list returned output that is not JSON: %w", err)
	}
	return runs, nil
}
