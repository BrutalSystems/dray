// Package server is dray-server's deploy-on-merge loop: a GitHub App webhook
// says a CI run finished on a repo's main branch; once every CI run for that
// commit has passed, Dray downloads the commit, reads each app's
// .dray/config.yaml in it, and deploys every environment that sets
// on_merge: deploy, with the same code the CLI uses. Each result is reported
// on the commit as a GitHub check run.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/BrutalSystems/dray/internal/githubapp"
)

// RepoAPI is what the server needs from GitHub for one installation.
type RepoAPI interface {
	RunsForCommit(ctx context.Context, repo, sha string) ([]githubapp.Run, error)
	ChecksForCommit(ctx context.Context, repo, sha string) ([]githubapp.CheckRun, error)
	Tarball(ctx context.Context, repo, ref string, w io.Writer) error
	CreateCheck(ctx context.Context, repo, sha string, k githubapp.Check) (int64, error)
	UpdateCheck(ctx context.Context, repo string, id int64, k githubapp.Check) error
}

// GitHub gives the API for an installation of the App.
type GitHub interface {
	Installation(id int64) RepoAPI
}

// App is one GitHub App the server answers for. A private App can be
// installed only in the organization that owns it, so a server deploying
// repos of several organizations has one App per organization, all sending
// to the same address.
type App struct {
	ID int64
	// WebhookSecret checks the signature of the App's webhooks.
	WebhookSecret []byte
	GitHub        GitHub
}

// Trigger is a commit on a repo's default branch whose CI finished.
type Trigger struct {
	Repo         string // owner/name
	SHA          string
	App          int64
	Installation int64
}

// Server receives webhooks and runs deploys.
type Server struct {
	// Apps are the GitHub Apps whose webhooks are accepted, by App ID.
	Apps map[int64]App
	// Deploy handles one trigger after its CI passed.
	Deploy func(ctx context.Context, api RepoAPI, t Trigger)
	Log    *slog.Logger

	mu      sync.Mutex
	queues  map[string]*queue
	handled map[string]bool // repo@sha already taken
	wg      sync.WaitGroup
	recent  []Record
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Record is one thing the server did with a commit, for /status.
type Record struct {
	Time   time.Time `json:"time"`
	Repo   string    `json:"repo"`
	SHA    string    `json:"sha"`
	What   string    `json:"what"`
	Result string    `json:"result"`
	URL    string    `json:"url,omitempty"`
}

// keepRecords is how many records /status shows.
const keepRecords = 100

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Note records an outcome for /status. The Deployer calls it for each
// environment it deploys.
func (s *Server) Note(r Record) {
	if r.Time.IsZero() {
		r.Time = s.now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recent = append(s.recent, r)
	if len(s.recent) > keepRecords {
		s.recent = s.recent[len(s.recent)-keepRecords:]
	}
}

// Recent are the newest records first.
func (s *Server) Recent() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, len(s.recent))
	for i, r := range s.recent {
		out[len(s.recent)-1-i] = r
	}
	return out
}

type queue struct {
	running bool
	pending *Trigger
}

func (s *Server) log() *slog.Logger {
	if s.Log != nil {
		return s.Log
	}
	return slog.Default()
}

// Handler serves /healthz and /webhooks/github.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("POST /webhooks/github", s.webhook)
	// Not public: the ingress exposes only the webhook and the health check.
	mux.HandleFunc("GET /status", s.status)
	return mux
}

type workflowRunEvent struct {
	Action      string `json:"action"`
	WorkflowRun struct {
		HeadBranch string `json:"head_branch"`
		HeadSHA    string `json:"head_sha"`
		Event      string `json:"event"`
	} `json:"workflow_run"`
	Repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

// verify finds the App a webhook came from and checks its signature with
// that App's secret. GitHub names the App in X-GitHub-Hook-Installation-
// Target-ID; without it, each App's secret is tried.
func (s *Server) verify(r *http.Request, body []byte) (int64, bool) {
	sig := r.Header.Get("X-Hub-Signature-256")
	if id, err := strconv.ParseInt(r.Header.Get("X-GitHub-Hook-Installation-Target-ID"), 10, 64); err == nil {
		a, ok := s.Apps[id]
		return id, ok && githubapp.VerifySignature(a.WebhookSecret, body, sig)
	}
	for id, a := range s.Apps {
		if githubapp.VerifySignature(a.WebhookSecret, body, sig) {
			return id, true
		}
	}
	return 0, false
}

func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 25<<20))
	if err != nil {
		http.Error(w, "reading the body", http.StatusBadRequest)
		return
	}
	app, ok := s.verify(r, body)
	if !ok {
		s.log().Warn("webhook with a bad signature refused", "delivery", r.Header.Get("X-GitHub-Delivery"))
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	switch r.Header.Get("X-GitHub-Event") {
	case "ping":
		_, _ = w.Write([]byte("pong\n"))
		return
	case "workflow_run":
	default:
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var ev workflowRunEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		http.Error(w, "not a workflow_run event", http.StatusBadRequest)
		return
	}
	run := ev.WorkflowRun
	// Only pushes to the default branch deploy; pull requests and other
	// branches never do.
	if ev.Action != "completed" || run.Event != "push" || run.HeadBranch == "" ||
		run.HeadBranch != ev.Repository.DefaultBranch || ev.Installation.ID == 0 || run.HeadSHA == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.Enqueue(Trigger{Repo: ev.Repository.FullName, SHA: run.HeadSHA, App: app, Installation: ev.Installation.ID})
	w.WriteHeader(http.StatusAccepted)
}

