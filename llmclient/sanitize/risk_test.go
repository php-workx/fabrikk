package sanitize

import (
	"testing"
)

func TestIsDestructive(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		expected bool
	}{
		// File deletion
		{
			name:     "rm -rf",
			command:  "rm -rf /tmp/test",
			expected: true,
		},
		{
			name:     "rm -rf with flags reversed",
			command:  "rm -fr /tmp/test",
			expected: true,
		},
		{
			name:     "rm -r recursive",
			command:  "rm -r ./dir",
			expected: true,
		},
		{
			name:     "rm -f force",
			command:  "rm -f file.txt",
			expected: true,
		},
		{
			name:     "rm --recursive --force",
			command:  "rm --recursive --force dir/",
			expected: true,
		},
		{
			name:     "rmdir",
			command:  "rmdir empty_dir",
			expected: true,
		},
		{
			name:     "safe rm",
			command:  "rm file.txt",
			expected: false,
		},

		// SQL operations
		{
			name:     "DROP TABLE",
			command:  "psql -c 'DROP TABLE users'",
			expected: true,
		},
		{
			name:     "drop table lowercase",
			command:  "mysql -e 'drop table customers'",
			expected: true,
		},
		{
			name:     "DROP DATABASE",
			command:  "DROP DATABASE production",
			expected: true,
		},
		{
			name:     "TRUNCATE",
			command:  "TRUNCATE TABLE logs",
			expected: true,
		},
		{
			name:     "DELETE FROM",
			command:  "DELETE FROM users WHERE id > 0",
			expected: true,
		},
		{
			name:     "safe SELECT",
			command:  "SELECT * FROM users",
			expected: false,
		},

		// Git operations
		{
			name:     "git push --force",
			command:  "git push --force origin main",
			expected: true,
		},
		{
			name:     "git push -f",
			command:  "git push -f",
			expected: true,
		},
		{
			name:     "git reset --hard",
			command:  "git reset --hard HEAD~1",
			expected: true,
		},
		{
			name:     "git clean -f",
			command:  "git clean -f",
			expected: true,
		},
		{
			name:     "git clean -fd",
			command:  "git clean -fd",
			expected: true,
		},
		{
			name:     "git checkout .",
			command:  "git checkout .",
			expected: true,
		},
		{
			name:     "safe git push",
			command:  "git push origin feature",
			expected: false,
		},
		{
			name:     "safe git commit",
			command:  "git commit -m 'message'",
			expected: false,
		},

		// Permission changes
		{
			name:     "chmod 777",
			command:  "chmod 777 /var/www",
			expected: true,
		},
		{
			name:     "chmod -R",
			command:  "chmod -R 755 /home/user",
			expected: true,
		},
		{
			name:     "chown -R",
			command:  "chown -R root:root /etc",
			expected: true,
		},
		{
			name:     "safe chmod",
			command:  "chmod 644 file.txt",
			expected: false,
		},

		// Disk operations
		{
			name:     "dd if=",
			command:  "dd if=/dev/zero of=/dev/sda",
			expected: true,
		},
		{
			name:     "write to sda",
			command:  "echo 'data' > /dev/sda",
			expected: true,
		},
		{
			name:     "mkfs",
			command:  "mkfs.ext4 /dev/sdb1",
			expected: true,
		},
		{
			name:     "fdisk",
			command:  "fdisk /dev/sda",
			expected: true,
		},
		{
			name:     "safe dd",
			command:  "dd if=/dev/zero of=./test.img bs=1M count=100",
			expected: false,
		},

		// System operations
		{
			name:     "shutdown",
			command:  "shutdown -h now",
			expected: true,
		},
		{
			name:     "reboot",
			command:  "reboot",
			expected: true,
		},
		{
			name:     "init 0",
			command:  "init 0",
			expected: true,
		},
		{
			name:     "systemctl stop",
			command:  "systemctl stop nginx",
			expected: true,
		},

		// Package management
		{
			name:     "apt remove",
			command:  "apt remove nginx",
			expected: true,
		},
		{
			name:     "apt-get purge",
			command:  "apt-get purge mysql-server",
			expected: true,
		},
		{
			name:     "brew uninstall",
			command:  "brew uninstall node",
			expected: true,
		},
		{
			name:     "npm uninstall global",
			command:  "npm uninstall -g typescript",
			expected: true,
		},
		{
			name:     "safe apt install",
			command:  "apt install nginx",
			expected: false,
		},

		// Process killing
		{
			name:     "kill -9",
			command:  "kill -9 12345",
			expected: true,
		},
		{
			name:     "killall",
			command:  "killall node",
			expected: true,
		},
		{
			name:     "pkill",
			command:  "pkill -f python",
			expected: true,
		},
		{
			name:     "safe kill",
			command:  "kill 12345",
			expected: false,
		},

		// Docker operations
		{
			name:     "docker rm -f",
			command:  "docker rm -f container_name",
			expected: true,
		},
		{
			name:     "docker system prune",
			command:  "docker system prune -a",
			expected: true,
		},
		{
			name:     "docker volume rm",
			command:  "docker volume rm data_volume",
			expected: true,
		},
		{
			name:     "safe docker run",
			command:  "docker run -d nginx",
			expected: false,
		},

		// Kubernetes operations
		{
			name:     "kubectl delete",
			command:  "kubectl delete pod my-pod",
			expected: true,
		},
		{
			name:     "kubectl delete namespace",
			command:  "kubectl delete namespace production",
			expected: true,
		},
		{
			name:     "safe kubectl get",
			command:  "kubectl get pods",
			expected: false,
		},

		// Edge cases
		{
			name:     "empty command",
			command:  "",
			expected: false,
		},
		{
			name:     "whitespace only",
			command:  "   ",
			expected: false,
		},
		{
			name:     "safe ls command",
			command:  "ls -la",
			expected: false,
		},
		{
			name:     "safe cat command",
			command:  "cat /etc/passwd",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsDestructive(tt.command)
			if result != tt.expected {
				t.Errorf("IsDestructive(%q) = %v, want %v", tt.command, result, tt.expected)
			}
		})
	}
}

