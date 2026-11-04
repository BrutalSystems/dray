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
			"Settings come from the environment:\n" +
			"  DRAY_GITHUB_APP_ID          the GitHub App's ID\n" +
			"  DRAY_GITHUB_APP_KEY_FILE    its private key (PEM)\n" +
			"  DRAY_WEBHOOK_SECRET_FILE    its webhook secret\n" +
			"  DRAY_BUILD_NAMESPACE        where build pods run (default dray)\n" +
			"  DRAY_BUILD_MEMORY, _CPU     what a build pod reserves (default 6Gi, 2)\n" +
			"  DRAY_BUILDKIT_ADDR          a long-running BuildKit to use instead of build pods\n" +
			"AWS, Kubernetes and the secret store use the pod's own identity.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, err := newServer()
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

func newServer() (*server.Server, error) {
	idStr, err := readSetting("DRAY_GITHUB_APP_ID")
	if err != nil {
		return nil, err
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("DRAY_GITHUB_APP_ID: %w", err)
	}
	keyPEM, err := readSettingFile("DRAY_GITHUB_APP_KEY_FILE")
	if err != nil {
		return nil, err
	}
	key, err := githubapp.ParseKey(keyPEM)
	if err != nil {
		return nil, err
	}
	secret, err := readSettingFile("DRAY_WEBHOOK_SECRET_FILE")
	if err != nil {
		return nil, err
	}
	d := &server.Deployer{Adapters: inClusterAdapters}
	return &server.Server{
		WebhookSecret: []byte(strings.TrimSpace(string(secret))),
		GitHub:        appInstallations{&githubapp.App{ID: id, Key: key}},
		Deploy:        d.Deploy,
		Log:           slog.New(slog.NewJSONHandler(os.Stderr, nil)),
	}, nil
}

// inClusterAdapters are the adapters for a deploy run by the server: its own
// AWS identity (no profile), kubectl with the pod's service account, and the
// secret store the app's config names.
func inClusterAdapters(c *config.RepoConfig, env string, e config.Environment, t config.Target, out io.Writer) (server.Adapters, error) {
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
		s, err := stores.Open(repoStore(c), env)
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
