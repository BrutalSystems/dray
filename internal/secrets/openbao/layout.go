package openbao

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Layout says where a logical path lives inside the mount, so Dray follows
// the vault's own naming standard instead of imposing one.
//
// A logical path is <app>/<name> (name may have further segments). Template
// spells the location with placeholders, one per segment:
//
//	{app}/{name}                    the default: secret:shop/app is shop/app
//	dray/{app}/{name}               everything under one folder
//	{client}/{app}/{env}/{name}     per client and per environment
//
// {app} and {name} are required, and {name} comes last. {client} is looked up
// in Clients by app; {env} is Env, the environment being deployed or imported.
type Layout struct {
	Template string
	// Clients maps each app to its client, for {client}.
	Clients map[string]string
	// Env fills {env}.
	Env string
}

var (
	literalRE = regexp.MustCompile(`^[A-Za-z0-9_][-A-Za-z0-9_.]*$`)
	valueRE   = regexp.MustCompile(`^[a-z0-9][-a-z0-9_.]*$`)
)

const defaultTemplate = "{app}/{name}"

// errNoClient means an app has no client, so it has no secrets here.
var errNoClient = errors.New("no client set for app")

func (l Layout) check() error {
	if l.Template == "" {
		return nil
	}
	segs := strings.Split(l.Template, "/")
	seen := map[string]int{}
	for i, s := range segs {
		switch s {
		case "{app}", "{name}", "{client}", "{env}":
			seen[s]++
			if s == "{name}" && i != len(segs)-1 {
				return fmt.Errorf("layout %q: {name} must come last", l.Template)
			}
		default:
			if !literalRE.MatchString(s) {
				return fmt.Errorf("layout %q: bad segment %q", l.Template, s)
			}
		}
	}
	for p, n := range seen {
		if n > 1 {
			return fmt.Errorf("layout %q: %s appears %d times", l.Template, p, n)
		}
	}
	if seen["{app}"] != 1 || seen["{name}"] != 1 {
		return fmt.Errorf("layout %q: needs {app} and {name}", l.Template)
	}
	if seen["{env}"] == 1 && l.Env != "" && !valueRE.MatchString(l.Env) {
		return fmt.Errorf("layout: bad environment %q", l.Env)
	}
	for app, c := range l.Clients {
		if !valueRE.MatchString(c) {
			return fmt.Errorf("layout: app %s has a bad client %q", app, c)
		}
	}
	return nil
}

func (l Layout) template() string {
	if l.Template == "" {
		return defaultTemplate
	}
	return l.Template
}

// folder is where an app's secrets live.
func (l Layout) folder(app string) (string, error) {
	segs := strings.Split(l.template(), "/")
	out := make([]string, 0, len(segs)-1)
	for _, s := range segs[:len(segs)-1] {
		switch s {
		case "{app}":
			s = app
		case "{env}":
			if l.Env == "" {
				return "", errors.New("this store's layout puts secrets per environment: name one (--env)")
			}
			s = l.Env
		case "{client}":
			c := l.Clients[app]
			if c == "" {
				return "", fmt.Errorf("%w %s in the store's layout (clients)", errNoClient, app)
			}
			s = c
		}
		out = append(out, s)
	}
	return strings.Join(out, "/"), nil
}

// location is where a logical path lives.
func (l Layout) location(path string) (string, error) {
	app, rest, _ := strings.Cut(path, "/")
	f, err := l.folder(app)
	if err != nil {
		return "", err
	}
	return f + "/" + rest, nil
}

// appsBase is the folder whose subfolders are apps, when the template has
// only literals before {app}; ok is false otherwise.
func (l Layout) appsBase() (base string, ok bool) {
	before, _, _ := strings.Cut(l.template(), "{app}")
	if strings.Contains(before, "{") {
		return "", false
	}
	return strings.TrimSuffix(before, "/"), true
}

// knownApps are the apps Clients names, sorted.
func (l Layout) knownApps() []string {
	out := make([]string, 0, len(l.Clients))
	for a := range l.Clients {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
