package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/BrutalSystems/dray/internal/config"
	"github.com/BrutalSystems/dray/internal/deploy"
	"github.com/BrutalSystems/dray/internal/deploy/buildkit"
	"github.com/BrutalSystems/dray/internal/deploy/ecr"
	"github.com/BrutalSystems/dray/internal/deploy/kubectl"
	"github.com/BrutalSystems/dray/internal/githubapp"
	"github.com/BrutalSystems/dray/internal/secrets/openbao"
	"github.com/BrutalSystems/dray/internal/secrets/stores"
	"github.com/BrutalSystems/dray/internal/server"
)

// NewServeCmd is `dray-server serve`: deploy on merge.
func NewServeCmd() *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Deploy apps when CI passes on their main branch",
		Long: "Receive GitHub App webhooks and, when every CI run for a commit on a repo's\n" +
			"default branch has passed, deploy each environment that sets on_merge: deploy.\n" +
			"Results are reported on the commit as check runs.\n\n" +
			"Settings come from the environment. The GitHub Apps (one per organization),\n" +
			"from the vault with the pod's Kubernetes identity:\n" +
			"  DRAY_VAULT_ADDR             OpenBao or Vault (default BAO_ADDR, then VAULT_ADDR)\n" +
			"  DRAY_VAULT_MOUNT            its KV version 2 mount (default kv)\n" +
			"  DRAY_VAULT_ROLE             the Kubernetes auth role that reads the Apps\n" +
			"  DRAY_GITHUB_APPS            the Apps' paths in the mount, comma-separated; each\n" +
			"                              holds app_id, private_key and webhook_secret\n" +
			"  DRAY_VAULT_APP_ROLE         role an app's deploy signs in as, from {client},\n" +
			"                              {app} and {env} (default kv-{client}-{app}-{env})\n" +
			"or one App from files: DRAY_GITHUB_APP_ID, DRAY_GITHUB_APP_KEY_FILE and\n" +
			"DRAY_WEBHOOK_SECRET_FILE. Builds:\n" +
			"  DRAY_BUILD_NAMESPACE        where build pods run (default dray)\n" +
			"  DRAY_BUILD_MEMORY, _CPU     what a build pod reserves (default 6Gi, 2)\n" +
			"  DRAY_BUILDKIT_ADDR          a long-running BuildKit to use instead of build pods\n" +
			"AWS, Kubernetes and the secret store use the pod's own identity.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newServer(cmd.Context())
			if err != nil {
				return err
			}
			return serve(cmd.Context(), listen, s)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", ":8080", "address to listen on")
	return cmd
}

func readSetting(name string) (string, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return "", fmt.Errorf("%s is not set", name)
	}
	return v, nil
}

func settingOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func readSettingFile(name string) ([]byte, error) {
	p, err := readSetting(name)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p) //nolint:gosec // a path the operator set
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return b, nil
}

type appInstallations struct{ app *githubapp.App }

func (a appInstallations) Installation(id int64) server.RepoAPI { return a.app.Installation(id) }

func newApp(id int64, keyPEM, secret []byte) (server.App, error) {
	key, err := githubapp.ParseKey(keyPEM)
	if err != nil {
		return server.App{}, err
	}
	return server.App{
		ID: id, WebhookSecret: []byte(strings.TrimSpace(string(secret))),
		GitHub: appInstallations{&githubapp.App{ID: id, Key: key}},
	}, nil
}

// vaultLogin is the server's Kubernetes sign-in to the vault, or nil when
// DRAY_VAULT_ROLE is not set.
func vaultLogin() *openbao.KubernetesLogin {
	if os.Getenv("DRAY_VAULT_ROLE") == "" {
		return nil
	}
	addr := settingOr("DRAY_VAULT_ADDR", openbao.EnvAddress())
	return &openbao.KubernetesLogin{Address: addr}
}

// loadApps reads the GitHub Apps: from the vault (DRAY_GITHUB_APPS), or one
// App from files.
func loadApps(ctx context.Context, login *openbao.KubernetesLogin) (map[int64]server.App, error) {
	apps := map[int64]server.App{}
	if paths := os.Getenv("DRAY_GITHUB_APPS"); paths != "" {
		if login == nil {
			return nil, errors.New("DRAY_GITHUB_APPS needs DRAY_VAULT_ROLE to read them")
		}
		store, err := openbao.New(openbao.Config{
			Address: login.Address, Mount: settingOr("DRAY_VAULT_MOUNT", "kv"),
			Token: login.Token(os.Getenv("DRAY_VAULT_ROLE")),
		})
		if err != nil {
			return nil, err
		}
		for _, p := range strings.Split(paths, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			v, err := store.Get(ctx, p)
			if err != nil {
				return nil, fmt.Errorf("GitHub App %s: %w", p, err)
			}
			id, err := strconv.ParseInt(v["app_id"], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("GitHub App %s: app_id: %w", p, err)
			}
			app, err := newApp(id, []byte(v["private_key"]), []byte(v["webhook_secret"]))
			if err != nil {
				return nil, fmt.Errorf("GitHub App %s: %w", p, err)
			}
			apps[id] = app
			slog.Info("GitHub App loaded", "app_id", id, "slug", v["slug"], "from", p)
		}
		return apps, nil
	}
	idStr, err := readSetting("DRAY_GITHUB_APP_ID")
	if err != nil {
		return nil, fmt.Errorf("no GitHub App: set DRAY_GITHUB_APPS, or %w", err)
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("DRAY_GITHUB_APP_ID: %w", err)
	}
	keyPEM, err := readSettingFile("DRAY_GITHUB_APP_KEY_FILE")
	if err != nil {
		return nil, err
	}
	secret, err := readSettingFile("DRAY_WEBHOOK_SECRET_FILE")
	if err != nil {
		return nil, err
	}
	app, err := newApp(id, keyPEM, secret)
	if err != nil {
		return nil, err
	}
	apps[id] = app
	return apps, nil
}

