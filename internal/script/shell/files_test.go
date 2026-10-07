package shell

import (
	"os/exec"
	"testing"
)

// TestShellCheck проверяет синтаксис bash без выполнения: верный текст (команда не выполняется),
// ошибка со строкой.
func TestShellCheck(t *testing.T) {
	t.Parallel()
	sh := "sh"
	if _, err := exec.LookPath("bash"); err == nil {
		sh = "bash"
	}
	l := shellLanguage{m: &Module{cfg: Config{Shell: sh}}}
	if p := l.Check([]byte("touch /nonexistent/should-not-run\necho ok\n")); p != nil {
		t.Fatalf("valid: %+v", p)
	}
	p := l.Check([]byte("echo 1\nif true; then\necho 2\n"))
	if p == nil || p.Message == "" {
		t.Fatalf("syntax: %+v", p)
	}
	if sh == "bash" && p.Line == 0 {
		t.Errorf("no line in %q", p.Message)
	}
}
