package deploy

import (
	"context"
	"fmt"
	"strings"
)

// Verdict is the CI gate's answer.
type Verdict struct {
	OK     bool
	Reason string // green, pending, failed or absent
	Detail string
	URL    string
}

var passing = map[string]bool{"success": true, "skipped": true, "neutral": true}

// Evaluate decides whether CI allows deploying HEAD, given HEAD and its
// ancestors (newest first) and the repo's recent runs.
//
// It does not demand a run for HEAD itself: workflows often skip commits by
// path (a docs-only change gets no run at all), and a gate that refuses a
// README change gets switched off. The newest commit that CI covered decides;
// the commits after it were judged unable to affect the build.
func Evaluate(commits []string, runs []CIRun) Verdict {
	bySha := map[string][]CIRun{}
	for _, r := range runs {
		if r.HeadSha != "" {
			bySha[r.HeadSha] = append(bySha[r.HeadSha], r)
		}
	}
	for _, sha := range commits {
		hits := bySha[sha]
		if len(hits) == 0 {
			continue
		}
		short := sha
		if len(short) > 7 {
			short = short[:7]
		}
		var pending, failed []CIRun
		for _, r := range hits {
			switch {
			case r.Status != "completed":
				pending = append(pending, r)
			case !passing[r.Conclusion]:
				failed = append(failed, r)
			}
		}
		if len(pending) > 0 {
			return Verdict{Reason: "pending", URL: pending[0].URL,
				Detail: fmt.Sprintf("CI is still running for %s (%s). Wait for it to finish.", short, names(pending, false))}
		}
		if len(failed) > 0 {
			return Verdict{Reason: "failed", URL: failed[0].URL,
				Detail: fmt.Sprintf("CI failed for %s: %s", short, names(failed, true))}
		}
		return Verdict{OK: true, Reason: "green", Detail: fmt.Sprintf("CI green for %s (%s)", short, names(hits, false))}
	}
	return Verdict{Reason: "absent", Detail: fmt.Sprintf(
		"no CI run found for HEAD or any of its %d most recent ancestors. Push the branch and let CI run.", len(commits))}
}

func names(runs []CIRun, withConclusion bool) string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = r.WorkflowName
		if withConclusion {
			out[i] += "=" + r.Conclusion
		}
	}
	return strings.Join(out, ", ")
}

// checkCI runs the gate. A CI that cannot answer is not a green CI: the
// deploy stops.
func checkCI(ctx context.Context, ci CI) (Verdict, error) {
	commits, err := ci.Commits(ctx)
	if err != nil {
		return Verdict{}, fmt.Errorf("the CI gate could not check CI, so it is refusing to deploy: %w", err)
	}
	runs, err := ci.Runs(ctx)
	if err != nil {
		return Verdict{}, fmt.Errorf("the CI gate could not check CI, so it is refusing to deploy: %w", err)
	}
	return Evaluate(commits, runs), nil
}
