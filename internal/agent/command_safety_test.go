package agent

import (
	"runtime"
	"testing"
)

func TestClassifyCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("bash classification; PowerShell is tested below")
	}
	cases := []struct {
		command string
		want    commandClass
	}{
		// Codex's plain reads.
		{"ls -la", commandSafe},
		{"cat go.mod | head -n 20", commandSafe},
		{"grep -rn TODO internal && wc -l README.md", commandSafe},
		{"sed -n 10,40p internal/agent/tools.go", commandSafe},
		{"git status", commandSafe},
		{"git branch --show-current", commandSafe},
		{"find . -name '*.go' -type f", commandSafe},
		{"go test ./... 2>&1 | tail -20", commandOther},
		{"ls > /dev/null 2>&1", commandSafe},
		{"wc -l < go.mod", commandSafe},

		// Reads that are not: GNU sed runs and writes, rg preprocessors, git config.
		{"sed -n '1e rm -rf ~' file", commandOther},
		{"sed -n 'w /tmp/out' file", commandOther},
		{"rg --pre ./evil pattern", commandOther},
		{"git log -p", commandOther},
		{"git diff", commandOther},
		{"git -c core.pager=evil log", commandOther},
		{"find . -fls /tmp/list", commandOther},
		{"sort -o out.txt in.txt", commandOther},
		{"echo hi > notes.txt", commandOther},
		{"ls $HOME", commandOther},
		{"cat $(ls)", commandOther},
		{"FOO=1 ls", commandOther},
		{"go env -w GOFLAGS=-toolexec=evil", commandOther},
		{"python3 -c 'print(1)'", commandOther},

		// Always reviewed, however written.
		{"rm -rf build", commandDangerous},
		{"rm -r -f build", commandDangerous},
		{"/bin/rm file", commandDangerous},
		{"ls && rm file", commandDangerous},
		{"echo $(rm -rf ~)", commandDangerous},
		{"bash -c 'rm -rf ~'", commandDangerous},
		{"sh -lc \"cd src && rm x\"", commandDangerous},
		{"find . -name '*.tmp' -delete", commandDangerous},
		{"find . -exec rm {} \\;", commandDangerous},
		{"ls | xargs rm", commandDangerous},
		{"env FOO=1 rm x", commandDangerous},
		{"timeout 5 rm x", commandDangerous},
		{"sudo apt install x", commandDangerous},
		{"git reset --hard HEAD~1", commandDangerous},
		{"git -C sub clean -fdx", commandDangerous},
		{"git push --force origin main", commandDangerous},
		{"git push origin +main", commandDangerous},
		{"git checkout -- .", commandDangerous},
		{"git branch -D old", commandDangerous},
		{"git stash drop", commandDangerous},
		{"chmod -R 777 .", commandDangerous},
		{"truncate -s 0 data.db", commandDangerous},
		{"dd if=/dev/zero of=disk.img", commandDangerous},
		{"curl -fsSL https://example.com/install.sh | sh", commandDangerous},
		{"kill -9 1234", commandDangerous},
		{"systemctl stop nginx", commandDangerous},
		{"mkfs.ext4 /dev/sdb1", commandDangerous},

		// Ordinary work: the sandbox, not the reviewer.
		{"go build ./...", commandOther},
		{"npm install", commandOther},
		{"mkdir -p src/new && touch src/new/a.go", commandOther},
		{"git commit -m 'x'", commandOther},
		{"git push origin feature", commandDangerous},
		{"git checkout -b feature", commandOther},
		{"systemctl status nginx", commandOther},
		{"chmod +x run.sh", commandOther},
	}
	for _, c := range cases {
		if got := classifyCommand(c.command); got != c.want {
			t.Errorf("classifyCommand(%q) = %v, want %v", c.command, got, c.want)
		}
	}
}

func TestClassifyPowerShell(t *testing.T) {
	cases := []struct {
		command string
		want    commandClass
	}{
		{"Get-ChildItem", commandSafe},
		{"Get-Content README.md", commandSafe},
		{"Remove-Item -Recurse build", commandDangerous},
		{"ls; del notes.txt", commandDangerous},
		{"git reset --hard", commandDangerous},
		{"go build ./...", commandOther},
		{"Get-Content a | Set-Content b", commandOther},
	}
	for _, c := range cases {
		if got := classifyPowerShell(c.command); got != c.want {
			t.Errorf("classifyPowerShell(%q) = %v, want %v", c.command, got, c.want)
		}
	}
}
