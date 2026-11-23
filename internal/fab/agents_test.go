package fab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Un `tools:` en el frontmatter no puede recortar las herramientas del experto: eso lo decide
// la llamada. Antes UX quedaba sin Playwright aunque su llamada lo habilitara.
func TestAgentsSinListaCerradaDeHerramientas(t *testing.T) {
	dir := t.TempDir()
	md := filepath.Join(dir, "ux.md")
	_ = os.WriteFile(md, []byte("---\nname: ux\ndescription: revisor\ntools: Read, Grep, Glob\n---\nEres el revisor."), 0o644)
	out := filepath.Join(dir, "agents.json")
	if err := LoadAgents(map[string]string{"ux": md}, out); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	if strings.Contains(string(b), `"tools"`) {
		t.Fatalf("agents.json no puede llevar una lista de herramientas:\n%s", b)
	}
	if !strings.Contains(string(b), "Eres el revisor.") {
		t.Fatal("se perdió el prompt")
	}
}
