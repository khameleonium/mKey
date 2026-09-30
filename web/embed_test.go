package web

import (
	"io/fs"
	"strings"
	"testing"
)

// TestFSHasIndex проверяет, что веб-интерфейс (собранный или заглушка) всегда содержит index.html.
func TestFSHasIndex(t *testing.T) {
	t.Parallel()

	// Читаем index.html из встроенной файловой системы.
	data, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatalf("index.html: %v", err)
	}

	// Это HTML-страница mKey.
	if !strings.Contains(string(data), "mKey") {
		t.Fatalf("index.html does not look like mKey page")
	}
}
