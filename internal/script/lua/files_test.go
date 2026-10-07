package lua

import "testing"

// TestLuaCheck проверяет разбор Lua без выполнения: верный текст, синтаксическая ошибка со
// строкой, ошибка компиляции.
func TestLuaCheck(t *testing.T) {
	t.Parallel()
	l := luaLanguage{}
	if p := l.Check([]byte("local x = 1\nmkey.tap('A')\n")); p != nil {
		t.Fatalf("valid: %+v", p)
	}
	if p := l.Check([]byte("local x = 1\nif x then\n")); p == nil || p.Line < 2 || p.Message == "" {
		t.Fatalf("syntax: %+v", p)
	}
	if p := l.Check([]byte("x = = 2")); p == nil || p.Line != 1 {
		t.Fatalf("line 1: %+v", p)
	}
}
