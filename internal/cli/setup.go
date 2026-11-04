package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/githubapp"
	"github.com/BrutalSystems/dray/internal/secrets"
)

// NewSetupCmd is `dray-server setup`: one-time setup steps.
func NewSetupCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "setup", Short: "One-time setup of what the server needs"}
	cmd.AddCommand(newSetupGitHubAppCmd())
	return cmd
}

type setupAppFlags struct {
	org, name, homepage, webhookURL, to, env, cfgPath, repo string
	overwrite, noBrowser                                    bool
}

func newSetupGitHubAppCmd() *cobra.Command {
	var f setupAppFlags
	cmd := &cobra.Command{
		Use:   "github-app",
		Short: "Create Dray's GitHub App and store its credentials in the secret store",
		Long: "Create the GitHub App dray-server runs as, through GitHub's manifest flow: this\n" +
			"opens github.com with every setting filled in, an org admin presses Create, and\n" +
			"GitHub hands the new App's ID, private key and webhook secret back to this\n" +
			"command on your machine, which writes them straight to the secret store.\n" +
			"Nothing secret is printed.\n\n" +
			"The App can read code and CI runs, write check runs, and hears when workflow\n" +
			"runs finish. Its webhook stays off until --webhook-url is given (or it is set\n" +
			"later in the App's settings).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetupGitHubApp(cmd, f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.org, "org", "", "GitHub organization that owns the App (required)")
	fl.StringVar(&f.name, "name", "Dray", "the App's name (unique on GitHub)")
	fl.StringVar(&f.homepage, "homepage", "https://github.com/BrutalSystems/dray", "the App's homepage")
	fl.StringVar(&f.webhookURL, "webhook-url", "", "dray-server's public webhook address, https://<host>/webhooks/github (default: off for now)")
	fl.StringVar(&f.to, "to", "", "where to store the credentials, secret:<app>/<name> (required)")
	fl.StringVar(&f.env, "env", "prod", "environment, for stores that keep secrets per environment")
	fl.StringVar(&f.cfgPath, "config", "", "your own settings file (default $DRAY_CONFIG or ~/.config/dray/config.yaml)")
	fl.StringVar(&f.repo, "repo", ".", "a repo whose .dray/config.yaml names the secret store")
	fl.BoolVar(&f.overwrite, "overwrite", false, "replace credentials already stored there")
	fl.BoolVar(&f.noBrowser, "no-browser", false, "print the address to open instead of opening it")
	return cmd
}

var setupPage = template.Must(template.New("p").Parse(`<!doctype html>
<meta charset="utf-8"><title>Create the {{.Name}} GitHub App</title>
<body style="font-family: system-ui; max-width: 40em; margin: 3em auto">
<h1>Create the {{.Name}} GitHub App</h1>
<p>GitHub will show the App's settings for <b>{{.Org}}</b>. Press <b>Create GitHub App</b> there; this page's command then stores its credentials.</p>
<form id="f" method="post" action="{{.Action}}">
<input type="hidden" name="manifest" value="{{.Manifest}}">
<button type="submit">Continue to GitHub</button>
</form>
<script>document.getElementById("f").submit()</script>
`))

type setupResult struct {
	app *githubapp.Created
	err error
}

func runSetupGitHubApp(cmd *cobra.Command, f setupAppFlags) error {
	if f.org == "" || f.to == "" {
		return errors.New("--org and --to are required")
	}
	ref := config.SecretRef(f.to)
	path, key, err := secrets.SplitRef(ref)
	if err != nil {
		return err
	}
	if key != "" {
		return errors.New("--to names a whole secret, not one key")
	}
	ctx := cmd.Context()
	store, err := openStore(f.repo, f.cfgPath, f.env)
	if err != nil {
		return err
	}
	if _, err := store.Get(ctx, path); err == nil && !f.overwrite {
		return fmt.Errorf("%s already holds credentials; pass --overwrite to replace them", f.to)
	} else if err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return err
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	base := "http://" + ln.Addr().String()
	var st [16]byte
	_, _ = rand.Read(st[:])
	state := hex.EncodeToString(st[:])
	webhook, active := f.webhookURL, f.webhookURL != ""
	if !active {
		// GitHub wants an address even for a webhook that is off.
		webhook = "https://dray.invalid/webhooks/github"
	}
	m, err := json.Marshal(githubapp.DrayManifest(f.name, f.homepage, webhook, base+"/callback", active))
	if err != nil {
		return err
	}

	done := make(chan setupResult, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		_ = setupPage.Execute(w, map[string]string{
			"Name": f.name, "Org": f.org, "Action": githubapp.NewAppURL(f.org, state), "Manifest": string(m),
		})
	})
	mux.HandleFunc("GET /callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "this answer is not for this setup", http.StatusBadRequest)
			return
		}
		app, err := githubapp.Convert(r.Context(), nil, "", r.URL.Query().Get("code"))
		if err == nil {
			err = store.Put(r.Context(), path, secrets.Values{
				"app_id":         strconv.FormatInt(app.ID, 10),
				"slug":           app.Slug,
				"private_key":    app.PEM,
				"webhook_secret": app.WebhookSecret,
			})
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err != nil {
			http.Error(w, "Setup failed: "+err.Error(), http.StatusInternalServerError)
		} else {
			_, _ = fmt.Fprintf(w, "The %s GitHub App was created and its credentials stored. You can close this tab.\n", app.Slug) //nolint:gosec // plain text, not HTML
		}
		select {
		case done <- setupResult{app, err}:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Open %s to create the %s GitHub App in %s.\n", base, f.name, f.org)
	if !f.noBrowser {
		openBrowser(base)
	}
	select {
	case res := <-done:
		if res.err != nil {
			return res.err
		}
		a := res.app
		_, _ = fmt.Fprintf(out, "\nCreated the GitHub App %s (ID %d): %s\n", a.Slug, a.ID, a.HTMLURL)
		_, _ = fmt.Fprintf(out, "Stored at %s (keys: app_id, private_key, slug, webhook_secret); nothing secret was printed.\n", f.to)
		_, _ = fmt.Fprintf(out, "Install it on the repos Dray deploys: %s/installations/new\n", a.HTMLURL)
		if !active {
			_, _ = fmt.Fprintln(out, "Its webhook is off: set the address once dray-server is reachable.")
		}
		return nil
	case <-time.After(30 * time.Minute):
		return errors.New("no answer from GitHub within 30 minutes")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func openBrowser(u string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		c = exec.Command("open", u) //nolint:gosec // our own localhost address
	case "linux":
		c = exec.Command("xdg-open", u) //nolint:gosec // same
	default:
		return
	}
	_ = c.Start()
}