func TestGetRiskLevel(t *testing.T) {
	tests := []struct {
		command  string
		expected RiskLevel
	}{
		{"rm -rf /tmp", RiskDestructive},
		{"ls -la", RiskSafe},
		{"git push --force", RiskDestructive},
		{"git status", RiskSafe},
		{"DROP TABLE users", RiskDestructive},
		{"SELECT * FROM users", RiskSafe},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			result := GetRiskLevel(tt.command)
			if result != tt.expected {
				t.Errorf("GetRiskLevel(%q) = %v, want %v", tt.command, result, tt.expected)
			}
		})
	}
}

func TestGetDestructivePatterns(t *testing.T) {
	patterns := GetDestructivePatterns()
	if len(patterns) == 0 {
		t.Error("GetDestructivePatterns() returned empty list")
	}

	// Verify we have some expected pattern names
	expectedPatterns := map[string]bool{
		"rm -rf":         false,
		"DROP TABLE":     false,
		"git force push": false,
		"chmod 777":      false,
		"kubectl delete": false,
	}

	for _, name := range patterns {
		if _, ok := expectedPatterns[name]; ok {
			expectedPatterns[name] = true
		}
	}

	for name, found := range expectedPatterns {
		if !found {
			t.Errorf("Expected pattern %q not found in GetDestructivePatterns()", name)
		}
	}
}

func TestRiskLevel_String(t *testing.T) {
	if string(RiskSafe) != "safe" {
		t.Errorf("RiskSafe = %q, want %q", RiskSafe, "safe")
	}
	if string(RiskDestructive) != "destructive" {
		t.Errorf("RiskDestructive = %q, want %q", RiskDestructive, "destructive")
	}
}
