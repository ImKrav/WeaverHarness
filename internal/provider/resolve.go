package provider

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
)

func defaultBinFor(providerName string) string {
	switch providerName {
	case "claude":
		return "claude"
	case "codex":
		return "codex"
	case "opencode":
		return "opencode"
	case "antigravity":
		return "agy"
	default:
		return providerName
	}
}

func resolveBinary(providerName, configured string) (string, error) {
	home, _ := os.UserHomeDir()
	return resolveBinaryWith(runtime.GOOS, home, os.Getenv, exec.LookPath, providerName, configured)
}

func resolveBinaryWith(
	goos, home string,
	getenv func(string) string,
	lookPath func(string) (string, error),
	providerName, configured string,
) (string, error) {
	bin := strings.TrimSpace(configured)
	if bin == "" {
		bin = defaultBinFor(providerName)
	}
	if bin == "" {
		return "", fmt.Errorf("provider %q sin binario configurado", providerName)
	}

	if resolved, err := lookPath(bin); err == nil {
		return resolved, nil
	} else if strings.ContainsAny(bin, "/\\") {
		return "", err
	} else {
		for _, cand := range candidatePaths(goos, home, getenv, providerName, bin) {
			info, statErr := os.Stat(cand)
			if statErr != nil || info.IsDir() {
				continue
			}
			if goos != "windows" && info.Mode()&0o111 == 0 {
				continue
			}
			return cand, nil
		}
		return "", err
	}
}

func candidatePaths(goos, home string, getenv func(string) string, providerName, bin string) []string {
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	var out []string
	add := func(p string) {
		if p != "" {
			out = append(out, p)
		}
	}

	switch goos {
	case "darwin":
		h := strings.TrimRight(home, "/")
		add(path.Join("/opt/homebrew/bin", bin))
		add(path.Join("/usr/local/bin", bin))
		if h != "" {
			add(path.Join(h, ".local", "bin", bin))
			add(path.Join(h, ".opencode", "bin", bin))
			add(path.Join(h, ".npm-global", "bin", bin))
		}
		if providerName == "antigravity" || bin == "agy" {
			add("/Applications/Antigravity.app/Contents/Resources/app/bin/agy")
			add("/Applications/Antigravity.app/Contents/MacOS/agy")
			if h != "" {
				add(path.Join(h, "Applications/Antigravity.app/Contents/Resources/app/bin/agy"))
			}
		}

	case "linux":
		h := strings.TrimRight(home, "/")
		if xdgBin := strings.TrimRight(getenv("XDG_BIN_HOME"), "/"); xdgBin != "" {
			add(path.Join(xdgBin, bin))
		}
		if h != "" {
			add(path.Join(h, ".local", "bin", bin))
			add(path.Join(h, ".opencode", "bin", bin))
			add(path.Join(h, ".npm-global", "bin", bin))
		}
		add(path.Join("/usr/local/bin", bin))
		add(path.Join("/home/linuxbrew/.linuxbrew/bin", bin))

	case "windows":
		h := strings.TrimRight(home, `\/`)
		exts := windowsExts(bin)
		if localApp := strings.TrimRight(getenv("LOCALAPPDATA"), `\/`); localApp != "" {
			for _, ext := range exts {
				add(winJoin(localApp, "Programs", bin, bin+ext))
				if providerName == "antigravity" || strings.EqualFold(bin, "agy") {
					add(winJoin(localApp, "Programs", "Antigravity", "bin", "agy"+ext))
				}
			}
		}
		if appData := strings.TrimRight(getenv("APPDATA"), `\/`); appData != "" {
			for _, ext := range exts {
				add(winJoin(appData, "npm", bin+ext))
			}
		}
		if h != "" {
			for _, ext := range exts {
				add(winJoin(h, ".local", "bin", bin+ext))
				add(winJoin(h, ".opencode", "bin", bin+ext))
				add(winJoin(h, "scoop", "shims", bin+ext))
			}
		}
	}
	return out
}

func windowsExts(bin string) []string {
	lower := strings.ToLower(bin)
	if strings.HasSuffix(lower, ".exe") || strings.HasSuffix(lower, ".cmd") || strings.HasSuffix(lower, ".bat") {
		return []string{""}
	}
	return []string{".exe", ".cmd"}
}

func winJoin(parts ...string) string {
	cleaned := make([]string, 0, len(parts))
	for i, p := range parts {
		p = strings.ReplaceAll(p, "/", `\`)
		if i > 0 {
			p = strings.TrimLeft(p, `\`)
		}
		p = strings.TrimRight(p, `\`)
		if p != "" {
			cleaned = append(cleaned, p)
		}
	}
	return strings.Join(cleaned, `\`)
}

// InstallHint returns an actionable configuration hint when a provider CLI is missing on goos.
func InstallHint(goos, providerName string) string {
	bin := defaultBinFor(providerName)
	var extra string
	switch goos {
	case "darwin":
		if providerName == "antigravity" {
			extra = " (en macOS suele estar en /Applications/Antigravity.app/... o Homebrew)"
		} else {
			extra = " (revisa /opt/homebrew/bin o ~/.local/bin)"
		}
	case "linux":
		extra = " (revisa ~/.local/bin, ~/.npm-global/bin o /usr/local/bin)"
	case "windows":
		extra = ` (revisa %APPDATA%\npm o %LOCALAPPDATA%\Programs)`
	}
	return fmt.Sprintf(`añade %q al PATH o configura [providers.%s] bin = "/ruta/a/%s" en config.toml%s`, bin, providerName, bin, extra)
}
