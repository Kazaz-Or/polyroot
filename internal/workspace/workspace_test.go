package workspace

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Kazaz-Or/polyroot/internal/config"
)

const fixture = `
version: 1
defaultAgent: claude
repos:
  payments-api: git/payments api
  payments-web: git/payments-web
  gaming-api: git/gaming-api
  shared-sdk: git/shared-sdk
  helm: git/helm
  terraform: git/terraform
groups:
  infra:
    repos: [helm, terraform]
  platform:
    repos: [shared-sdk]
    groups: [infra]
  everything:
    repos: [helm]          # duplicate via another path
    groups: [platform, infra]
workspaces:
  payments:
    primary: payments-api
    repos: [payments-web, shared-sdk]
    groups: [platform]
    context: contexts/payments.md
  gaming:
    primary: gaming-api
    groups: [everything]
  self:
    primary: helm
    repos: [helm]
    groups: [infra]
`

func setup(t *testing.T) *config.Config {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	for _, d := range []string{"payments api", "payments-web", "gaming-api", "shared-sdk", "helm", "terraform"} {
		if err := os.MkdirAll(filepath.Join(root, "git", d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must(t, os.MkdirAll(filepath.Join(root, "git/payments api/.git"), 0o755)) // the others are plain folders
	must(t, os.WriteFile(filepath.Join(root, "git/payments-web/AGENTS.md"), []byte("# web"), 0o644))
	must(t, os.WriteFile(filepath.Join(root, "git/helm/CLAUDE.md"), []byte("# helm"), 0o644))
	must(t, os.MkdirAll(filepath.Join(root, "contexts"), 0o755))
	must(t, os.WriteFile(filepath.Join(root, "contexts/payments.md"), []byte("# Payments\n\nweb -> api -> worker"), 0o644))
	cfg, err := config.Parse([]byte(fixture), filepath.Join(root, "config.yaml"))
	must(t, err)
	if errs := cfg.Check([]string{"claude"}); len(errs) > 0 {
		t.Fatal(errs)
	}
	return cfg
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestMembersOrderAndDedup(t *testing.T) {
	cfg := setup(t)
	for _, c := range []struct {
		ws        string
		want, via []string
	}{
		// primary, direct repos, then groups depth-first; shared-sdk listed directly wins.
		{"payments", []string{"payments-api", "payments-web", "shared-sdk", "helm", "terraform"}, []string{"", "", "", "infra", "infra"}},
		// nested groups, a repo reachable three ways appears once.
		{"gaming", []string{"gaming-api", "helm", "shared-sdk", "terraform"}, []string{"", "everything", "platform", "infra"}},
		// primary repeated in repos and groups.
		{"self", []string{"helm", "terraform"}, []string{"", "infra"}},
	} {
		got, via, err := Members(cfg, c.ws)
		must(t, err)
		if !reflect.DeepEqual(got, c.want) || !reflect.DeepEqual(via, c.via) {
			t.Errorf("%s: got %v via %v, want %v via %v", c.ws, got, via, c.want, c.via)
		}
	}
	if _, _, err := Members(cfg, "nope"); err == nil {
		t.Error("expected unknown workspace error")
	}
}

func TestDeterministic(t *testing.T) {
	cfg := setup(t)
	first, _, _ := Members(cfg, "gaming")
	for range 50 {
		again, _, _ := Members(cfg, "gaming")
		if !reflect.DeepEqual(first, again) {
			t.Fatal("non-deterministic order")
		}
	}
}

func TestResolveSharesPhysicalRepos(t *testing.T) {
	cfg := setup(t)
	pay, err := Resolve(cfg, "payments")
	must(t, err)
	gam, err := Resolve(cfg, "gaming")
	must(t, err)
	find := func(r *Resolved, name string) Repo {
		for _, x := range r.Repos {
			if x.Name == name {
				return x
			}
		}
		t.Fatalf("%s missing from %s", name, r.Name)
		return Repo{}
	}
	if find(pay, "helm").Path != find(gam, "helm").Path {
		t.Error("helm should be the same physical path in both workspaces")
	}
	if pay.Primary().Name != "payments-api" || !strings.HasSuffix(pay.Primary().Path, "payments api") {
		t.Errorf("primary: %+v", pay.Primary())
	}
	if len(pay.Additional()) != 4 {
		t.Errorf("additional: %+v", pay.Additional())
	}
	if got := find(pay, "payments-web").Instructions; !reflect.DeepEqual(got, []string{"AGENTS.md"}) {
		t.Errorf("instructions: %v", got)
	}
}

func TestResolveFailsOnMissingRepo(t *testing.T) {
	cfg := setup(t)
	must(t, os.RemoveAll(cfg.Repos["terraform"].Path))
	cfg, err := config.Parse([]byte(fixture), cfg.File) // re-stat paths
	must(t, err)
	_, err = Resolve(cfg, "payments")
	if err == nil || !strings.Contains(err.Error(), "terraform") || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected missing terraform error, got %v", err)
	}
	if _, err := Resolve(cfg, "self"); err == nil {
		t.Fatal("self also uses terraform")
	}
}

func TestInstructions(t *testing.T) {
	cfg := setup(t)
	ws, err := Resolve(cfg, "payments")
	must(t, err)
	text, err := Instructions(ws)
	must(t, err)
	for _, want := range []string{
		"# Polyroot Workspace: payments",
		"- payments-api: " + ws.Primary().Path + "\n", // Git repo: no marker
		"/payments-web (no Git) [AGENTS.md]\n",
		"/helm (no Git) [CLAUDE.md]\n",
		"Each repository is independent",
		"web -> api -> worker\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("instructions missing %q:\n%s", want, text)
		}
	}
	gam, _ := Resolve(cfg, "gaming")
	text, _ = Instructions(gam)
	if strings.Contains(text, "---") {
		t.Error("workspace without context should not include a context section")
	}
}
