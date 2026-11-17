package mint

import (
	"context"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type repository struct {
	Root  string
	Owner string
	Name  string
}

var (
	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,100}$`)
)

func parseRemote(remote string) (repository, error) {
	var path string
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		path = strings.TrimPrefix(remote, "git@github.com:")
	case strings.HasPrefix(remote, "https://"), strings.HasPrefix(remote, "ssh://"):
		u, err := url.Parse(remote)
		if err != nil || u.Host != "github.com" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			return repository{}, errors.New("remote must target github.com without a port, query, or fragment")
		}
		if u.Scheme == "https" && u.User != nil {
			return repository{}, errors.New("credentials in remote URLs are not supported")
		}
		if u.Scheme == "ssh" && u.User != nil && u.User.String() != "git" {
			return repository{}, errors.New("SSH remote user must be git")
		}
		path = strings.TrimPrefix(u.Path, "/")
	default:
		return repository{}, errors.New("remote must be an HTTPS or SSH GitHub.com repository URL")
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return repository{}, errors.New("remote must name exactly one owner and repository")
	}
	name := strings.TrimSuffix(parts[1], ".git")
	if !ownerPattern.MatchString(parts[0]) || !repoPattern.MatchString(name) || name == "." || name == ".." {
		return repository{}, errors.New("invalid GitHub owner or repository name")
	}
	return repository{Owner: parts[0], Name: name}, nil
}

func discoverRepository(ctx context.Context, remote string) (repository, error) {
	git, err := exec.LookPath("git")
	if err != nil {
		return repository{}, errors.New("git is required on PATH")
	}
	env := gitEnvironment(os.Environ())
	command := exec.CommandContext(ctx, git, "rev-parse", "--show-toplevel")
	command.Env = env
	output, err := command.Output()
	if err != nil {
		return repository{}, errors.New("run gh mint inside a Git working tree")
	}
	root, err := canonicalPath(strings.TrimSpace(string(output)))
	if err != nil {
		return repository{}, err
	}

	command = exec.CommandContext(ctx, git, "-C", root, "remote", "get-url", remote)
	command.Env = env
	output, err = command.Output()
	if err != nil {
		return repository{}, errors.New("cannot resolve the configured Git remote")
	}
	repo, err := parseRemote(strings.TrimSpace(string(output)))
	if err != nil {
		return repository{}, err
	}
	repo.Root = root
	return repo, nil
}

func gitEnvironment(environ []string) []string {
	env := make([]string, 0, len(environ))
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") {
			continue
		}
		env = append(env, entry)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0")
}

func protectPaths(repo repository, dir string, cfg config) ([]string, error) {
	key, err := canonicalPath(cfg.KeyFile)
	if err != nil {
		return nil, err
	}
	if key != cfg.KeyFile {
		return nil, errors.New("private key path changed; update the configuration with its canonical path")
	}
	keyDirectory := filepath.Dir(key)
	if within(keyDirectory, repo.Root) || within(repo.Root, keyDirectory) {
		return nil, errors.New("private key directory must be outside and not contain the working tree")
	}
	configDir, err := canonicalPath(dir)
	if err != nil {
		return nil, err
	}
	if within(configDir, repo.Root) || within(repo.Root, configDir) {
		return nil, errors.New("gh-mint configuration directory must be outside and not contain the working tree")
	}
	if strings.ContainsAny(keyDirectory+configDir, "*?[]") {
		return nil, errors.New("private key and configuration directories cannot contain glob metacharacters")
	}
	if within(keyDirectory, cfg.Copilot) {
		return nil, errors.New("copilot executable must be outside the private key directory")
	}

	denied := []string{keyDirectory, configDir}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, errors.New("cannot resolve the user home directory")
	}
	for _, name := range []string{".ssh", ".aws", ".azure", ".gnupg", ".kube", ".gitconfig", ".git-credentials", ".netrc", "_netrc", ".npmrc", ".pypirc"} {
		denied = append(denied, filepath.Join(home, name))
	}
	copilotHome := os.Getenv("COPILOT_HOME")
	if copilotHome == "" {
		copilotHome = filepath.Join(home, ".copilot")
	}
	if !filepath.IsAbs(copilotHome) {
		return nil, errors.New("inherited COPILOT_HOME must be absolute")
	}
	denied = append(denied, copilotHome, filepath.Join(home, ".copilot"), filepath.Join(home, ".claude"))

	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	defaultGHHome := filepath.Join(base, "gh")
	if os.PathSeparator == '\\' {
		base = os.Getenv("APPDATA")
		if base == "" {
			return nil, errors.New("cannot locate GitHub CLI credentials")
		}
		defaultGHHome = filepath.Join(base, "GitHub CLI")
	}
	ghHome := os.Getenv("GH_CONFIG_DIR")
	if ghHome == "" {
		ghHome = defaultGHHome
	}
	if !filepath.IsAbs(ghHome) {
		return nil, errors.New("inherited GH_CONFIG_DIR must be absolute")
	}
	denied = append(denied, ghHome, defaultGHHome)
	return denied, nil
}
