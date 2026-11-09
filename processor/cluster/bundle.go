package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var installMu sync.Mutex

func SafeRelative(name string) bool {
	return name != "" && name != "." && !filepath.IsAbs(name) && filepath.Clean(name) == name && !strings.Contains(name, "\\") && name != ".." && !strings.HasPrefix(name, "../")
}
func Digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func BuildBundle(dir, id string, p Program) ([]byte, string, error) {
	if p.Manifest != "" {
		return nil, "", errors.New("resolve manifest before building bundle")
	}
	if err := p.ValidateScript(); err != nil {
		return nil, "", err
	}
	if p.PriorityRank() < 1 || p.PriorityRank() > 5 {
		return nil, "", errors.New("rank must be between 1 and 5")
	}
	if p.Entrypoint == "" {
		p.Entrypoint = id
	}
	if p.TimeoutSeconds < -1 {
		return nil, "", errors.New("invalid timeout_seconds")
	}
	if p.AutoPublish.Directory != "" && p.AutoPublish.Directory != "output" && p.AutoPublish.Directory != "work" && p.AutoPublish.Directory != "both" {
		return nil, "", errors.New("auto_publish.directory must be output, work or both")
	}
	if p.AutoPublish.Enabled && (!SafeRelative(id) || strings.Contains(id, "/") || strings.HasPrefix(id, ".")) {
		return nil, "", errors.New("automatic publication requires a visible filename-safe program ID")
	}
	names := append([]string{p.Entrypoint}, p.Files...)
	if p.PackageLock != "" {
		names = append(names, p.PackageLock)
	}
	if p.RequirementsLock != "" {
		names = append(names, p.RequirementsLock)
	}
	b := Bundle{PackageLock: p.PackageLock, Script: p.Script, ProgramID: id, Entrypoint: p.Entrypoint, RequirementsLock: p.RequirementsLock, Files: map[string][]byte{"processor_publish.py": publicationHelper, "processor_supervise.py": publicationSupervisor}, Resources: p.Resources(), TimeoutSeconds: p.TimeoutSeconds, AutoPublish: p.AutoPublish}
	size := 0
	for _, name := range names {
		if name == "processor_publish.py" || name == "processor_supervise.py" {
			return nil, "", errors.New("processor helper filenames are reserved")
		}
		if !SafeRelative(name) {
			return nil, "", fmt.Errorf("invalid bundle file %q", name)
		}
		path := filepath.Join(dir, name)
		if p.Script != nil {
			if err := NoSymlinks(dir, name); err != nil {
				return nil, "", err
			}
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, "", err
		}
		root, err := filepath.Abs(dir)
		if err != nil {
			return nil, "", err
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return nil, "", err
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || !SafeRelative(rel) {
			return nil, "", errors.New("bundle file escapes program directory")
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, "", err
		}
		if !info.Mode().IsRegular() {
			return nil, "", errors.New("bundle requires regular files")
		}
		if info.Size() > MaxArtifactBytes {
			return nil, "", errors.New("bundle file too large")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", err
		}
		size += len(data)
		if size > MaxArtifactBytes {
			return nil, "", errors.New("bundle exceeds 64 MiB")
		}
		b.Files[name] = data
	}
	data, err := json.Marshal(b)
	if err != nil {
		return nil, "", err
	}
	return data, Digest(data), nil
}
func request(ctx context.Context, method, url, token string, body io.Reader) (*http.Response, error) {
	r, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	return (&http.Client{Timeout: 15e9, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(r)
}
func InstallBundle(ctx context.Context, root string, spec Install) (string, error) {
	installMu.Lock()
	defer installMu.Unlock()
	if _, err := exec.LookPath("python3"); err != nil {
		return "", errors.New("worker requires Python 3")
	}
	if len(spec.Version) != 64 {
		return "", errors.New("invalid version")
	}
	if _, err := hex.DecodeString(spec.Version); err != nil {
		return "", err
	}
	dst := filepath.Join(root, "bundles", spec.Version)
	if _, err := os.Stat(filepath.Join(dst, "ready")); err == nil {
		if _, err := LoadBundle(root, spec.Version); err == nil {
			return dst, nil
		}
		return "", errors.New("installed bundle was modified")
	}
	response, err := request(ctx, "GET", spec.BundleURL, spec.Token, nil)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", fmt.Errorf("bundle download HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 90<<20))
	if err != nil {
		return "", err
	}
	if Digest(data) != spec.Version {
		return "", errors.New("bundle checksum mismatch")
	}
	var b Bundle
	if err = json.Unmarshal(data, &b); err != nil {
		return "", err
	}
	if !SafeRelative(b.Entrypoint) || b.Files[b.Entrypoint] == nil {
		return "", errors.New("invalid entrypoint")
	}
	if b.Script != nil {
		if err := RuntimeAdmission(Program{Entrypoint: b.Entrypoint, Script: b.Script, PackageLock: b.PackageLock}, DetectRuntimeCapabilities()); err != "" {
			return "", errors.New(err)
		}
	}
	if b.PackageLock != "" && (!SafeRelative(b.PackageLock) || b.Files[b.PackageLock] == nil || b.Files[filepath.Join(filepath.Dir(b.PackageLock), "package.json")] == nil) {
		return "", errors.New("invalid package lock")
	}
	if b.RequirementsLock != "" && (!SafeRelative(b.RequirementsLock) || b.Files[b.RequirementsLock] == nil) {
		return "", errors.New("invalid requirements lock")
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return "", err
	}
	temp, err := os.MkdirTemp(filepath.Dir(dst), ".install-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temp)
	for name, content := range b.Files {
		if !SafeRelative(name) {
			return "", errors.New("invalid bundle member")
		}
		path := filepath.Join(temp, name)
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return "", err
		}
		if err = os.WriteFile(path, content, 0600); err != nil {
			return "", err
		}
	}
	if err = os.WriteFile(filepath.Join(temp, "bundle.json"), data, 0600); err != nil {
		return "", err
	}
	// Install environments at their permanent path: venv scripts embed this path.
	if err = os.RemoveAll(dst); err != nil {
		return "", err
	}
	if err = os.Rename(temp, dst); err != nil {
		return "", err
	}
	ready := false
	defer func() {
		if !ready {
			os.RemoveAll(dst)
		}
	}()
	if b.RequirementsLock != "" {
		venv := filepath.Join(dst, "venv")
		if err := runInstallCommand(exec.CommandContext(ctx, "python3", "-m", "venv", venv), spec.Version); err != nil {
			return "", fmt.Errorf("venv creation failed: %w", err)
		}
		install := exec.CommandContext(ctx, filepath.Join(venv, "bin", "python"), "-m", "pip", "install", "--disable-pip-version-check", "--no-input", "--require-hashes", "-r", filepath.Join(dst, b.RequirementsLock))
		install.Dir = dst
		if err := runInstallCommand(install, spec.Version); err != nil {
			return "", fmt.Errorf("dependency installation failed: %w", err)
		}
	}
	if b.PackageLock != "" {
		install := exec.CommandContext(ctx, "npm", "ci", "--omit=dev", "--ignore-scripts", "--no-audit", "--no-fund")
		install.Dir = filepath.Join(dst, filepath.Dir(b.PackageLock))
		if err := runInstallCommand(install, spec.Version); err != nil {
			return "", fmt.Errorf("npm installation failed: %w", err)
		}
	}
	if b.PackageLock != "" {
		hash, err := dependencyDigest(filepath.Join(dst, filepath.Dir(b.PackageLock), "node_modules"))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(dst, "node-dependencies.sha256"), []byte(hash), 0600); err != nil {
			return "", err
		}
	}
	if err = os.WriteFile(filepath.Join(dst, "ready"), []byte(spec.Version), 0600); err != nil {
		return "", err
	}
	ready = true
	return dst, nil
}
func InstalledVersions(root string) []string {
	entries, _ := os.ReadDir(filepath.Join(root, "bundles"))
	out := []string{}
	for _, e := range entries {
		if e.IsDir() && len(e.Name()) == 64 {
			if _, err := LoadBundle(root, e.Name()); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	return out
}

// LoadBundle verifies immutable program files before advertising or executing them.
func LoadBundle(root, version string) (*Bundle, error) {
	if len(version) != 64 {
		return nil, errors.New("invalid version")
	}
	if _, err := hex.DecodeString(version); err != nil {
		return nil, err
	}
	dir := filepath.Join(root, "bundles", version)
	if _, err := os.Stat(filepath.Join(dir, "ready")); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(dir, "bundle.json"))
	if err != nil {
		return nil, err
	}
	if Digest(data) != version {
		return nil, errors.New("bundle manifest checksum mismatch")
	}
	var b Bundle
	if err = json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	for name, expected := range b.Files {
		if !SafeRelative(name) {
			return nil, errors.New("invalid bundle member")
		}
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, errors.New("bundle member is not a regular file")
		}
		actual, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if Digest(actual) != Digest(expected) {
			return nil, errors.New("bundle member checksum mismatch")
		}
	}
	if b.PackageLock != "" {
		expected, err := os.ReadFile(filepath.Join(dir, "node-dependencies.sha256"))
		if err != nil {
			return nil, err
		}
		actual, err := dependencyDigest(filepath.Join(dir, filepath.Dir(b.PackageLock), "node_modules"))
		if err != nil || string(expected) != actual {
			return nil, errors.New("cached Node dependencies modified")
		}
	}
	return &b, nil
}

// Install tasks already reserve one CPU core and 512 MiB at the broker.
func runInstallCommand(cmd *exec.Cmd, version string) error {
	cleanup, err := configureCgroup(cmd, fmt.Sprintf("install-%s-%d", version[:12], time.Now().UnixNano()), Program{}.Resources())
	if err != nil {
		return err
	}
	defer cleanup()
	data, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %.4096s", err, data)
	}
	return nil
}

func dependencyDigest(root string) (string, error) {
	h := sha256.New()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%d:%s:%d:", len(rel), rel, d.Type())
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return err
			}
			inside, err := filepath.Rel(filepath.Dir(root), resolved)
			if err != nil || !SafeRelative(inside) {
				return errors.New("dependency link escapes cache")
			}
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) {
				return errors.New("absolute dependency link")
			}
			fmt.Fprintf(h, "%d:%s", len(link), link)
			return nil
		}
		if !d.Type().IsRegular() {
			return errors.New("dependency must be a regular file")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%d:", len(data))
		h.Write(data)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil)), err
}
