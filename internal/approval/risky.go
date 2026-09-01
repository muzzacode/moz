package approval

import (
	"regexp"
	"strings"
)

// Risk describes why a shell command was flagged.
type Risk struct {
	// Reason is a short human-readable explanation.
	Reason string
	// Detail is the specific fragment that triggered the flag.
	Detail string
}

// riskyPattern flags a shell command that reaches outside the project.
type riskyPattern struct {
	re     *regexp.Regexp
	reason string
}

// riskyPatterns catch commands whose effects escape the workspace.
//
// The file tools are sandboxed by safepath, but exec runs an arbitrary shell and
// is not. A model told to make a build pass will happily install a package
// globally or symlink into a system bin directory, which is a change to the
// machine rather than to the project.
var riskyPatterns = []riskyPattern{
	{regexp.MustCompile(`(?i)\bsudo\b`), "runs as root"},
	{regexp.MustCompile(`(?i)\bsu\s`), "switches user"},

	// Writes into system or shared locations.
	{regexp.MustCompile(`(^|\s|>)/(usr|opt|etc|bin|sbin|Library|System|var)(/|\s|$)`), "touches a system directory"},
	{regexp.MustCompile(`(?i)\b(ln|cp|mv|install|tee)\b[^|;&]*\s/(usr|opt|etc|bin|sbin|Library|System)/`), "writes into a system directory"},

	// Global package installs change every project on the machine.
	{regexp.MustCompile(`(?i)\bnpm\s+(i|install)\b[^|;&]*\s-g\b`), "installs a global npm package"},
	{regexp.MustCompile(`(?i)\b(npm|yarn|pnpm)\s+global\b`), "installs a global package"},
	{regexp.MustCompile(`(?i)\bbrew\s+(install|uninstall|link|unlink|upgrade)\b`), "changes Homebrew packages"},
	{regexp.MustCompile(`(?i)\bgo\s+install\b`), "installs a Go binary globally"},
	{regexp.MustCompile(`(?i)\bcargo\s+install\b`), "installs a Rust binary globally"},

	// Shell and tool configuration outside the project.
	{regexp.MustCompile(`(?i)(\.zshrc|\.bashrc|\.bash_profile|\.profile|\.zprofile|\.zshenv)`), "edits shell configuration"},
	{regexp.MustCompile(`(?i)\bgit\s+config\s+--global\b`), "changes global git configuration"},
	{regexp.MustCompile(`(?i)\blaunchctl\b|\bcrontab\b|\bsystemctl\b`), "changes system services or scheduled jobs"},
	{regexp.MustCompile(`(?i)\bdefaults\s+write\b`), "changes macOS defaults"},

	// Irreversible or remote-affecting actions.
	{regexp.MustCompile(`(?i)\brm\s+(-[a-z]*r[a-z]*f|-[a-z]*f[a-z]*r)\b`), "recursive force delete"},
	{regexp.MustCompile(`(?i)\bgit\s+push\b[^|;&]*(--force|-f)\b`), "force pushes"},
	{regexp.MustCompile(`(?i)\bgit\s+(reset\s+--hard|clean\s+-[a-z]*f)`), "discards uncommitted work"},
	{regexp.MustCompile(`(?i)\bcurl\b[^|;&]*\|\s*(ba)?sh\b`), "pipes a download into a shell"},
	{regexp.MustCompile(`(?i)\bwget\b[^|;&]*\|\s*(ba)?sh\b`), "pipes a download into a shell"},

	// Home directory writes outside the project.
	{regexp.MustCompile(`(?i)(^|\s)(>|>>)\s*~?/?\.(ssh|aws|gnupg|kube|docker)/`), "writes to a credential directory"},
}

// ClassifyCommand reports whether a shell command reaches outside the project.
//
// It is intentionally conservative: a false positive costs one approval prompt,
// while a false negative silently changes the user's machine.
func ClassifyCommand(command string) (Risk, bool) {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return Risk{}, false
	}

	for _, p := range riskyPatterns {
		if m := p.re.FindString(cmd); m != "" {
			return Risk{Reason: p.reason, Detail: strings.TrimSpace(m)}, true
		}
	}
	if detail, ok := looksLikeGlobalPipInstall(cmd); ok {
		return Risk{Reason: "installs a Python package outside the project", Detail: detail}, true
	}
	return Risk{}, false
}

// pipInstallRe finds a pip install invocation and captures its arguments.
//
// Go's regexp has no negative lookahead, so the distinction between installing
// declared project dependencies and installing something new is made in code.
var pipInstallRe = regexp.MustCompile(`(?i)\bpip3?\s+install\b([^|;&]*)`)

// looksLikeGlobalPipInstall reports whether a pip install adds a package rather
// than materialising dependencies the project already declares.
func looksLikeGlobalPipInstall(cmd string) (string, bool) {
	m := pipInstallRe.FindStringSubmatch(cmd)
	if m == nil {
		return "", false
	}
	args := strings.Fields(m[1])
	if len(args) == 0 {
		return "", false
	}
	for _, a := range args {
		switch {
		// Installing from a requirements file or the project itself is ordinary.
		case a == "-r" || a == "--requirement",
			a == "-e" || a == "--editable",
			a == "." || a == "-":
			return "", false
		}
	}
	return strings.TrimSpace(m[0]), true
}