func newServer(ctx context.Context) (*server.Server, error) {
	login := vaultLogin()
	apps, err := loadApps(ctx, login)
	if err != nil {
		return nil, err
	}
	d := &server.Deployer{Adapters: func(c *config.RepoConfig, env string, e config.Environment, t config.Target, out io.Writer) (server.Adapters, error) {
		return inClusterAdapters(c, env, e, t, out, login)
	}}
	return &server.Server{
		Apps:   apps,
		Deploy: d.Deploy,
		Log:    slog.New(slog.NewJSONHandler(os.Stderr, nil)),
	}, nil
}

// appRole is the vault role an app's deploy signs in as.
func appRole(c *config.RepoConfig, env string) (string, error) {
	role := settingOr("DRAY_VAULT_APP_ROLE", "kv-{client}-{app}-{env}")
	client := ""
	if c.SecretStore != nil && c.SecretStore.OpenBao != nil {
		client = c.SecretStore.OpenBao.Client
	}
	if strings.Contains(role, "{client}") && client == "" {
		return "", errors.New("secret_store.openbao.client is not set, and the vault role needs it")
	}
	r := strings.NewReplacer("{client}", client, "{app}", c.Name, "{env}", env)
	return r.Replace(role), nil
}

// inClusterAdapters are the adapters for a deploy run by the server: its own
// AWS identity (no profile), kubectl with the pod's service account, and the
// secret store the app's config names.
func inClusterAdapters(c *config.RepoConfig, env string, e config.Environment, t config.Target, out io.Writer, login *openbao.KubernetesLogin) (server.Adapters, error) {
	if t.Cloud != "aws" {
		return server.Adapters{}, fmt.Errorf("target cloud %q is not supported", t.Cloud)
	}
	reg := &ecr.Registry{Account: t.Account, Region: t.Region}
	// No Docker in the server's image: the registry login goes into a config
	// file that buildctl hands to BuildKit for the push.
	dir, err := os.MkdirTemp("", "dray-docker-")
	if err != nil {
		return server.Adapters{}, err
	}
	reg.DockerConfig = dir
	kb := buildkit.Builder{
		DockerConfig: dir, Progress: out,
		Cache: func(ctx context.Context, ref string) (string, error) {
			repo := strings.TrimPrefix(ref, reg.Host()+"/")
			if i := strings.LastIndex(repo, ":"); i >= 0 {
				repo = repo[:i]
			}
			return reg.CacheRepo(ctx, repo)
		},
	}
	var builder deploy.Builder
	if addr := os.Getenv("DRAY_BUILDKIT_ADDR"); addr != "" {
		kb.Addr = addr
		builder = &kb
	} else {
		builder = &buildkit.PodBuilder{
			Builder:   kb,
			Namespace: settingOr("DRAY_BUILD_NAMESPACE", "dray"),
			Memory:    settingOr("DRAY_BUILD_MEMORY", "6Gi"),
			CPU:       settingOr("DRAY_BUILD_CPU", "2"),
			Arch:      strings.TrimPrefix(t.Platform, "linux/"),
		}
	}
	ad := server.Adapters{
		Registry: reg,
		Builder:  builder,
		Cluster:  &kubectl.Cluster{Namespace: e.Namespace, Progress: out},
	}
	if c.SecretStore != nil {
		// Each app's deploy signs in as that app's own role, so it reads its
		// own secrets and nothing else.
		var token func(context.Context) (string, error)
		if login != nil && c.SecretStore.Type == "openbao" {
			role, err := appRole(c, env)
			if err != nil {
				return server.Adapters{}, err
			}
			token = login.Token(role)
		}
		s, err := stores.OpenWithToken(repoStore(c), env, token)
		if err != nil {
			return server.Adapters{}, fmt.Errorf("secret_store: %w", err)
		}
		ad.Secrets = s
	}
	return ad, nil
}

func serve(ctx context.Context, listen string, s *server.Server) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &http.Server{Addr: listen, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	slog.Info("dray-server listening", "addr", listen)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Let running deploys finish; a deploy cut off mid-rollout is worse.
	s.Wait()
	return nil
}
