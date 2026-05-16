package sanitize

import (
	"regexp"
	"strings"
)

// RiskLevel represents the risk level of a command
type RiskLevel string

// Risk is the public command-risk classification used by bridge consumers.
type Risk = RiskLevel

const (
	// RiskSafe indicates a command is considered safe
	RiskSafe RiskLevel = "safe"
	// RiskDestructive indicates a command may be destructive
	RiskDestructive RiskLevel = "destructive"
)

// riskPattern represents a pattern for detecting destructive commands
type riskPattern struct {
	Pattern *regexp.Regexp
	Name    string
}

// destructivePatterns contains patterns for detecting destructive commands
var destructivePatterns = []riskPattern{
	// File deletion
	{Name: "rm -rf", Pattern: regexp.MustCompile(`\brm\s+(-[a-zA-Z]*r[a-zA-Z]*f|--recursive\s+--force|-[a-zA-Z]*f[a-zA-Z]*r)\b`)},
	{Name: "rm -r", Pattern: regexp.MustCompile(`\brm\s+-[a-zA-Z]*r\b`)},
	{Name: "rm -f", Pattern: regexp.MustCompile(`\brm\s+-[a-zA-Z]*f\b`)},
	{Name: "rmdir", Pattern: regexp.MustCompile(`\brmdir\b`)},

	// SQL destructive operations
	{Name: "DROP TABLE", Pattern: regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`)},
	{Name: "DROP DATABASE", Pattern: regexp.MustCompile(`(?i)\bDROP\s+DATABASE\b`)},
	{Name: "TRUNCATE", Pattern: regexp.MustCompile(`(?i)\bTRUNCATE\b`)},
	{Name: "DELETE FROM", Pattern: regexp.MustCompile(`(?i)\bDELETE\s+FROM\b`)},

	// Git destructive operations
	{Name: "git force push", Pattern: regexp.MustCompile(`\bgit\s+(push\s+)?(-[a-zA-Z]*f|--force)\b`)},
	{Name: "git reset hard", Pattern: regexp.MustCompile(`\bgit\s+reset\s+--hard\b`)},
	{Name: "git clean -f", Pattern: regexp.MustCompile(`\bgit\s+clean\s+-[a-zA-Z]*[fd]`)},
	{Name: "git checkout .", Pattern: regexp.MustCompile(`\bgit\s+checkout\s+\.`)},

	// Permission changes
	{Name: "chmod 777", Pattern: regexp.MustCompile(`\bchmod\s+777\b`)},
	{Name: "chmod -R", Pattern: regexp.MustCompile(`\bchmod\s+-[a-zA-Z]*R\b`)},
	{Name: "chown -R", Pattern: regexp.MustCompile(`\bchown\s+-[a-zA-Z]*R\b`)},

	// Disk operations
	{Name: "write to device", Pattern: regexp.MustCompile(`>\s*/dev/(sda|hda|nvme|vda|xvda|disk)\b`)},
	{Name: "dd to device", Pattern: regexp.MustCompile(`\bdd\s+.*of=/dev/(sda|hda|nvme|vda|xvda|disk)`)},
	{Name: "mkfs", Pattern: regexp.MustCompile(`\bmkfs\b`)},
	{Name: "fdisk", Pattern: regexp.MustCompile(`\bfdisk\b`)},

	// System operations
	{Name: "shutdown", Pattern: regexp.MustCompile(`\bshutdown\b`)},
	{Name: "reboot", Pattern: regexp.MustCompile(`\breboot\b`)},
	{Name: "init 0", Pattern: regexp.MustCompile(`\binit\s+[06]\b`)},
	{Name: "systemctl stop", Pattern: regexp.MustCompile(`\bsystemctl\s+stop\b`)},

	// Package management destructive
	{Name: "apt remove", Pattern: regexp.MustCompile(`\b(apt|apt-get)\s+(remove|purge)\b`)},
	{Name: "brew uninstall", Pattern: regexp.MustCompile(`\bbrew\s+uninstall\b`)},
	{Name: "npm uninstall global", Pattern: regexp.MustCompile(`\bnpm\s+(uninstall|remove)\s+-g\b`)},

	// Kill processes
	{Name: "kill -9", Pattern: regexp.MustCompile(`\bkill\s+-9\b`)},
	{Name: "killall", Pattern: regexp.MustCompile(`\bkillall\b`)},
	{Name: "pkill", Pattern: regexp.MustCompile(`\bpkill\b`)},

	// Docker destructive
	{Name: "docker rm -f", Pattern: regexp.MustCompile(`\bdocker\s+(rm|container\s+rm)\s+-[a-zA-Z]*f\b`)},
	{Name: "docker system prune", Pattern: regexp.MustCompile(`\bdocker\s+system\s+prune\b`)},
	{Name: "docker volume rm", Pattern: regexp.MustCompile(`\bdocker\s+volume\s+rm\b`)},

	// Kubernetes destructive
	{Name: "kubectl delete", Pattern: regexp.MustCompile(`\bkubectl\s+delete\b`)},
}

// IsDestructive checks if a command contains destructive patterns
func IsDestructive(command string) bool {
	// Normalize whitespace
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}

	for _, p := range destructivePatterns {
		if p.Pattern.MatchString(cmd) {
			return true
		}
	}
	return false
}

// GetRiskLevel returns the risk level for a command
func GetRiskLevel(command string) RiskLevel {
	if IsDestructive(command) {
		return RiskDestructive
	}
	return RiskSafe
}

// ClassifyRisk returns the command risk classification.
func ClassifyRisk(command string) Risk {
	return GetRiskLevel(command)
}

// GetDestructivePatterns returns the list of destructive command patterns
// Useful for testing and documentation
func GetDestructivePatterns() []string {
	patterns := make([]string, len(destructivePatterns))
	for i, p := range destructivePatterns {
		patterns[i] = p.Name
	}
	return patterns
}