// Enqueue queues a trigger. Each repo deploys one commit at a time; a newer
// trigger replaces one still waiting, so a burst of merges deploys the latest.
func (s *Server) Enqueue(t Trigger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queues == nil {
		s.queues = map[string]*queue{}
		s.handled = map[string]bool{}
	}
	if s.handled[t.Repo+"@"+t.SHA] {
		return
	}
	q := s.queues[t.Repo]
	if q == nil {
		q = &queue{}
		s.queues[t.Repo] = q
	}
	q.pending = &t
	if q.running {
		return
	}
	q.running = true
	s.wg.Add(1)
	go s.work(q)
}

func (s *Server) work(q *queue) {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		t := q.pending
		q.pending = nil
		if t == nil {
			q.running = false
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
		s.handle(context.Background(), *t)
	}
}

// Wait waits for every queued deploy to finish, for tests and shutdown.
func (s *Server) Wait() { s.wg.Wait() }

// handle deploys a trigger once every CI run for its commit has passed. A run
// still going means another webhook will come; a failed one means no deploy.
func (s *Server) handle(ctx context.Context, t Trigger) {
	log := s.log().With("repo", t.Repo, "sha", t.SHA)
	app, ok := s.Apps[t.App]
	if !ok {
		log.Error("no such App", "app", t.App)
		return
	}
	api := app.GitHub.Installation(t.Installation)
	runs, err := api.RunsForCommit(ctx, t.Repo, t.SHA)
	if err != nil {
		log.Error("cannot read CI runs; not deploying", "err", err)
		return
	}
	switch verdict(runs) {
	case "pending":
		log.Info("CI still running; waiting for its next webhook")
		return
	case "failed":
		log.Info("CI failed; not deploying")
		s.markHandled(t)
		s.Note(Record{Repo: t.Repo, SHA: t.SHA, What: "CI", Result: "failed; not deployed"})
		return
	}
	// GitHub keeps what this server did: a commit that already carries one of
	// this App's deploy checks, finished or recently started, was handled
	// before (perhaps by the server before a restart) and is not deployed again.
	if done, why := s.alreadyDeployed(ctx, api, t); done {
		log.Info("already handled; not deploying again", "why", why)
		s.markHandled(t)
		s.Note(Record{Repo: t.Repo, SHA: t.SHA, What: "deploy", Result: "skipped: " + why})
		return
	}
	s.markHandled(t)
	log.Info("CI passed; deploying")
	s.Deploy(ctx, api, t)
}

// abandonedAfter is how long an unfinished deploy check counts as running.
// Past it, the server that started it is taken to have died mid-deploy.
const abandonedAfter = time.Hour

func (s *Server) alreadyDeployed(ctx context.Context, api RepoAPI, t Trigger) (bool, string) {
	checks, err := api.ChecksForCommit(ctx, t.Repo, t.SHA)
	if err != nil {
		// Unreadable is not "nothing there": deploying twice is worse than
		// waiting for the next CI webhook.
		return true, "could not read the commit's checks: " + err.Error()
	}
	for _, c := range checks {
		if c.App.ID != t.App || !strings.HasPrefix(c.Name, "dray") {
			continue
		}
		if c.Status == "completed" {
			return true, "check \"" + c.Name + "\" already " + c.Conclusion
		}
		if s.now().Sub(c.StartedAt) < abandonedAfter {
			return true, "check \"" + c.Name + "\" is in progress"
		}
	}
	return false, ""
}

func (s *Server) markHandled(t Trigger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handled[t.Repo+"@"+t.SHA] = true
}

var passing = map[string]bool{"success": true, "skipped": true, "neutral": true}

// verdict is green, pending or failed for one commit's runs. No runs at all
// is pending: the webhook that woke us belongs to a run GitHub will list.
func verdict(runs []githubapp.Run) string {
	if len(runs) == 0 {
		return "pending"
	}
	for _, r := range runs {
		if r.Status != "completed" {
			return "pending"
		}
	}
	for _, r := range runs {
		if !passing[r.Conclusion] {
			return "failed"
		}
	}
	return "green"
}

// status lists recent outcomes, newest first: as a table, or JSON with
// ?format=json.
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	recs := s.Recent()
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(recs)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if len(recs) == 0 {
		_, _ = fmt.Fprintln(w, "Nothing yet since the server started.")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TIME (UTC)\tREPO\tCOMMIT\tWHAT\tRESULT\tCHECK")
	for _, rec := range recs {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", rec.Time.UTC().Format("2006-01-02 15:04"),
			rec.Repo, shortSHA(rec.SHA), rec.What, rec.Result, rec.URL)
	}
	_ = tw.Flush()
}
